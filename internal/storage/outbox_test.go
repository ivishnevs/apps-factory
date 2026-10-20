package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func fillOutbox(t *testing.T, s *Store, n int) {
	t.Helper()
	for i := range n {
		if _, err := s.DB.Exec("INSERT INTO outbox(id,subject,body,company_id) VALUES(?,?,?,?)", fmt.Sprintf("m%03d", i), "factory.default.events.x", "{}", DefaultCompany); err != nil {
			t.Fatal(err)
		}
	}
}

// TestParallelPublishersPublishEachRowOnce (postgres-tenancy 3.2): two Core processes' publishers take
// disjoint batches (FOR UPDATE SKIP LOCKED) and together publish every outbox row exactly once.
func TestParallelPublishersPublishEachRowOnce(t *testing.T) {
	s, err := Open(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	const rows = 250
	fillOutbox(t, s, rows)
	var mu sync.Mutex
	published := map[string]int{}
	publish := func(ctx context.Context, o Outbox) error {
		mu.Lock()
		published[o.ID]++
		mu.Unlock()
		time.Sleep(time.Millisecond) // a broker round trip: the other publisher runs meanwhile
		return nil
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				n, err := s.PublishOutbox(context.Background(), publish, nil)
				if err != nil {
					errs <- err
					return
				}
				if n == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if len(published) != rows {
		t.Fatalf("%d of %d rows published", len(published), rows)
	}
	for id, n := range published {
		if n != 1 {
			t.Fatalf("%s published %d times", id, n)
		}
	}
	var unsent int
	if err = s.DB.QueryRow("SELECT count(*) FROM outbox WHERE sent=0").Scan(&unsent); err != nil || unsent != 0 {
		t.Fatal(unsent, err)
	}
}

// TestCrashBeforeMarkRepublishesTheSameID (postgres-tenancy 3.2): a publisher that stops after the broker
// accepted a message but before its transaction marked it sent leaves the row unsent; the next publisher
// publishes it again with the same ID (JetStream deduplicates by that ID). A failed publish is retried
// later and does not hold back the rest.
func TestCrashBeforeMarkRepublishesTheSameID(t *testing.T) {
	s, err := Open(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	fillOutbox(t, s, 3)
	var first []string
	ctx, crash := context.WithCancel(context.Background())
	_, err = s.PublishOutbox(ctx, func(_ context.Context, o Outbox) error {
		first = append(first, o.ID)
		if len(first) == 2 {
			crash() // Core dies after the broker acknowledged m001
		}
		return nil
	}, nil)
	if err == nil {
		t.Fatal("the crashed batch committed")
	}
	var again []string
	if _, err = s.PublishOutbox(context.Background(), func(_ context.Context, o Outbox) error {
		again = append(again, o.ID)
		if o.ID == "m002" {
			return errors.New("broker unavailable")
		}
		return nil
	}, nil); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(first) != "[m000 m001]" || fmt.Sprint(again) != "[m000 m001 m002]" {
		t.Fatal(first, again)
	}
	var sent, attempts int
	if err = s.DB.QueryRow("SELECT count(*) FILTER (WHERE sent=1), max(attempts) FROM outbox").Scan(&sent, &attempts); err != nil || sent != 2 || attempts != 1 {
		t.Fatal(sent, attempts, err)
	}
}

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/protocol"

	"github.com/nats-io/nats.go"
)

// TestJetStreamFeedDeliversEachRowWithItsSeq (trigger-log-scheduling 3.4): every feed row reaches JetStream on
// factory.<company>.feed.<project>.<kind> once, in order, carrying the row's seq; publishing the same rows
// again after a crash before they were marked sent delivers nothing new (same message IDs).
func TestJetStreamFeedDeliversEachRowWithItsSeq(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to a dedicated JetStream instance")
	}
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	defer b.JS.DeleteStream("FACTORY_COMMANDS")
	defer b.JS.DeleteStream("FACTORY_EVENTS")
	s := engineStore(t)
	ctx := context.Background()
	dispatchEngineTask(t, s, readyEngineTask(t, s, createEngineTask(t, s, "a")), "run-a")
	readyEngineTask(t, s, createEngineTask(t, s, "b"))
	rows, err := s.Feed(ctx, DefaultCompany, FeedFilter{}, 0, 100)
	if err != nil || len(rows.Items) < 6 {
		t.Fatal(len(rows.Items), err)
	}
	publish := func(ctx context.Context, o Outbox) error { return b.Publish(ctx, o.Subject, o.ID, o.Body) }
	for {
		n, err := s.PublishOutbox(ctx, publish, nil)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	// A crash after the publish and before the mark: the rows are published again with the same IDs.
	if _, err = s.DB.Exec("UPDATE outbox SET sent=0 WHERE id LIKE 'feed-%'"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PublishOutbox(ctx, publish, nil); err != nil {
		t.Fatal(err)
	}
	sub, err := b.Subscribe(protocol.FeedSubject(DefaultCompany, "*", ">"), "feed-seq-test")
	if err != nil {
		t.Fatal(err)
	}
	var got []FeedItem
	for {
		msgs, err := sub.Fetch(1, nats.MaxWait(500*time.Millisecond))
		if errors.Is(err, nats.ErrTimeout) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		var e protocol.Envelope
		var item FeedItem
		if json.Unmarshal(msgs[0].Data, &e) != nil || json.Unmarshal(e.Payload, &item) != nil {
			t.Fatal(string(msgs[0].Data))
		}
		if msgs[0].Subject != protocol.FeedSubject(DefaultCompany, item.ProjectID, item.Type) {
			t.Fatal(msgs[0].Subject, item.Type)
		}
		got = append(got, item)
		_ = msgs[0].AckSync()
	}
	if len(got) != len(rows.Items) {
		t.Fatalf("%d messages for %d feed rows", len(got), len(rows.Items))
	}
	for i, item := range got {
		if want := rows.Items[i]; item.Sequence != want.Sequence || item.EventID != want.EventID || item.Type != want.Type {
			t.Fatalf("message %d: seq %d %s, row seq %d %s", i, item.Sequence, item.Type, want.Sequence, want.Type)
		}
	}
}

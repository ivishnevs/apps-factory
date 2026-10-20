package storage

import (
	"context"
	"errors"
	"sync"
	"testing"

	"appfactory/internal/messaging"
)

// TestConcurrentAnswersFirstWins (hierarchy 2.4): two principals answer one question at the same time; one
// answer applies, the other is refused as a conflict, and a later answer learns the recorded one.
func TestConcurrentAnswersFirstWins(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	_, _, _, m := askOnTask(t, s, "manager")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, sender := range []string{"manager", "owner"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.ReplyMessage(ctx, DefaultCompany, m.ID, messaging.Reply{Version: m.Version, Sender: sender, Body: "answer by " + sender, RequestID: "answer-" + sender})
		}()
	}
	wg.Wait()
	applied := 0
	for _, err := range errs {
		switch {
		case err == nil:
			applied++
		case !errors.Is(err, ErrConflict):
			t.Fatal(err)
		}
	}
	if applied != 1 {
		t.Fatal("applied", applied, errs)
	}
	answer, err := s.AnswerFor(ctx, DefaultCompany, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ReplyMessage(ctx, DefaultCompany, m.ID, messaging.Reply{Version: m.Version + 1, Sender: "owner", Body: "late", RequestID: "late"})
	var answered *AnsweredError
	if !errors.As(err, &answered) || answered.Answer.ID != answer.ID || !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

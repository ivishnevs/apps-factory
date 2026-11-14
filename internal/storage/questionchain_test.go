package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/messaging"
)

// question-chain-limit: the loop guard counts questions between one pair of agents about one task in a recent window,
// since the owner last wrote on it, and refuses the one past the limit instead of escalating it.

func chainStore(t *testing.T) (*Store, *clock.Fake) {
	t.Helper()
	s := engineStore(t)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	createEngineTask(t, s, "hub")
	return s, fake
}

func ask(s *Store, task, from, to, body string) (messaging.Message, error) {
	return s.SendMessage(context.Background(), DefaultCompany, messaging.Send{ProjectID: "p", TaskID: task, Sender: from, Recipient: to, MessageType: "question", Body: body, RequestID: "q-" + from + to + body})
}

func refusedAs(err error, reason string) bool {
	var r *ExitRefusal
	return errors.As(err, &r) && r.Reason == reason
}

// A pair that asks each other eight times about a task within the window is refused on the ninth; nothing is stored.
func TestQuestionChainRefusesLoop(t *testing.T) {
	s, fake := chainStore(t)
	for i := range 8 {
		from, to := "designer", "reviewer"
		if i%2 == 1 {
			from, to = to, from
		}
		if _, err := ask(s, "hub", from, to, fmt.Sprint("round ", i)); err != nil {
			t.Fatal(i, err)
		}
		fake.Advance(time.Minute)
	}
	if _, err := ask(s, "hub", "designer", "reviewer", "round 8"); !refusedAs(err, "question_limit") {
		t.Fatal("ninth question accepted", err)
	}
	var n int
	if err := s.DB.QueryRow("SELECT count(*) FROM messages WHERE task_id='hub' AND message_type='question'").Scan(&n); err != nil || n != 8 {
		t.Fatal(n, err)
	}
	// Another pair on the same task is a different chain.
	if _, err := ask(s, "hub", "designer", "producer", "production check"); err != nil {
		t.Fatal(err)
	}
}

// Review rounds spread over days stay under the limit; questions to and from people never count.
func TestQuestionChainReviewRoundsAndPeople(t *testing.T) {
	s, fake := chainStore(t)
	for day := range 4 {
		for round := range 4 {
			if _, err := ask(s, "hub", "designer", "reviewer", fmt.Sprint("critique ", day, round)); err != nil {
				t.Fatal(day, round, err)
			}
			fake.Advance(3 * time.Hour)
		}
		fake.Advance(12 * time.Hour)
	}
	for i := range 10 {
		if _, err := ask(s, "hub", "designer", "owner", fmt.Sprint("approval request ", i)); err != nil {
			t.Fatal(i, err)
		}
	}
}

// The owner writing on the task restarts the count.
func TestQuestionChainOwnerRestarts(t *testing.T) {
	ctx := context.Background()
	s, fake := chainStore(t)
	for i := range 8 {
		if _, err := ask(s, "hub", "designer", "reviewer", fmt.Sprint("round ", i)); err != nil {
			t.Fatal(i, err)
		}
	}
	fake.Advance(time.Minute)
	if _, err := s.SendMessage(ctx, DefaultCompany, messaging.Send{ProjectID: "p", TaskID: "hub", Sender: "owner", Recipient: "designer", MessageType: "notification", Body: "changes: see notes", RequestID: "owner-notes"}); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Minute)
	if _, err := ask(s, "hub", "designer", "reviewer", "after the owner's notes"); err != nil {
		t.Fatal(err)
	}
}

// A question escalated by the old lifetime limit is still answered by its recipient.
func TestOldChainEscalationAnswerable(t *testing.T) {
	ctx := context.Background()
	s, _ := chainStore(t)
	m, err := ask(s, "hub", "designer", "reviewer", "critique please")
	if err != nil {
		t.Fatal(err)
	}
	m.Status, m.DeliveryStatus, m.EscalationReason = "ESCALATED", "NONE", oldChainLimitReason
	b, _ := json.Marshal(m)
	if _, err = s.DB.Exec("UPDATE messages SET status='ESCALATED', body=? WHERE id=?", b, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReplyMessage(ctx, DefaultCompany, m.ID, messaging.Reply{Version: m.Version, Sender: "reviewer", Body: "changes: see notes", RequestID: "answer"}); err != nil {
		t.Fatal(err)
	}
}

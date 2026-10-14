package storage

import (
	"context"
	"errors"
	"testing"

	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

// TestEscalationCancelAndClose: a cancel answer with open children is refused with needs_cascade and the
// escalation stays open; once the child is closed the answer cancels the task. Closing a task clears its
// other escalations, and the answer to Factory's question is not delivered anywhere.
func TestEscalationCancelAndClose(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	parent := readyEngineTask(t, s, createEngineTask(t, s, "parent"))
	child, err := s.CreateTask(ctx, co, tasks.Create{ID: "child", ProjectID: "p", Title: "child", ParentTaskID: parent.ID, RequestID: "create-child"})
	if err != nil {
		t.Fatal(err)
	}
	other := readyEngineTask(t, s, createEngineTask(t, s, "other"))
	if err = s.SyncEscalations(ctx, co, "stalled", []EscalationWant{{TaskID: parent.ID, RefID: "x", Summary: "waits on x"}, {TaskID: other.ID, RefID: "y", Summary: "waits on y", Target: "owner"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncEscalations(ctx, co, "bogus", nil); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	open, _ := s.Escalations(ctx, co, true)
	byTask := map[string]Escalation{}
	for _, e := range open {
		byTask[e.TaskID] = e
	}
	if len(open) != 2 || byTask["parent"].Target != "owner" {
		t.Fatalf("%+v", open)
	}
	answer := func(e Escalation, choice, key string) error {
		q, err := s.Message(ctx, co, e.MessageID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: choice, Answers: map[string]any{EscalationAnswerID: choice}, RequestID: key})
		return err
	}
	if err = answer(byTask["parent"], "cancel", "c1"); !errors.Is(err, ErrNeedsCascade) {
		t.Fatal(err)
	}
	if left, _ := s.Escalations(ctx, co, true); len(left) != 2 {
		t.Fatal("refused answer closed the escalation", left)
	}
	if _, err = s.TaskAction(ctx, co, child.ID, tasks.Action{Version: child.Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	if err = answer(byTask["parent"], "cancel", "c2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Task(ctx, co, "parent"); got.Status != "CANCELLED" {
		t.Fatal(got.Status)
	}
	answered, _ := s.AnswerFor(ctx, co, byTask["parent"].MessageID)
	if answered.Recipient != "factory" || answered.DeliveryStatus != "NONE" {
		t.Fatalf("%+v", answered)
	}
	// Closing the other task clears its escalation and withdraws the question.
	other, _ = s.Task(ctx, co, "other")
	if _, err = s.TaskAction(ctx, co, other.ID, tasks.Action{Version: other.Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.Escalations(ctx, co, true); len(left) != 0 {
		t.Fatalf("%+v", left)
	}
	if m, _ := s.Message(ctx, co, byTask["other"].MessageID); m.Status != "WITHDRAWN" {
		t.Fatal(m.Status)
	}
}

package storage

import (
	"context"
	"errors"
	"strings"
	"testing"

	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// TestTaskKinds (task-kinds): an agent linking an OpenSpec change must say work or spec; a spec task's runs carry
// no change for the node to check; a work task that ends change_missing can be answered spec, which makes it a
// spec task and runs it again without the check.
func TestTaskKinds(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	create := func(id, change, kind, originRun string) (tasks.Task, error) {
		return s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "p", Title: id, Assignee: "demo", OpenSpecChange: change, Kind: kind, OriginRun: originRun, RequestID: "create-" + id})
	}
	if _, err := create("a", "new-change", "", "run-1"); !errors.Is(err, ErrInput) || !strings.Contains(err.Error(), "kind: spec") {
		t.Fatalf("an agent's linked task without a kind: %v", err)
	}
	if _, err := create("b", "", "conversation", ""); !errors.Is(err, ErrInput) {
		t.Fatalf("conversation is not settable: %v", err)
	}
	if v, err := create("c", "old-change", "", ""); err != nil || v.Kind != "" {
		t.Fatalf("a person's linked task defaults to work: %+v %v", v, err)
	}
	spec, err := create("s", "new-change", "spec", "run-1")
	if err != nil || spec.Kind != tasks.KindSpec {
		t.Fatalf("%+v %v", spec, err)
	}
	rs := dispatchEngineTask(t, s, readyEngineTask(t, s, spec), "rs")
	if rs.OpenSpecChange != "" {
		t.Fatalf("a spec task's run carries its change for the node to check: %q", rs.OpenSpecChange)
	}
	if _, err = s.TaskAction(ctx, co, "s", tasks.Action{Version: taskOf(t, s, "s").Version, Action: "cancel", Reason: "test"}); err != nil {
		t.Fatal(err)
	}

	work, err := create("w", "missing-change", "work", "run-1")
	if err != nil || work.Kind != "" {
		t.Fatalf("%+v %v", work, err)
	}
	r := dispatchEngineTask(t, s, readyEngineTask(t, s, work), "rw1")
	if r.OpenSpecChange != "missing-change" {
		t.Fatalf("a work task's run must carry its change: %q", r.OpenSpecChange)
	}
	lease, err := claimLease(t, s, co, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, r.ID, lease, protocol.Event{Status: "ENDED", Outcome: "change_missing"}); err != nil {
		t.Fatal(err)
	}
	open, _ := s.Escalations(ctx, co, true)
	var q string
	for _, e := range open {
		if e.TaskID == "w" && e.Reason == "change_missing" {
			if strings.Join(e.Answers, ",") != "spec,retry,cancel" {
				t.Fatalf("%+v", e)
			}
			q = e.MessageID
		}
	}
	if q == "" {
		t.Fatalf("no change_missing escalation: %+v", open)
	}
	m, _ := s.Message(ctx, co, q)
	if _, err = s.ReplyMessage(ctx, co, m.ID, messaging.Reply{Version: m.Version, Sender: "owner", Body: "spec", Answers: map[string]any{"answer": "spec"}, RequestID: "spec"}); err != nil {
		t.Fatal(err)
	}
	got := taskOf(t, s, "w")
	if got.Kind != tasks.KindSpec || got.Status != "READY" {
		t.Fatalf("answer spec: %+v", got)
	}
	if r = dispatchEngineTask(t, s, got, "rw2"); r.OpenSpecChange != "" {
		t.Fatalf("after spec the run still carries its change: %q", r.OpenSpecChange)
	}

	// work ⇄ spec by update; a conversation keeps its kind.
	k := tasks.KindWork
	c := taskOf(t, s, "c")
	if v, err := s.UpdateTask(ctx, co, "c", tasks.Update{Version: c.Version, Kind: &k, RequestID: "k1"}); err != nil || v.Kind != "" {
		t.Fatalf("%+v %v", v, err)
	}
}

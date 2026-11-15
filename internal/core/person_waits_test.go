package core

import (
	"context"
	"strings"
	"testing"

	"appfactory/internal/decisions"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// activeParks lists a task's active parks as kind:ref.
func activeParks(t *testing.T, h *exitHarness, task string) []string {
	t.Helper()
	rows, err := h.s.DB.Query("SELECT kind, ref_id FROM task_parks WHERE task_id=? AND status='active' ORDER BY seq", task)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k, r string
		_ = rows.Scan(&k, &r)
		out = append(out, k+":"+r)
	}
	return out
}

func waitOwner(h *exitHarness, r exitRun) (int, storage.ExitRefusal, string) {
	return h.callAt(r, "factory_exit_wait", map[string]any{"from": "owner", "reason": "the owner decides the prototype scope"}, h.ack(r))
}

// TestWaitOnOwnerLinksTheDecision replays 2026-11-13 (person-waits-on-items): a lead proposes a decision from the task
// it delegates from; the delegated task waits `from: owner`. Core parks it on the decision (the board shows "waits for
// your decision"), and recording the decision wakes it with the outcome; a sibling that is not waiting is untouched.
func TestWaitOnOwnerLinksTheDecision(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	// The lead's task stays in the backlog: only its id matters (the decision is proposed from it).
	if _, err := h.s.CreateTask(context.Background(), storage.DefaultCompany, tasks.Create{ID: "plan", ProjectID: "notes", Title: "plan", Assignee: "manager", RequestID: "c-plan"}); err != nil {
		t.Fatal(err)
	}
	var err error
	// Both are delegated from the lead's task (origin_task, as tasks created through Factory MCP); the sibling stays in
	// the backlog: not waiting, and not taking the agent's slot.
	h.create("build", "notes", "demo")
	if _, err = h.s.CreateTask(ctx, co, tasks.Create{ID: "sibling", ProjectID: "notes", Title: "sibling", Assignee: "demo", RequestID: "c-sibling"}); err != nil {
		t.Fatal(err)
	}
	if _, err = h.s.DB.Exec("UPDATE tasks SET body=jsonb_set(body::jsonb,'{origin_task}','\"plan\"')::json WHERE id IN ('build','sibling')"); err != nil {
		t.Fatal(err)
	}
	r := h.start("build")
	// The lead proposes while the delegated task runs.
	d, err := h.s.ProposeDecision(ctx, co, "notes", decisions.Propose{Topic: "prototype scope", Value: "kangaroo states first", Rationale: "r", TaskID: "plan", Actor: "manager", RequestID: "scope"})
	if err != nil {
		t.Fatal(err)
	}
	code, _, body := waitOwner(h, r)
	if code != 200 || !strings.Contains(body, d.ID) {
		t.Fatal("wait on the owner with an open decision", code, body)
	}
	h.end(r, "succeeded", "accepted")
	if got := activeParks(t, h, "build"); len(got) != 1 || got[0] != tasks.ParkDecision+":"+d.ID {
		t.Fatal("not parked on the decision:", got)
	}
	if _, err = h.s.RecordDecision(ctx, co, d.ID, decisions.Record{Version: d.Version, Status: "approved", Rationale: "go", Actor: "owner", RequestID: "approve-scope"}); err != nil {
		t.Fatal(err)
	}
	if got := activeParks(t, h, "build"); len(got) != 0 {
		t.Fatal("still parked after the decision:", got)
	}
	if tr := h.lastTrigger("build"); tr.kind != "decision_recorded" || !strings.Contains(tr.body, "approved") {
		t.Fatalf("no outcome trigger: %+v", tr)
	}
	var n int
	_ = h.s.DB.QueryRow("SELECT count(*) FROM task_triggers WHERE task_id='sibling' AND kind='decision_recorded'").Scan(&n)
	if n != 0 {
		t.Fatal("a sibling that was not waiting was woken")
	}
}

// TestRecordedDecisionWakesOwnerWait (person-waits-on-items): a task already parked on the owner's message (an older
// wait) wakes when a decision proposed from its origin is recorded.
func TestRecordedDecisionWakesOwnerWait(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	h.create("conv", "notes", "manager")
	h.create("task", "notes", "demo")
	if _, err := h.s.DB.Exec("UPDATE tasks SET body=jsonb_set(body::jsonb,'{origin_task}','\"conv\"')::json WHERE id='task'"); err != nil {
		t.Fatal(err)
	}
	if err := h.s.ParkOnMessage(ctx, co, "task", "owner", "", "older wait"); err != nil {
		t.Fatal(err)
	}
	d, err := h.s.ProposeDecision(ctx, co, "notes", decisions.Propose{Topic: "scope", Value: "v", Rationale: "r", TaskID: "conv", Actor: "manager", RequestID: "d"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.s.RecordDecision(ctx, co, d.ID, decisions.Record{Version: d.Version, Status: "rejected", Rationale: "no", Actor: "owner", RequestID: "rej"}); err != nil {
		t.Fatal(err)
	}
	if got := activeParks(t, h, "task"); len(got) != 0 {
		t.Fatal("the owner wait did not clear:", got)
	}
	if tr := h.lastTrigger("task"); tr.kind != "decision_recorded" || !strings.Contains(tr.body, "rejected") {
		t.Fatalf("%+v", tr)
	}
}

// TestWaitOnOwnerNeedsAnItem (person-waits-on-items): with nothing for the owner to answer the wait is refused; with
// only a question to the owner about the task, the message park stays (the answer is the owner's message); a wait on
// an agent is not affected.
func TestWaitOnOwnerNeedsAnItem(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("lone", "notes", "demo")
	r := h.start("lone")
	if code, ref, body := waitOwner(h, r); code != 409 || ref.Reason != "wait_unanswerable" || !strings.Contains(ref.Hint, "factory_exit_ask") {
		t.Fatal("wait with nothing to answer", code, body)
	}
	if _, err := h.s.SendMessage(ctx, storage.DefaultCompany, messaging.Send{ProjectID: "notes", TaskID: "lone", Sender: "demo", Recipient: "owner", MessageType: "question", Body: "Which account?", RequestID: "q-lone"}); err != nil {
		t.Fatal(err)
	}
	if code, _, body := waitOwner(h, r); code != 200 {
		t.Fatal("wait with an open question", code, body)
	}
	h.end(r, "succeeded", "accepted")
	if got := activeParks(t, h, "lone"); len(got) != 1 || got[0] != tasks.ParkMessage+":owner" {
		t.Fatal(got)
	}
}

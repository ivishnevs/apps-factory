package core

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appfactory/internal/messaging"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// TestDependencyWaitEscalations replays the M5b-P3 case (dependency-wait-escalations 4.1): a READY task waits on a
// planned BACKLOG task. Nothing escalates before the company's threshold; then one stalled escalation goes to the
// assignee's manager, keep_waiting keeps the dependency and snoozes it, movement of the dependency clears it, and
// an agent cannot hold the task with a manual block.
func TestDependencyWaitEscalations(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "m5", ProjectID: "notes", Title: "M5 screens", Assignee: "demo", RequestID: "m5"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "p3", ProjectID: "notes", Title: "M5b-P3", Assignee: "demo", Ready: true, Dependencies: []string{"m5"}, RequestID: "p3"}); err != nil {
		t.Fatal(err)
	}
	open := func(after time.Duration) []storage.Escalation {
		t.Helper()
		h.fake.Advance(after)
		if err := h.c.sweepStalls(ctx); err != nil {
			t.Fatal(err)
		}
		all, err := h.s.Escalations(ctx, co, true)
		if err != nil {
			t.Fatal(err)
		}
		var out []storage.Escalation
		for _, e := range all {
			if e.Reason == "stalled" && e.TaskID == "p3" {
				out = append(out, e)
			}
		}
		return out
	}
	if got := open(time.Minute); len(got) != 0 {
		t.Fatalf("planned BACKLOG dependency escalated at once: %+v", got)
	}

	// An agent's block of the waiting task is refused with the hint; the owner's is not intercepted.
	block := func(actor string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/tasks/p3/actions", nil)
		r.SetPathValue("id", "p3")
		w := httptest.NewRecorder()
		if !h.c.dependencyWaitBlock(w, r, actor) {
			return nil
		}
		return w
	}
	if w := block("demo"); w == nil || w.Code != 409 || !strings.Contains(w.Body.String(), "keep_waiting") {
		t.Fatalf("agent block of a dependency wait not refused: %+v", w)
	}
	if w := block(""); w != nil {
		t.Fatal("owner block refused")
	}

	got := open(storage.DefaultStallBacklogAfter)
	if len(got) != 1 || got[0].RefID != "m5" || got[0].Target != "manager" || !strings.Contains(got[0].Summary, `"M5 screens"`) || !strings.Contains(got[0].Summary, "24h") {
		t.Fatalf("%+v", got)
	}
	if strings.Join(got[0].Answers, ",") != "keep_waiting,drop_dependency,cancel" {
		t.Fatal(got[0].Answers)
	}
	q, _ := h.s.Message(ctx, co, got[0].MessageID)
	if _, err := h.s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "manager", Body: "planned", Answers: map[string]any{"answer": "keep_waiting"}, RequestID: "keep"}); err != nil {
		t.Fatal(err)
	}
	if p3 := h.task("p3"); len(p3.Dependencies) != 1 || p3.Dependencies[0] != "m5" || p3.Status != "READY" {
		t.Fatalf("keep_waiting changed the plan: %+v", p3)
	}
	// Snoozed: the resync does not reopen it until another threshold passes.
	if got := open(time.Minute); len(got) != 0 {
		t.Fatalf("keep_waiting reopened at once: %+v", got)
	}
	if got := open(storage.DefaultStallBacklogAfter - 2*time.Minute); len(got) != 0 {
		t.Fatalf("keep_waiting reopened before the threshold: %+v", got)
	}
	got = open(2 * time.Minute)
	if len(got) != 1 {
		t.Fatalf("no escalation after another threshold: %+v", got)
	}
	// The dependency moves (reprioritised): not stale any more, the escalation clears by itself.
	m5 := h.task("m5")
	prio := 70
	if _, err := h.s.UpdateTask(ctx, co, "m5", tasks.Update{Version: m5.Version, Priority: &prio}); err != nil {
		t.Fatal(err)
	}
	if left := open(time.Minute); len(left) != 0 {
		t.Fatalf("moved dependency left the escalation open: %+v", left)
	}
	// The legacy answer unblock is drop_dependency.
	got = open(storage.DefaultStallBacklogAfter)
	q, _ = h.s.Message(ctx, co, got[0].MessageID)
	if _, err := h.s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: "go", Answers: map[string]any{"answer": "unblock"}, RequestID: "drop"}); err != nil {
		t.Fatal(err)
	}
	if p3 := h.task("p3"); len(p3.Dependencies) != 0 {
		t.Fatal(p3.Dependencies)
	}
	all, _ := h.s.Escalations(ctx, co, false)
	if e := all[len(all)-1]; e.Answer != "drop_dependency" {
		t.Fatalf("%+v", e)
	}
}

// TestSweepIsolation (dependency-wait-escalations 0.2): one task that cannot be synced is skipped and reported, the
// other escalations open, and a failing background step does not stop Core while storage is up.
func TestSweepIsolation(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	h.create("ok", "notes", "demo")
	err := h.s.SyncEscalations(ctx, co, "stalled", []storage.EscalationWant{{TaskID: "missing", RefID: "x", Summary: "waits on x"}, {TaskID: "ok", RefID: "x", Summary: "waits on x"}})
	var failed storage.SweepErrors
	if !errors.As(err, &failed) || len(failed) != 1 || failed[0].TaskID != "missing" {
		t.Fatal(err)
	}
	if open, _ := h.s.Escalations(ctx, co, true); len(open) != 1 || open[0].TaskID != "ok" {
		t.Fatalf("%+v", open)
	}
	if err := h.c.syncSweepFailures(ctx, co, storage.SweepErrors{{TaskID: "ok", Err: errors.New("boom")}}); err != nil {
		t.Fatal(err)
	}
	page, _ := h.s.Feed(ctx, co, storage.FeedFilter{Task: "ok"}, 0, 100)
	alerts := 0
	for _, it := range page.Items {
		if it.Type == "task.alert.sweep_failed" {
			alerts++
		}
	}
	if alerts != 1 {
		t.Fatalf("%+v", page.Items)
	}
	if err := h.c.isolate(ctx, co, "test", errors.New("one company broke")); err != nil {
		t.Fatal("a failing step stopped Core", err)
	}
}

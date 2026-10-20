package storage

import (
	"context"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// TestWorkerReportsLost: a worker's end without terminal evidence (outcome lost) is recorded lost: no
// attempt, backoff and budget as for any loss; a requested cancel still cancels the task, and a run Core
// stopped for inactivity ended idle_timeout.
func TestWorkerReportsLost(t *testing.T) {
	s, fake := retryStore(t)
	ctx := context.Background()
	co := DefaultCompany
	run := func(id string) (protocol.RunSpec, string) {
		t.Helper()
		spec := dispatchEngineTask(t, s, readyEngineTask(t, s, createEngineTask(t, s, id)), id+"-run")
		lease, err := s.Claim(ctx, co, spec.ID)
		if err != nil {
			t.Fatal(err)
		}
		return spec, lease
	}
	spec, lease := run("t1")
	if err := end(t, s, spec.ID, lease, protocol.Event{Status: "ENDED", Outcome: "lost", Result: "app-server disconnected: EOF"}); err != nil {
		t.Fatal(err)
	}
	if st, out := outcomeOf(t, s, spec.ID); st != "ENDED" || out != "lost" {
		t.Fatal(st, out)
	}
	got := taskOf(t, s, "t1")
	if got.Status != "READY" || got.AttemptsNoProgress != 0 || got.FailStreak != 1 || got.NotBefore != fake.Now().Add(time.Minute).Unix() || budgetUsed(t, s, "t1") != 1 {
		t.Fatalf("%+v budget %d", got, budgetUsed(t, s, "t1"))
	}

	spec, lease = run("t2")
	t2 := taskOf(t, s, "t2")
	if _, err := s.TaskAction(ctx, co, "t2", tasks.Action{Version: t2.Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	if err := end(t, s, spec.ID, lease, protocol.Event{Status: "ENDED", Outcome: "lost", Result: "interrupt result unknown"}); err != nil {
		t.Fatal(err)
	}
	if got = taskOf(t, s, "t2"); got.Status != "CANCELLED" {
		t.Fatalf("cancel lost: %+v", got)
	}

	spec, lease = run("t3")
	if _, err := s.DB.Exec("UPDATE runs SET stop_reason='idle_timeout' WHERE id=?", spec.ID); err != nil {
		t.Fatal(err)
	}
	if err := end(t, s, spec.ID, lease, protocol.Event{Status: "ENDED", Outcome: "lost", Result: "interrupt result unknown"}); err != nil {
		t.Fatal(err)
	}
	if _, out := outcomeOf(t, s, spec.ID); out != "idle_timeout" {
		t.Fatal(out)
	}
	if got = taskOf(t, s, "t3"); got.AttemptsNoProgress != 1 || got.FailStreak != 1 {
		t.Fatalf("%+v", got)
	}
}

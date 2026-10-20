package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// TestRunPhaseMigration (migration 0030): ended runs become ENDED with their outcome (derived where it was
// empty), a claimed v2 run is stored CLAIMED, a task held BLOCKED on its released lost run is released to
// READY after the backoff, a side-effect one stays for the owner, and retry_eligible is gone.
func TestRunPhaseMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	co := DefaultCompany
	start := func(id string, sideEffects bool) {
		t.Helper()
		if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p-" + id, Name: id, Workspace: "/tmp/phase-" + id}); err != nil {
			t.Fatal(err)
		}
		v, err := s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "p-" + id, Title: id, Assignee: "a-" + id, Ready: true, SideEffects: sideEffects, RequestID: "c-" + id})
		if err != nil {
			t.Fatal(err)
		}
		spec := dispatchEngineTask(t, s, v, id+"-run")
		if _, err = s.Claim(ctx, co, spec.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"done", "timeout", "stopped", "claimed", "running", "held"} {
		start(id, false)
	}
	start("effects", true)
	rollbackTo(t, s.DB, 29)
	for _, q := range []string{
		// Outcomes left empty (the run_outcome trigger fires on status changes only).
		"UPDATE runs SET status='COMPLETED' WHERE id='done-run'",
		"UPDATE runs SET status='FAILED',result='timed_out: still running' WHERE id='timeout-run'",
		"UPDATE runs SET status='CANCELLED' WHERE id='stopped-run'",
		"UPDATE runs SET outcome='' WHERE id IN ('done-run','timeout-run','stopped-run')",
		"UPDATE runs SET protocol=2,run_started=0 WHERE id='claimed-run'",
		"UPDATE runs SET status='FAILED',outcome='lost',result='lost: worker lease expired' WHERE id IN ('held-run','effects-run')",
		"UPDATE tasks SET status='BLOCKED',body=json_set(body,'$.status','BLOCKED','$.block_reason','run: outcome unknown; inspect workspace and reconcile run','$.retry_eligible',json('false')) WHERE id IN ('held','effects')",
	} {
		if _, err = s.DB.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	held := taskOf(t, s, "held")
	s.DB.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	for id, want := range map[string][2]string{
		"done-run": {"ENDED", "succeeded"}, "timeout-run": {"ENDED", "timed_out"}, "stopped-run": {"ENDED", "revoked"},
		"claimed-run": {"CLAIMED", ""}, "running-run": {"RUNNING", ""}, "held-run": {"ENDED", "lost"},
	} {
		if st, out := outcomeOf(t, s, id); st != want[0] || out != want[1] {
			t.Errorf("%s: %s/%s, want %v", id, st, out, want)
		}
	}
	got := taskOf(t, s, "held")
	if got.Status != "READY" || got.BlockReason != "" || got.FailStreak != 1 || got.AttemptsNoProgress != 0 || got.NotBefore <= time.Now().Unix() ||
		got.Version != held.Version+1 || !strings.HasPrefix(got.Result, "lost:") || reason(t, s, "held") != tasks.ReasonBackoff {
		t.Fatalf("held task not released: %+v (%s)", got, reason(t, s, "held"))
	}
	var recovered bool
	if err = s.DB.QueryRow("SELECT recovered FROM runs WHERE id='held-run'").Scan(&recovered); err != nil || !recovered {
		t.Fatal("released run not recovered", err)
	}
	if got = taskOf(t, s, "effects"); got.Status != "BLOCKED" || !strings.HasPrefix(got.BlockReason, "run:") {
		t.Fatalf("side-effect task released: %+v", got)
	}
	var left int
	if err = s.DB.QueryRow("SELECT count(*) FROM tasks WHERE json_extract(body,'$.retry_eligible') IS NOT NULL").Scan(&left); err != nil || left != 0 {
		t.Fatal("retry_eligible kept", left, err)
	}
	// Only phases are stored, and an ENDED run names its outcome.
	for _, q := range []string{"UPDATE runs SET status='COMPLETED' WHERE id='running-run'", "UPDATE runs SET status='ENDED' WHERE id='running-run'"} {
		if _, err = s.DB.Exec(q); err == nil || !strings.Contains(err.Error(), "run status must be a phase") {
			t.Fatal(q, err)
		}
	}
	// The claimed run still holds its agent and project: it is live.
	if r, _ := s.Get(ctx, co, "claimed-run"); r.Status != "CLAIMED" {
		t.Fatal(r.Status)
	}
}

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

package storage

import (
	"context"
	"path/filepath"
	"testing"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// TestRetireUnknownAndFailed (migration 0029, then 0030): UNKNOWN runs end with outcome lost, keeping their
// slots only while their runtime may still run; FAILED tasks become READY with a ready trigger and retry,
// while an escalated one stays parked.
func TestRetireUnknownAndFailed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	co := DefaultCompany
	start := func(id string) protocol.RunSpec {
		t.Helper()
		if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p-" + id, Name: id, Workspace: "/tmp/retire-" + id}); err != nil {
			t.Fatal(err)
		}
		v, err := s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "p-" + id, Title: id, Assignee: "a-" + id, Ready: true, RequestID: "c-" + id})
		if err != nil {
			t.Fatal(err)
		}
		spec := dispatchEngineTask(t, s, v, id+"-run")
		if _, err = s.Claim(ctx, co, spec.ID); err != nil {
			t.Fatal(err)
		}
		return spec
	}
	start("held") // lost while its v2 node is silent: the runtime may still run
	start("gone") // lost, runtime known gone (already retried)
	start("failed")
	start("escalated")
	rollbackTo(t, s.DB, 28)
	for _, q := range []string{
		"UPDATE runs SET status='UNKNOWN',outcome='lost',released=0,result='Worker lease expired' WHERE id='held-run'",
		"UPDATE runs SET status='UNKNOWN',outcome='lost',released=1,result='Worker lease expired' WHERE id='gone-run'",
		"UPDATE runs SET status='FAILED',outcome='failed',finished_at=1 WHERE id IN ('failed-run','escalated-run')",
		"UPDATE tasks SET status='BLOCKED',body=json_set(body,'$.status','BLOCKED','$.block_reason','run: outcome unknown; inspect workspace and reconcile run') WHERE id='held'",
		"UPDATE tasks SET status='READY',body=json_set(body,'$.status','READY'),handled_seq=wake_seq WHERE id='gone'",
		"UPDATE tasks SET status='FAILED',body=json_set(body,'$.status','FAILED','$.retry_eligible',json('true')) WHERE id IN ('failed','escalated')",
	} {
		if _, err = s.DB.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	if err = s.SyncEscalations(ctx, co, "retries_exhausted", []EscalationWant{{TaskID: "escalated", Summary: "3 attempts in a row without progress"}}); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	for id, want := range map[string]bool{"held-run": true, "gone-run": false} {
		var status, outcome string
		var unreleased bool
		var finished int64
		if err = s.DB.QueryRow("SELECT status,outcome,unreleased,finished_at FROM runs WHERE id=?", id).Scan(&status, &outcome, &unreleased, &finished); err != nil {
			t.Fatal(err)
		}
		if status != "ENDED" || outcome != "lost" || unreleased != want || finished == 0 {
			t.Fatal(id, status, outcome, unreleased, finished)
		}
	}
	for id, want := range map[string]string{"held": tasks.ReasonLiveRun, "gone": tasks.ReasonIdle, "failed": tasks.ReasonRunnable, "escalated": tasks.ReasonParked + tasks.ParkEscalation} {
		got := taskOf(t, s, id)
		if r := reason(t, s, id); r != want || got.Status == "FAILED" {
			t.Errorf("%s: %s %+v, want %s", id, r, got, want)
		}
	}
	var triggers int
	if err = s.DB.QueryRow("SELECT count(*) FROM task_triggers WHERE kind='ready' AND actor='migration' AND task_id IN ('failed','escalated') AND addressee='a-'||task_id").Scan(&triggers); err != nil || triggers != 2 {
		t.Fatal(triggers, err)
	}
	// Reads present the escalated task as held by its escalation (stored READY, derived BLOCKED).
	if got, err := s.Task(ctx, co, "escalated"); err != nil || got.Status != "BLOCKED" || got.BlockReason != "escalation: retries_exhausted: 3 attempts in a row without progress" {
		t.Fatalf("%+v %v", got, err)
	}
	// The held run keeps its workspace until it is resolved; then its task retries.
	other, err := s.CreateTask(ctx, co, tasks.Create{ID: "other", ProjectID: "p-held", Title: "other", Assignee: "other", Ready: true, RequestID: "c-other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Dispatch(ctx, co, other.ID, other.Version, protocol.RunSpec{ID: "other-run", Project: "p-held", Agent: "other", Provider: protocol.RuntimeCodex}); err == nil {
		t.Fatal("the held run lost its workspace")
	}
	if err = s.Resolve(ctx, co, "held-run", "retry", "the node's machine is gone"); err != nil {
		t.Fatal(err)
	}
	if got := taskOf(t, s, "held"); got.Status != "READY" || got.FailStreak != 1 {
		t.Fatalf("%+v", got)
	}
	if _, err = s.Dispatch(ctx, co, other.ID, other.Version, protocol.RunSpec{ID: "other-run", Project: "p-held", Agent: "other", Provider: protocol.RuntimeCodex}); err != nil {
		t.Fatal(err)
	}
}

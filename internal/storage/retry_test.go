package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

func retryStore(t *testing.T) (*Store, *clock.Fake) {
	t.Helper()
	s := engineStore(t)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	return s, fake
}

func taskOf(t *testing.T, s *Store, id string) tasks.Task {
	t.Helper()
	v, err := s.Task(context.Background(), DefaultCompany, id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestLostV1RunRetriesInItsSession: a v1 lost lease releases the run (the worker stops on a lost lease), so
// the task retries after the backoff in the same session while the lost run no longer holds the project.
func TestLostV1RunRetriesInItsSession(t *testing.T) {
	s, fake := retryStore(t)
	ctx := context.Background()
	co := DefaultCompany
	task := readyEngineTask(t, s, createEngineTask(t, s, "t1"))
	spec := dispatchEngineTask(t, s, task, "r1")
	lease, err := s.Claim(ctx, co, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, spec.ID, lease, protocol.Event{SessionID: "thread-1"}); err != nil {
		t.Fatal(err)
	}
	fake.Advance(LeaseTTL + time.Second)
	if err = s.Expire(ctx, co); err != nil {
		t.Fatal(err)
	}
	got := taskOf(t, s, "t1")
	if got.Status != "READY" || got.FailStreak != 1 || got.AttemptsNoProgress != 0 || got.NotBefore != fake.Now().Add(time.Minute).Unix() {
		t.Fatalf("%+v", got)
	}
	if v, _ := s.TaskSnapshot(ctx, co, "t1"); tasks.Evaluate(withAgent(v)).Reason != tasks.ReasonBackoff {
		t.Fatalf("%+v", tasks.Evaluate(withAgent(v)))
	}
	// Another task of the project is not held by the lost run.
	other := readyEngineTask(t, s, createEngineTask(t, s, "t2"))
	other.Assignee = "demo"
	if _, err = s.Dispatch(ctx, co, other.ID, other.Version, protocol.RunSpec{ID: "o1", Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex}); err != nil {
		t.Fatal("lost run still holds the project:", err)
	}
	if err = s.Cancel(ctx, co, "o1"); err != nil {
		t.Fatal(err)
	}
	got = taskOf(t, s, "t1")
	retry, err := s.Dispatch(ctx, co, got.ID, got.Version, protocol.RunSpec{ID: "r2", Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex})
	if err != nil || retry.SessionID != "thread-1" || retry.Attempt != 2 {
		t.Fatalf("%+v %v", retry, err)
	}
	// A task waiting on a released lost run can be cancelled (the old run is gone).
	if err = s.Cancel(ctx, co, "r2"); err != nil {
		t.Fatal(err)
	}
	other = readyEngineTask(t, s, createEngineTask(t, s, "t3"))
	spec3 := dispatchEngineTask(t, s, other, "r3")
	if _, err = s.Claim(ctx, co, spec3.ID); err != nil {
		t.Fatal(err)
	}
	fake.Advance(LeaseTTL + time.Second)
	if err = s.Expire(ctx, co); err != nil {
		t.Fatal(err)
	}
	waiting := taskOf(t, s, "t3")
	if _, err = s.TaskAction(ctx, co, "t3", tasks.Action{Version: waiting.Version, Action: "cancel"}); err != nil {
		t.Fatal("cancel refused while waiting to retry:", err)
	}
}

// TestRetryBudgetAndPausedOut: paused_out (quota) is not an attempt and requeues the task; the daily budget
// escalates; a side-effect task is never retried.
func TestRetryBudgetAndPausedOut(t *testing.T) {
	s, _ := retryStore(t)
	ctx := context.Background()
	co := DefaultCompany
	run := func(id, runID string, ev protocol.Event) tasks.Task {
		t.Helper()
		task := taskOf(t, s, id)
		spec := dispatchEngineTask(t, s, task, runID)
		lease, err := s.Claim(ctx, co, spec.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = end(t, s, spec.ID, lease, ev); err != nil {
			t.Fatal(err)
		}
		return taskOf(t, s, id)
	}
	readyEngineTask(t, s, createEngineTask(t, s, "quota"))
	if got := run("quota", "q1", protocol.Event{Status: "FAILED", Outcome: "paused_out"}); got.Status != "READY" || got.FailStreak != 0 || got.AttemptsNoProgress != 0 || got.NotBefore != 0 {
		t.Fatalf("%+v", got)
	}
	readyEngineTask(t, s, createEngineTask(t, s, "busy"))
	if _, err := s.DB.Exec("INSERT INTO task_budget(company_id,task_id,day,used) VALUES(?,?,?,?)", co, "busy", s.Now().UTC().Format("2006-01-02"), DailyBudget-1); err != nil {
		t.Fatal(err)
	}
	got := run("busy", "b1", protocol.Event{Status: "FAILED", Outcome: "failed"})
	open, _ := s.Escalations(ctx, co, true)
	if got.Status != "READY" || len(open) != 1 || open[0].Reason != "budget_exhausted" || open[0].Target != "owner" || reason(t, s, "busy") != tasks.ReasonParked+tasks.ParkEscalation {
		t.Fatalf("%+v %+v", got, open)
	}
	// Releasing it again is an authority retry: budget and counters reset, escalation cleared.
	got = readyEngineTask(t, s, got)
	if open, _ = s.Escalations(ctx, co, true); len(open) != 0 || got.FailStreak != 0 {
		t.Fatalf("%+v %+v", got, open)
	}
	var used int
	_ = s.DB.QueryRow("SELECT coalesce(sum(used),0) FROM task_budget WHERE task_id='busy'").Scan(&used)
	if used != 0 {
		t.Fatal("budget not reset", used)
	}
	side, err := s.CreateTask(ctx, co, tasks.Create{ID: "publish", ProjectID: "p", Title: "publish", Assignee: "demo", SideEffects: true, Ready: true, RequestID: "create-publish"})
	if err != nil || !side.SideEffects {
		t.Fatal(side, err)
	}
	if got = run("publish", "p1", protocol.Event{Status: "FAILED", Outcome: "failed"}); got.FailStreak != 0 || reason(t, s, "publish") != tasks.ReasonParked+tasks.ParkEscalation {
		t.Fatalf("side-effect task retried: %+v", got)
	}
}

// TestLostV2RunWaitsForTheNode: a run lost because its node went silent keeps the task parked (its runtime
// may still run) until the node proves it gone: a new epoch, or a heartbeat that no longer lists it. A node
// stopping a run Core declared lost confirms it; that is not a cancel.
func TestLostV2RunWaitsForTheNode(t *testing.T) {
	s, fake := retryStore(t)
	ctx := context.Background()
	co := DefaultCompany
	register(t, s, "n1", 1)
	start := func(id, runID string, epoch int64, claim string) string {
		t.Helper()
		task := readyEngineTask(t, s, createEngineTask(t, s, id))
		spec := dispatchEngineTask(t, s, task, runID)
		c, err := claimV2(s, spec.ID, "n1", epoch, claim)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.StartRun(ctx, co, spec.ID, c.Lease); err != nil {
			t.Fatal(err)
		}
		return c.Lease
	}
	start("t1", "r1", 1, "1-0-1")
	fake.Advance(NodeOffline + time.Second)
	if err := s.Expire(ctx, co); err != nil {
		t.Fatal(err)
	}
	if got := taskOf(t, s, "t1"); got.Status != "IN_PROGRESS" || reason(t, s, "t1") != tasks.ReasonLiveRun {
		t.Fatalf("retried while the node may still run it: %+v", got)
	}
	register(t, s, "n1", 2) // the node restarted: what it ran is gone
	if got := taskOf(t, s, "t1"); got.Status != "READY" || got.NotBefore == 0 {
		t.Fatalf("%+v", got)
	}

	// Lost while silent, then the node comes back, is told the run is terminal and stops it.
	lease := start("t2", "r2", 2, "2-0-1")
	fake.Advance(NodeOffline + time.Second)
	if err := s.Expire(ctx, co); err != nil {
		t.Fatal(err)
	}
	a, err := s.NodeHeartbeat(ctx, co, "n1", NodeHeartbeat{Epoch: 2, Inventory: []InventoryRun{{RunID: "r2", State: "running"}}})
	if err != nil || len(a.Stop) != 1 || a.Stop[0].Reason != "terminal" || taskOf(t, s, "t2").Status != "IN_PROGRESS" {
		t.Fatal(a, err, taskOf(t, s, "t2"))
	}
	if err = end(t, s, "r2", lease, protocol.Event{Status: "CANCELLED"}); err != nil {
		t.Fatal(err)
	}
	if got := taskOf(t, s, "t2"); got.Status != "READY" {
		t.Fatalf("the node's stop of a lost run cancelled the task: %+v", got)
	}

	// Lost while silent, then a heartbeat without it: gone.
	start("t3", "r3", 2, "2-0-2")
	fake.Advance(NodeOffline + time.Second)
	if err = s.Expire(ctx, co); err != nil {
		t.Fatal(err)
	}
	if _, err = s.NodeHeartbeat(ctx, co, "n1", NodeHeartbeat{Epoch: 2}); err != nil {
		t.Fatal(err)
	}
	if got := taskOf(t, s, "t3"); got.Status != "READY" {
		t.Fatalf("%+v", got)
	}
}

// TestReassignmentResetsAttempts: a new assignee starts its own attempt count and clears the escalation.
func TestReassignmentResetsAttempts(t *testing.T) {
	s, fake := retryStore(t)
	ctx := context.Background()
	co := DefaultCompany
	readyEngineTask(t, s, createEngineTask(t, s, "t1"))
	for i := range RetryAttempts {
		task := taskOf(t, s, "t1")
		spec := dispatchEngineTask(t, s, task, "r"+string(rune('1'+i)))
		lease, err := s.Claim(ctx, co, spec.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = end(t, s, spec.ID, lease, protocol.Event{Status: "FAILED", Outcome: "timed_out"}); err != nil {
			t.Fatal(err)
		}
		fake.Advance(time.Hour)
	}
	got := taskOf(t, s, "t1")
	if open, _ := s.Escalations(ctx, co, true); got.Status != "READY" || len(open) != 1 || open[0].Reason != "retries_exhausted" {
		t.Fatalf("%+v %+v", got, open)
	}
	qa := "qa-agent"
	if got, err := s.UpdateTask(ctx, co, "t1", tasks.Update{Version: got.Version, Assignee: &qa}); err != nil || got.AttemptsNoProgress != 0 || got.FailStreak != 0 {
		t.Fatal(got, err)
	}
	if open, _ := s.Escalations(ctx, co, true); len(open) != 0 {
		t.Fatalf("%+v", open)
	}
}

func withAgent(s tasks.Snapshot) tasks.Snapshot { s.AgentConfigured = true; return s }

// reason is the task's Evaluate reason now.
func reason(t *testing.T, s *Store, id string) string {
	t.Helper()
	snap, err := s.TaskSnapshot(context.Background(), DefaultCompany, id)
	if err != nil {
		t.Fatal(err)
	}
	return tasks.Evaluate(withAgent(snap)).Reason
}

// TestInspectEscalation: a task with external effects is inspected instead of retried; continue reconciles a
// lost run as COMPLETED and tells the next brief the effect happened; retry reruns a failed one.
func TestInspectEscalation(t *testing.T) {
	s, fake := retryStore(t)
	ctx := context.Background()
	co := DefaultCompany
	answer := func(task, choice string) {
		t.Helper()
		open, _ := s.Escalations(ctx, co, true)
		for _, e := range open {
			if e.TaskID == task && e.Reason == "inspect" {
				q, _ := s.Message(ctx, co, e.MessageID)
				if _, err := s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: choice, Answers: map[string]any{"answer": choice}, RequestID: task + choice}); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
		t.Fatalf("no inspect escalation for %s: %+v", task, open)
	}
	publish, err := s.CreateTask(ctx, co, tasks.Create{ID: "publish", ProjectID: "p", Title: "publish", Assignee: "demo", SideEffects: true, Ready: true, RequestID: "c-publish"})
	if err != nil {
		t.Fatal(err)
	}
	spec := dispatchEngineTask(t, s, publish, "p1")
	if _, err = s.Claim(ctx, co, spec.ID); err != nil {
		t.Fatal(err)
	}
	fake.Advance(LeaseTTL + time.Second)
	if err = s.Expire(ctx, co); err != nil {
		t.Fatal(err)
	}
	answer("publish", "continue")
	got := taskOf(t, s, "publish")
	run, _ := s.Get(ctx, co, "p1")
	if got.Status != "READY" || !strings.Contains(got.ReviewFeedback, "effect happened") || run.Status != "COMPLETED" {
		t.Fatalf("%+v %+v", got, run)
	}

	deploy, _ := s.CreateTask(ctx, co, tasks.Create{ID: "deploy", ProjectID: "p", Title: "deploy", Assignee: "other", SideEffects: true, Ready: true, RequestID: "c-deploy"})
	spec = dispatchEngineTask(t, s, deploy, "d1")
	lease, _ := s.Claim(ctx, co, spec.ID)
	if err = end(t, s, spec.ID, lease, protocol.Event{Status: "FAILED", Outcome: "failed"}); err != nil {
		t.Fatal(err)
	}
	if r := reason(t, s, "deploy"); r != tasks.ReasonParked+tasks.ParkEscalation {
		t.Fatal("side-effect task retried", r)
	}
	answer("deploy", "retry")
	if got = taskOf(t, s, "deploy"); got.Status != "READY" {
		t.Fatal(got.Status)
	}
}

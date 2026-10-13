package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

func verdict(t *testing.T, s *Store, id string) tasks.Verdict {
	t.Helper()
	snap, err := s.TaskSnapshot(context.Background(), DefaultCompany, id)
	if err != nil {
		t.Fatal(err)
	}
	snap.AgentConfigured = true
	return tasks.Evaluate(snap)
}

func pendingKinds(v tasks.Verdict) []string {
	out := []string{}
	for _, g := range v.Pending {
		out = append(out, g.Kind)
	}
	return out
}

func expectVerdict(t *testing.T, s *Store, id, reason string, pending ...string) {
	t.Helper()
	v := verdict(t, s, id)
	got := pendingKinds(v)
	if v.Reason != reason || len(got) != len(pending) {
		t.Fatalf("%s: %+v (pending %v), want %s pending %v", id, v, got, reason, pending)
	}
	for i := range got {
		if got[i] != pending[i] {
			t.Fatalf("%s: pending %v, want %v", id, got, pending)
		}
	}
}

func TestTriggersAndParksFollowTheLifecycle(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	a := createEngineTask(t, s, "a")
	b := createEngineTask(t, s, "b", "a")
	expectVerdict(t, s, "a", tasks.ReasonBacklog)
	a = readyEngineTask(t, s, a)
	expectVerdict(t, s, "a", tasks.ReasonRunnable, "ready")
	b = readyEngineTask(t, s, b)
	expectVerdict(t, s, "b", tasks.ReasonDependencies, "ready")

	// A completed run consumes the triggers it was briefed on at claim.
	run := dispatchEngineTask(t, s, a, "a1")
	finishEngineRun(t, s, run, "COMPLETED")
	expectVerdict(t, s, "a", tasks.ReasonIdle)
	a, _ = s.Task(ctx, co, "a")
	if a, _ = s.ReviewTask(ctx, co, "a", tasks.Review{Version: a.Version, Feedback: "add tests"}); a.Status != "REWORK" {
		t.Fatal(a)
	}
	expectVerdict(t, s, "a", tasks.ReasonIdle, "review_result")
	a = readyEngineTask(t, s, a)
	expectVerdict(t, s, "a", tasks.ReasonRunnable, "review_result", "ready")

	// A blocking question parks the task; the answer clears the park and wakes it.
	run = dispatchEngineTask(t, s, a, "a2")
	lease, err := s.Claim(ctx, co, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.SendMessage(ctx, co, messaging.Send{ProjectID: "p", TaskID: "a", RunID: run.ID, Sender: "demo", Recipient: "owner", MessageType: "question", Body: "Which store?", Blocking: true, RequestID: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Apply(ctx, co, protocol.Wrap("agent.completed", run.ID, "", protocol.Event{RunID: run.ID, Lease: lease, Status: "COMPLETED", Result: "asked"})); err != nil {
		t.Fatal(err)
	}
	expectVerdict(t, s, "a", tasks.ReasonParked+tasks.ParkAsk)
	if _, err = s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: "SQLite", RequestID: "answer"}); err != nil {
		t.Fatal(err)
	}
	expectVerdict(t, s, "a", tasks.ReasonRunnable, "answer")

	// A manual block parks the task until it is readied again.
	a, _ = s.Task(ctx, co, "a")
	if a, err = s.TaskAction(ctx, co, "a", tasks.Action{Version: a.Version, Action: "block", Reason: "waiting for keys"}); err != nil {
		t.Fatal(err)
	}
	expectVerdict(t, s, "a", tasks.ReasonParked+tasks.ParkBlock, "answer")
	a = readyEngineTask(t, s, a)
	expectVerdict(t, s, "a", tasks.ReasonRunnable, "answer", "ready")

	// The last open dependency reaching DONE wakes its dependent once.
	run = dispatchEngineTask(t, s, a, "a3")
	finishEngineRun(t, s, run, "COMPLETED")
	a, _ = s.Task(ctx, co, "a")
	if _, err = s.ReviewTask(ctx, co, "a", tasks.Review{Version: a.Version, Accept: true}); err != nil {
		t.Fatal(err)
	}
	expectVerdict(t, s, "b", tasks.ReasonRunnable, "ready", "dependency_done")

	// A lost lease parks the task for inspection; resolving the run clears it.
	b, _ = s.Task(ctx, co, "b")
	run = dispatchEngineTask(t, s, b, "b1")
	if _, err = s.Claim(ctx, co, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE runs SET lease_until=? WHERE id=?", time.Now().Add(-time.Hour).Unix(), run.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Expire(ctx, co); err != nil {
		t.Fatal(err)
	}
	expectVerdict(t, s, "b", tasks.ReasonParked+tasks.ParkInspect, "ready", "dependency_done")
	if err = s.Resolve(ctx, co, run.ID, "FAILED", "inspected; nothing was changed"); err != nil {
		t.Fatal(err)
	}
	if v := verdict(t, s, "b"); v.Reason == tasks.ReasonParked+tasks.ParkInspect {
		t.Fatal("resolved run still parks the task", v)
	}

	// Cancelling overrides every remaining park.
	b, _ = s.Task(ctx, co, "b")
	if b, err = s.TaskAction(ctx, co, "b", tasks.Action{Version: b.Version, Action: "block", Reason: "pause"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TaskAction(ctx, co, "b", tasks.Action{Version: b.Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	var active int
	if err = s.DB.QueryRow("SELECT count(*) FROM task_parks WHERE status='active'").Scan(&active); err != nil || active != 0 {
		t.Fatal("parks left active", active, err)
	}

	// Triggers and parks are visible in the feed.
	page, _ := s.ProjectFeed(ctx, co, "p", 0, 100)
	seen := map[string]bool{}
	for _, item := range page.Items {
		seen[item.Type] = true
	}
	for _, kind := range []string{"task.trigger.ready", "task.trigger.answer", "task.trigger.dependency_done", "task.trigger.review_result", "task.parked.ask", "task.unparked.ask", "task.parked.block", "task.parked.inspect", "task.unparked.all"} {
		if !seen[kind] {
			t.Error("feed lacks", kind)
		}
	}
}

func TestTriggerAndParkBackfill(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProject(ctx, DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/trigger-backfill"}); err != nil {
		t.Fatal(err)
	}
	ready := createEngineTask(t, s, "ready")
	readyEngineTask(t, s, ready)
	blocked := createEngineTask(t, s, "blocked")
	if _, err = s.TaskAction(ctx, DefaultCompany, "blocked", tasks.Action{Version: blocked.Version, Action: "block", Reason: "keys"}); err != nil {
		t.Fatal(err)
	}
	rollbackTo(t, s.DB, 9)
	s.DB.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	expectVerdict(t, s, "ready", tasks.ReasonRunnable, "ready")
	expectVerdict(t, s, "blocked", tasks.ReasonParked+tasks.ParkBlock)
}

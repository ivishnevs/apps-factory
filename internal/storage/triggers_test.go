package storage

import (
	"context"
	"errors"
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
	finishEngineRun(t, s, run, "succeeded")
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
	if err = s.Apply(ctx, co, protocol.Wrap("agent.completed", run.ID, "", protocol.Event{RunID: run.ID, Lease: lease, Status: "ENDED", Outcome: "succeeded", Result: "asked"})); err != nil {
		t.Fatal(err)
	}
	expectVerdict(t, s, "a", tasks.ReasonParked+tasks.ParkAsk)
	// The park is the wait: stored READY, presented BLOCKED with the question.
	var stored string
	if err = s.DB.QueryRow("SELECT status FROM tasks WHERE id='a'").Scan(&stored); err != nil || stored != "READY" {
		t.Fatal(stored, err)
	}
	if a, _ = s.Task(ctx, co, "a"); a.Status != "BLOCKED" || a.WaitMessageID != q.ID || a.BlockReason != "message: waiting for answer" {
		t.Fatalf("%+v", a)
	}
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
	finishEngineRun(t, s, run, "succeeded")
	a, _ = s.Task(ctx, co, "a")
	if _, err = s.ReviewTask(ctx, co, "a", tasks.Review{Version: a.Version, Accept: true}); err != nil {
		t.Fatal(err)
	}
	expectVerdict(t, s, "b", tasks.ReasonRunnable, "ready", "dependency_done")

	// A lost lease parks a side-effect task for inspection; resolving the run clears it.
	b, _ = s.Task(ctx, co, "b")
	yes := true
	if b, err = s.UpdateTask(ctx, co, "b", tasks.Update{Version: b.Version, SideEffects: &yes}); err != nil {
		t.Fatal(err)
	}
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
	// The inspect escalation (answers retry, continue, cancel) leads; resolving the run answers it.
	expectVerdict(t, s, "b", tasks.ReasonParked+tasks.ParkEscalation, "ready", "dependency_done")
	if err = s.Resolve(ctx, co, run.ID, "retry", "inspected; nothing was changed"); err != nil {
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
	for _, kind := range []string{"task.trigger.ready", "task.trigger.answer", "task.trigger.dependency_done", "task.trigger.review_result", "task.parked.ask", "task.unparked.ask", "task.parked.block", "task.parked.escalation", "task.unparked.all"} {
		if !seen[kind] {
			t.Error("feed lacks", kind)
		}
	}
}

func TestCreatedReadyCommentsAndLastChild(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "x", ProjectID: "p", Title: "x", Ready: true}); !errors.Is(err, ErrInput) {
		t.Fatal("ready task without assignee", err)
	}
	r, err := s.CreateTask(ctx, co, tasks.Create{ID: "r", ProjectID: "p", Title: "r", Assignee: "demo", Ready: true})
	if err != nil || r.Status != "READY" {
		t.Fatal(r, err)
	}
	expectVerdict(t, s, "r", tasks.ReasonRunnable, "created_ready")

	// Comments: only authority comments wake the task (D8); the assignee's own and other agents' comments are
	// stored for the next brief.
	comment := func(actor, text string, authority bool) {
		t.Helper()
		if _, err := s.CommentTask(ctx, co, "r", tasks.Comment{Text: text, Actor: actor, Authority: authority}); err != nil {
			t.Fatal(err)
		}
	}
	comment("demo", "started looking", false)
	comment("qa", "please add a test", false)
	expectVerdict(t, s, "r", tasks.ReasonRunnable, "created_ready")
	comment("", "use metric units", false)
	comment("lead", "ship metric first", true)
	v := verdict(t, s, "r")
	if got := pendingKinds(v); len(got) != 3 || got[1] != "owner_comment" || !v.Pending[1].Authority || !v.Pending[2].Authority || v.Pending[2].Actor != "lead" {
		t.Fatalf("%+v", v.Pending)
	}
	if cs, _ := s.Comments(ctx, co, "r"); len(cs) != 4 {
		t.Fatal(cs)
	}

	// The last open child closing, by any path, wakes the parent once.
	parent := createEngineTask(t, s, "parent")
	parent = readyEngineTask(t, s, parent)
	child := func(id string) tasks.Task {
		v, err := s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "p", Title: id, Assignee: "demo", ParentTaskID: "parent", Ready: true})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	c1, c2 := child("c1"), child("c2")
	if _, err = s.TaskAction(ctx, co, "c1", tasks.Action{Version: c1.Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	expectVerdict(t, s, "parent", tasks.ReasonRunnable, "ready")
	run := dispatchEngineTask(t, s, c2, "c2-run")
	if err = s.Cancel(ctx, co, run.ID); err != nil {
		t.Fatal(err)
	}
	expectVerdict(t, s, "parent", tasks.ReasonRunnable, "ready", "last_child_closed")
}

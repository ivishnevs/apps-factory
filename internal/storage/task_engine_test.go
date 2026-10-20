package storage

import (
	"appfactory/internal/clock"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func engineStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	_, err = s.CreateProject(context.Background(), DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "Project", Workspace: "/tmp/task-engine-p"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func createEngineTask(t *testing.T, s *Store, id string, deps ...string) tasks.Task {
	t.Helper()
	v, err := s.CreateTask(context.Background(), DefaultCompany, tasks.Create{ID: id, ProjectID: "p", Title: id, Assignee: "demo", Dependencies: deps, RequestID: "create-" + id})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func readyEngineTask(t *testing.T, s *Store, v tasks.Task) tasks.Task {
	t.Helper()
	v, err := s.TaskAction(context.Background(), DefaultCompany, v.ID, tasks.Action{Version: v.Version, Action: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func dispatchEngineTask(t *testing.T, s *Store, v tasks.Task, id string) protocol.RunSpec {
	t.Helper()
	r, err := s.Dispatch(context.Background(), DefaultCompany, v.ID, v.Version, protocol.RunSpec{ID: id, Project: v.ProjectID, Agent: v.Assignee, Provider: protocol.RuntimeCodex})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func finishEngineRun(t *testing.T, s *Store, r protocol.RunSpec, outcome string) protocol.Envelope {
	t.Helper()
	ctx := context.Background()
	lease, err := s.Claim(ctx, DefaultCompany, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	e := protocol.Wrap("agent.completed", r.ID, "", protocol.Event{RunID: r.ID, Lease: lease, Status: "ENDED", Outcome: outcome, SessionID: "session-" + r.TaskID, Result: "checks passed"})
	if err = s.Apply(ctx, DefaultCompany, e); err != nil {
		t.Fatal(err)
	}
	return e
}
func TestDependenciesReviewAndRework(t *testing.T) {
	s := engineStore(t)
	s.Clock = clock.NewFake(time.Now()) // the version count must not depend on crossing a second boundary
	ctx := context.Background()
	a := readyEngineTask(t, s, createEngineTask(t, s, "a"))
	b := readyEngineTask(t, s, createEngineTask(t, s, "b", "a"))
	if _, err := s.Dispatch(ctx, DefaultCompany, b.ID, b.Version, protocol.RunSpec{ID: "too-early", Project: "p", Agent: "demo"}); !errors.Is(err, ErrConflict) {
		t.Fatal("dependency scheduled", err)
	}
	r := dispatchEngineTask(t, s, a, "run-a")
	event := finishEngineRun(t, s, r, "succeeded")
	a, _ = s.Task(ctx, DefaultCompany, "a")
	if a.Status != "IN_REVIEW" || a.Version != 4 {
		t.Fatalf("unexpected projection %+v", a)
	}
	if _, err := s.Dispatch(ctx, DefaultCompany, b.ID, b.Version, protocol.RunSpec{ID: "still-early", Project: "p", Agent: "demo"}); !errors.Is(err, ErrConflict) {
		t.Fatal("review bypass", err)
	}
	submission := tasks.Submit{Version: a.Version, RunID: r.ID, Submission: tasks.Submission{Summary: "done", Checks: []string{"test passed"}, Artifacts: []tasks.Artifact{{ID: "design", Version: "v1", URI: "file:///tmp/design.png"}}}, RequestID: "submit-a"}
	a, err := s.SubmitTask(ctx, DefaultCompany, a.ID, submission)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.SubmitTask(ctx, DefaultCompany, a.ID, submission)
	if err != nil || again.Version != a.Version {
		t.Fatal("submission replay", again, err)
	}
	a, err = s.ReviewTask(ctx, DefaultCompany, a.ID, tasks.Review{Version: a.Version, Accept: false, Feedback: "add a check"})
	if err != nil || a.Status != "REWORK" {
		t.Fatal(a, err)
	}
	a = readyEngineTask(t, s, a)
	r2 := dispatchEngineTask(t, s, a, "run-a2")
	if r2.SessionID != "session-a" || r2.Attempt != 2 {
		t.Fatal("task session/attempt lost", r2)
	}
	finishEngineRun(t, s, r2, "succeeded")
	a, _ = s.Task(ctx, DefaultCompany, "a")
	a, err = s.ReviewTask(ctx, DefaultCompany, a.ID, tasks.Review{Version: a.Version, Accept: true, RequestID: "accept-a"})
	if err != nil || a.Status != "DONE" {
		t.Fatal(a, err)
	}
	if err = s.Apply(ctx, DefaultCompany, event); err != nil {
		t.Fatal(err)
	}
	a2, _ := s.Task(ctx, DefaultCompany, "a")
	if a2.Version != a.Version || a2.Status != "DONE" {
		t.Fatal("late event regressed task")
	}
	rb := dispatchEngineTask(t, s, b, "run-b")
	if rb.SessionID != "" {
		t.Fatal("another task reused thread", rb)
	}
	attempts, err := s.Attempts(ctx, DefaultCompany, "a")
	if err != nil || len(attempts) != 2 {
		t.Fatal(attempts, err)
	}
}
func TestDAGAndOptimisticIdempotency(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	a := createEngineTask(t, s, "a")
	b := createEngineTask(t, s, "b", "a")
	deps := []string{b.ID}
	if _, err := s.UpdateTask(ctx, DefaultCompany, a.ID, tasks.Update{Version: a.Version, Dependencies: &deps}); !errors.Is(err, ErrCycle) {
		t.Fatal("cycle accepted", err)
	}
	title := "updated"
	in := tasks.Update{Version: a.Version, Title: &title, RequestID: "update-a"}
	updated, err := s.UpdateTask(ctx, DefaultCompany, a.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.UpdateTask(ctx, DefaultCompany, a.ID, in)
	if err != nil || replay.Version != updated.Version {
		t.Fatal(replay, err)
	}
	changed := "different"
	in.Title = &changed
	if _, err = s.UpdateTask(ctx, DefaultCompany, a.ID, in); !errors.Is(err, ErrConflict) {
		t.Fatal("idempotency key reuse accepted", err)
	}
	if _, err = s.TaskAction(ctx, DefaultCompany, a.ID, tasks.Action{Version: 1, Action: "ready"}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale version accepted", err)
	}
	_, err = s.CreateProject(ctx, DefaultCompany, tasks.ProjectCreate{ID: "other", Name: "Other", Workspace: "/tmp/task-engine-other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, DefaultCompany, tasks.Create{ProjectID: "other", Title: "bad", Dependencies: []string{a.ID}}); !errors.Is(err, ErrInput) {
		t.Fatal("cross-project dependency", err)
	}
	all, err := s.Tasks(ctx, DefaultCompany, "other", "")
	if err != nil || len(all) != 0 {
		t.Fatal("failed mutation not rolled back", all, err)
	}
}
func TestDispatchAtomicAndSingleWriter(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	a := readyEngineTask(t, s, createEngineTask(t, s, "a"))
	b := readyEngineTask(t, s, createEngineTask(t, s, "b"))
	var wg sync.WaitGroup
	success := make(chan string, 2)
	for _, id := range []string{"run1", "run2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := s.Dispatch(ctx, DefaultCompany, a.ID, a.Version, protocol.RunSpec{ID: id, Project: "p", Agent: "demo"}); err == nil {
				success <- id
			}
		}(id)
	}
	wg.Wait()
	close(success)
	count := 0
	for range success {
		count++
	}
	if count != 1 {
		t.Fatal("duplicate dispatch", count)
	}
	if _, err := s.Dispatch(ctx, DefaultCompany, b.ID, b.Version, protocol.RunSpec{ID: "run-b", Project: "p", Agent: "demo"}); !errors.Is(err, ErrConflict) {
		t.Fatal("writer or agent slot bypass", err)
	}
	var commands, attempts int
	_ = s.DB.QueryRow("SELECT count(*) FROM outbox WHERE subject LIKE 'factory.default.commands.%'").Scan(&commands)
	_ = s.DB.QueryRow("SELECT count(*) FROM task_attempts").Scan(&attempts)
	if commands != 1 || attempts != 1 {
		t.Fatal("non-atomic dispatch", commands, attempts)
	}
}

// A side-effect task is inspected, never retried automatically, after a lost run; resolving the run
// answers the inspection.
func TestTaskLostReconciliationAndCancel(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	created, err := s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: "a", ProjectID: "p", Title: "a", Assignee: "demo", SideEffects: true, RequestID: "create-a"})
	if err != nil {
		t.Fatal(err)
	}
	a := readyEngineTask(t, s, created)
	r := dispatchEngineTask(t, s, a, "run-a")
	if _, err := s.Claim(ctx, DefaultCompany, r.ID); err != nil {
		t.Fatal(err)
	}
	_, _ = s.DB.Exec("UPDATE runs SET lease_until=? WHERE id=?", time.Now().Add(-time.Minute).Unix(), r.ID)
	if err := s.Expire(ctx, DefaultCompany); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Task(ctx, DefaultCompany, a.ID)
	if reason(t, s, a.ID) != tasks.ReasonParked+tasks.ParkEscalation {
		t.Fatal(a)
	}
	if _, err := s.TaskAction(ctx, DefaultCompany, a.ID, tasks.Action{Version: a.Version, Action: "ready"}); !errors.Is(err, ErrConflict) {
		t.Fatal("lost run retried", err)
	}
	if err := s.Resolve(ctx, DefaultCompany, r.ID, "retry", "workspace inspected; nothing happened, run it again"); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Task(ctx, DefaultCompany, a.ID)
	if a.Status != "READY" || reason(t, s, a.ID) != tasks.ReasonRunnable {
		t.Fatal("resolve retry is not a retry", a, reason(t, s, a.ID))
	}
	r = dispatchEngineTask(t, s, a, "run-a2")
	if err := s.Cancel(ctx, DefaultCompany, r.ID); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Task(ctx, DefaultCompany, a.ID)
	if a.Status != "CANCELLED" {
		t.Fatal(a)
	}
	if _, err := s.Claim(ctx, DefaultCompany, r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("cancelled queued task claimed")
	}
}

func TestProjectVersionAndReplay(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	in := tasks.ProjectUpdate{Version: 1, Name: "Updated", RequestID: "update-project"}
	p, err := s.UpdateProject(ctx, DefaultCompany, "p", in)
	if err != nil || p.Version != 2 {
		t.Fatal(p, err)
	}
	again, err := s.UpdateProject(ctx, DefaultCompany, "p", in)
	if err != nil || again.Version != 2 {
		t.Fatal(again, err)
	}
	in.RequestID = "stale"
	if _, err = s.UpdateProject(ctx, DefaultCompany, "p", in); !errors.Is(err, ErrConflict) {
		t.Fatal("stale project version accepted", err)
	}
}
func TestRunningTaskCancellation(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	a := readyEngineTask(t, s, createEngineTask(t, s, "a"))
	r := dispatchEngineTask(t, s, a, "run-a")
	lease, err := s.Claim(ctx, DefaultCompany, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, _ = s.Task(ctx, DefaultCompany, a.ID)
	a, err = s.TaskAction(ctx, DefaultCompany, a.ID, tasks.Action{Version: a.Version, Action: "cancel"})
	if err != nil || !a.CancelRequested || a.Status != "IN_PROGRESS" {
		t.Fatal(a, err)
	}
	cancel, err := s.Heartbeat(ctx, DefaultCompany, r.ID, lease)
	if err != nil || !cancel {
		t.Fatal("cancel not delivered", err)
	}
	if err = s.Apply(ctx, DefaultCompany, protocol.Wrap("agent.completed", r.ID, "", protocol.Event{RunID: r.ID, Lease: lease, Status: "ENDED", Outcome: "revoked"})); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Task(ctx, DefaultCompany, a.ID)
	if a.Status != "CANCELLED" {
		t.Fatal(a)
	}
}

func TestLargeResultsKeepBrokerNotificationsBounded(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	a := readyEngineTask(t, s, createEngineTask(t, s, "large"))
	r := dispatchEngineTask(t, s, a, "large-run")
	lease, err := s.Claim(ctx, DefaultCompany, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("x", 700*1024)
	if err = s.Apply(ctx, DefaultCompany, protocol.Wrap("agent.completed", r.ID, "", protocol.Event{RunID: r.ID, Lease: lease, Status: "ENDED", Outcome: "succeeded", Result: text})); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Task(ctx, DefaultCompany, a.ID)
	if a.Status != "IN_REVIEW" || len(a.Result) != len(text) {
		t.Fatal("large result lost")
	}
	out, err := s.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range out {
		if strings.HasPrefix(o.Subject, "factory.default.events.") && len(o.Body) > 4096 {
			t.Fatal("unbounded entity notification", len(o.Body))
		}
	}
}

// TestConcurrencyCaps: an agent with max_concurrent_runs 2 and 1 per project runs one task per project, two
// in all; a third waits (dispatch conflict) and the verdict says capacity.
func TestConcurrencyCaps(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	for _, p := range []string{"q", "r"} {
		if _, err := s.CreateProject(ctx, DefaultCompany, tasks.ProjectCreate{ID: p, Name: p, Workspace: "/tmp/caps-" + p}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := []byte(`{"spec":{"execution":{"max_concurrent_runs":2,"max_concurrent_runs_per_project":1}}}`)
	dispatch := func(id, project string) error {
		v, err := s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: id, ProjectID: project, Title: id, Assignee: "dev", Ready: true, RequestID: id})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Dispatch(ctx, DefaultCompany, v.ID, v.Version, protocol.RunSpec{ID: "run-" + id, Project: project, Agent: "dev", Provider: protocol.RuntimeCodex, ConfigSnapshot: cfg})
		return err
	}
	if err := dispatch("p1", "p"); err != nil {
		t.Fatal(err)
	}
	if err := dispatch("p2", "p"); !errors.Is(err, ErrConflict) {
		t.Fatal("second run in one project:", err)
	}
	if err := dispatch("q1", "q"); err != nil {
		t.Fatal(err)
	}
	if err := dispatch("r1", "r"); !errors.Is(err, ErrConflict) {
		t.Fatal("third run of the agent:", err)
	}
	snap, err := s.TaskSnapshot(ctx, DefaultCompany, "r1")
	if err != nil {
		t.Fatal(err)
	}
	snap.AgentConfigured = true
	snap.AgentCap, snap.AgentProjectCap = AgentCaps(cfg)
	if v := tasks.Evaluate(snap); v.Reason != tasks.ReasonCapacity || snap.AgentLive != 2 {
		t.Fatalf("%+v live=%d", v, snap.AgentLive)
	}
}

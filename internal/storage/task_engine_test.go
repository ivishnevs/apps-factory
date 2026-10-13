package storage

import (
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func engineStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	_, err = s.CreateProject(context.Background(), tasks.ProjectCreate{ID: "p", Name: "Project", Workspace: "/tmp/task-engine-p"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func createEngineTask(t *testing.T, s *Store, id string, deps ...string) tasks.Task {
	t.Helper()
	v, err := s.CreateTask(context.Background(), tasks.Create{ID: id, ProjectID: "p", Title: id, Assignee: "demo", Dependencies: deps, RequestID: "create-" + id})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func readyEngineTask(t *testing.T, s *Store, v tasks.Task) tasks.Task {
	t.Helper()
	v, err := s.TaskAction(context.Background(), v.ID, tasks.Action{Version: v.Version, Action: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func dispatchEngineTask(t *testing.T, s *Store, v tasks.Task, id string) protocol.RunSpec {
	t.Helper()
	r, err := s.Dispatch(context.Background(), v.ID, v.Version, protocol.RunSpec{ID: id, Project: v.ProjectID, Agent: v.Assignee, Provider: protocol.RuntimeCodex})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func finishEngineRun(t *testing.T, s *Store, r protocol.RunSpec, status string) protocol.Envelope {
	t.Helper()
	ctx := context.Background()
	lease, err := s.Claim(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	e := protocol.Wrap("agent.completed", r.ID, "", protocol.Event{RunID: r.ID, Lease: lease, Status: status, SessionID: "session-" + r.TaskID, Result: "checks passed"})
	if err = s.Apply(ctx, e); err != nil {
		t.Fatal(err)
	}
	return e
}
func TestMigrationPreservesFoundation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	spec := protocol.RunSpec{ID: "legacy", Project: "legacy", Agent: "demo"}
	b, _ := json.Marshal(spec)
	if _, err = db.Exec("INSERT INTO runs(id,project,agent,workspace,spec,status,created_at,result) VALUES('legacy','legacy','demo','/tmp/legacy',?,'COMPLETED',1,'old result')", b); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	r, err := s.Get(context.Background(), "legacy")
	if err != nil || r.Result != "old result" {
		t.Fatal(r, err)
	}
	var version int
	if err = s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 7 {
		t.Fatal(version, err)
	}
}
func TestDependenciesReviewAndRework(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	a := readyEngineTask(t, s, createEngineTask(t, s, "a"))
	b := readyEngineTask(t, s, createEngineTask(t, s, "b", "a"))
	if _, err := s.Dispatch(ctx, b.ID, b.Version, protocol.RunSpec{ID: "too-early", Project: "p", Agent: "demo"}); !errors.Is(err, ErrConflict) {
		t.Fatal("dependency scheduled", err)
	}
	r := dispatchEngineTask(t, s, a, "run-a")
	event := finishEngineRun(t, s, r, "COMPLETED")
	a, _ = s.Task(ctx, "a")
	if a.Status != "IN_REVIEW" || a.Version != 4 {
		t.Fatalf("unexpected projection %+v", a)
	}
	if _, err := s.Dispatch(ctx, b.ID, b.Version, protocol.RunSpec{ID: "still-early", Project: "p", Agent: "demo"}); !errors.Is(err, ErrConflict) {
		t.Fatal("review bypass", err)
	}
	submission := tasks.Submit{Version: a.Version, RunID: r.ID, Submission: tasks.Submission{Summary: "done", Checks: []string{"test passed"}, Artifacts: []tasks.Artifact{{ID: "design", Version: "v1", URI: "file:///tmp/design.png"}}}, RequestID: "submit-a"}
	a, err := s.SubmitTask(ctx, a.ID, submission)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.SubmitTask(ctx, a.ID, submission)
	if err != nil || again.Version != a.Version {
		t.Fatal("submission replay", again, err)
	}
	a, err = s.ReviewTask(ctx, a.ID, tasks.Review{Version: a.Version, Accept: false, Feedback: "add a check"})
	if err != nil || a.Status != "REWORK" {
		t.Fatal(a, err)
	}
	a = readyEngineTask(t, s, a)
	r2 := dispatchEngineTask(t, s, a, "run-a2")
	if r2.SessionID != "session-a" || r2.Attempt != 2 {
		t.Fatal("task session/attempt lost", r2)
	}
	finishEngineRun(t, s, r2, "COMPLETED")
	a, _ = s.Task(ctx, "a")
	a, err = s.ReviewTask(ctx, a.ID, tasks.Review{Version: a.Version, Accept: true, RequestID: "accept-a"})
	if err != nil || a.Status != "DONE" {
		t.Fatal(a, err)
	}
	if err = s.Apply(ctx, event); err != nil {
		t.Fatal(err)
	}
	a2, _ := s.Task(ctx, "a")
	if a2.Version != a.Version || a2.Status != "DONE" {
		t.Fatal("late event regressed task")
	}
	rb := dispatchEngineTask(t, s, b, "run-b")
	if rb.SessionID != "" {
		t.Fatal("another task reused thread", rb)
	}
	attempts, err := s.Attempts(ctx, "a")
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
	if _, err := s.UpdateTask(ctx, a.ID, tasks.Update{Version: a.Version, Dependencies: &deps}); !errors.Is(err, ErrInput) {
		t.Fatal("cycle accepted", err)
	}
	title := "updated"
	in := tasks.Update{Version: a.Version, Title: &title, RequestID: "update-a"}
	updated, err := s.UpdateTask(ctx, a.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.UpdateTask(ctx, a.ID, in)
	if err != nil || replay.Version != updated.Version {
		t.Fatal(replay, err)
	}
	changed := "different"
	in.Title = &changed
	if _, err = s.UpdateTask(ctx, a.ID, in); !errors.Is(err, ErrConflict) {
		t.Fatal("idempotency key reuse accepted", err)
	}
	if _, err = s.TaskAction(ctx, a.ID, tasks.Action{Version: 1, Action: "ready"}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale version accepted", err)
	}
	_, err = s.CreateProject(ctx, tasks.ProjectCreate{ID: "other", Name: "Other", Workspace: "/tmp/task-engine-other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, tasks.Create{ProjectID: "other", Title: "bad", Dependencies: []string{a.ID}}); !errors.Is(err, ErrInput) {
		t.Fatal("cross-project dependency", err)
	}
	all, err := s.Tasks(ctx, "other", "")
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
			if _, err := s.Dispatch(ctx, a.ID, a.Version, protocol.RunSpec{ID: id, Project: "p", Agent: "demo"}); err == nil {
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
	if _, err := s.Dispatch(ctx, b.ID, b.Version, protocol.RunSpec{ID: "run-b", Project: "p", Agent: "demo"}); !errors.Is(err, ErrConflict) {
		t.Fatal("writer or agent slot bypass", err)
	}
	var commands, attempts int
	_ = s.DB.QueryRow("SELECT count(*) FROM outbox WHERE subject LIKE 'factory.commands.%'").Scan(&commands)
	_ = s.DB.QueryRow("SELECT count(*) FROM task_attempts").Scan(&attempts)
	if commands != 1 || attempts != 1 {
		t.Fatal("non-atomic dispatch", commands, attempts)
	}
}
func TestTaskUnknownReconciliationAndCancel(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	a := readyEngineTask(t, s, createEngineTask(t, s, "a"))
	r := dispatchEngineTask(t, s, a, "run-a")
	if _, err := s.Claim(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	_, _ = s.DB.Exec("UPDATE runs SET lease_until=? WHERE id=?", time.Now().Add(-time.Minute).Unix(), r.ID)
	if err := s.Expire(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Task(ctx, a.ID)
	if a.Status != "BLOCKED" || a.RetryEligible {
		t.Fatal(a)
	}
	if _, err := s.TaskAction(ctx, a.ID, tasks.Action{Version: a.Version, Action: "ready"}); !errors.Is(err, ErrConflict) {
		t.Fatal("unknown retried", err)
	}
	if err := s.Resolve(ctx, r.ID, "FAILED", "workspace inspected; result unknown and new attempt needs owner intent"); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Task(ctx, a.ID)
	if a.Status != "FAILED" || a.RetryEligible {
		t.Fatal("reconciliation retried automatically", a)
	}
	a = readyEngineTask(t, s, a)
	r = dispatchEngineTask(t, s, a, "run-a2")
	if err := s.Cancel(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Task(ctx, a.ID)
	if a.Status != "CANCELLED" {
		t.Fatal(a)
	}
	if _, err := s.Claim(ctx, r.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("cancelled queued task claimed")
	}
}

func TestProjectVersionAndReplay(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	in := tasks.ProjectUpdate{Version: 1, Name: "Updated", RequestID: "update-project"}
	p, err := s.UpdateProject(ctx, "p", in)
	if err != nil || p.Version != 2 {
		t.Fatal(p, err)
	}
	again, err := s.UpdateProject(ctx, "p", in)
	if err != nil || again.Version != 2 {
		t.Fatal(again, err)
	}
	in.RequestID = "stale"
	if _, err = s.UpdateProject(ctx, "p", in); !errors.Is(err, ErrConflict) {
		t.Fatal("stale project version accepted", err)
	}
}
func TestRunningTaskCancellation(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	a := readyEngineTask(t, s, createEngineTask(t, s, "a"))
	r := dispatchEngineTask(t, s, a, "run-a")
	lease, err := s.Claim(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, _ = s.Task(ctx, a.ID)
	a, err = s.TaskAction(ctx, a.ID, tasks.Action{Version: a.Version, Action: "cancel"})
	if err != nil || !a.CancelRequested || a.Status != "IN_PROGRESS" {
		t.Fatal(a, err)
	}
	cancel, err := s.Heartbeat(ctx, r.ID, lease)
	if err != nil || !cancel {
		t.Fatal("cancel not delivered", err)
	}
	if err = s.Apply(ctx, protocol.Wrap("agent.completed", r.ID, "", protocol.Event{RunID: r.ID, Lease: lease, Status: "CANCELLED"})); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Task(ctx, a.ID)
	if a.Status != "CANCELLED" {
		t.Fatal(a)
	}
}

func TestLargeResultsKeepBrokerNotificationsBounded(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	a := readyEngineTask(t, s, createEngineTask(t, s, "large"))
	r := dispatchEngineTask(t, s, a, "large-run")
	lease, err := s.Claim(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("x", 700*1024)
	if err = s.Apply(ctx, protocol.Wrap("agent.completed", r.ID, "", protocol.Event{RunID: r.ID, Lease: lease, Status: "COMPLETED", Result: text})); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Task(ctx, a.ID)
	if a.Status != "IN_REVIEW" || len(a.Result) != len(text) {
		t.Fatal("large result lost")
	}
	out, err := s.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range out {
		if strings.HasPrefix(o.Subject, "factory.events.") && len(o.Body) > 4096 {
			t.Fatal("unbounded entity notification", len(o.Body))
		}
	}
}

package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

func TestPausesStopDispatchAndShowInWhy(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	a := readyEngineTask(t, s, createEngineTask(t, s, "a"))
	for _, bad := range []Pause{{Scope: "project"}, {Scope: "company", Target: "x"}, {Scope: "node", Target: "n"}} {
		if err := s.SetPause(ctx, co, bad); !errors.Is(err, ErrInput) {
			t.Fatal("invalid pause accepted", bad, err)
		}
	}
	for _, p := range []Pause{{Scope: "project", Target: "p", Reason: "release freeze"}, {Scope: "agent", Target: "demo"}, {Scope: "company"}} {
		if err := s.SetPause(ctx, co, p); err != nil {
			t.Fatal(err)
		}
		if err := s.SetPause(ctx, co, p); err != nil {
			t.Fatal("pause not idempotent", err)
		}
		if v := verdict(t, s, "a"); !strings.HasPrefix(v.Reason, tasks.ReasonPaused) || v.Clearer.Who != "owner" {
			t.Fatal("not paused", p, v)
		}
		if _, err := s.Dispatch(ctx, co, "a", a.Version, protocol.RunSpec{ID: "r-" + p.Scope, Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex}); !errors.Is(err, ErrConflict) {
			t.Fatal("dispatched while paused", p, err)
		}
	}
	// The most general pause is reported first.
	if v := verdict(t, s, "a"); v.Reason != tasks.ReasonPaused+"company" || v.Clearer.Action != "factory resume --all" {
		t.Fatal(v)
	}
	if ps, _ := s.Pauses(ctx, co); len(ps) != 3 || ps[1].Scope != "company" && ps[0].Scope != "agent" {
		t.Fatal(ps)
	}
	for _, p := range [][2]string{{"company", ""}, {"agent", "demo"}} {
		if err := s.ClearPause(ctx, co, p[0], p[1], ""); err != nil {
			t.Fatal(err)
		}
	}
	if v := verdict(t, s, "a"); v.Reason != tasks.ReasonPaused+"project:p" || v.Clearer.Action != "factory resume project p" {
		t.Fatal(v)
	}
	if err := s.ClearPause(ctx, co, "project", "p", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearPause(ctx, co, "project", "p", ""); err != nil {
		t.Fatal("resume not idempotent", err)
	}
	expectVerdict(t, s, "a", tasks.ReasonRunnable, "ready")
	dispatchEngineTask(t, s, a, "after-resume")
	page, _ := s.Feed(ctx, co, FeedFilter{Attention: true}, 0, 100)
	paused := 0
	for _, item := range page.Items {
		if item.Type == "owner.paused" {
			paused++
		}
	}
	if paused != 3 {
		t.Fatal("pause events", paused)
	}
}

func TestRevokeReturnsTasksToReady(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	// One writer per project: each task gets its own project.
	task := func(id, agent string) tasks.Task {
		t.Helper()
		if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p-" + id, Name: id, Workspace: "/tmp/revoke-" + id}); err != nil {
			t.Fatal(err)
		}
		v, err := s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "p-" + id, Title: id, Assignee: agent, Ready: true})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	queued := dispatchEngineTask(t, s, task("queued", "a1"), "q1")
	running := dispatchEngineTask(t, s, task("running", "a2"), "r1")
	finishing := dispatchEngineTask(t, s, task("finishing", "a3"), "f1")
	runLease, err := s.Claim(ctx, co, running.ID)
	if err != nil {
		t.Fatal(err)
	}
	finLease, err := s.Claim(ctx, co, finishing.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.RevokeLive(ctx, co, "owner"); err != nil || n != 3 {
		t.Fatal(n, err)
	}
	if n, _ := s.RevokeLive(ctx, co, "owner"); n != 0 {
		t.Fatal("revoked twice", n)
	}
	status := func(id string) string {
		v, _ := s.Task(ctx, co, id)
		return v.Status
	}
	if r, _ := s.Get(ctx, co, queued.ID); r.Status != "ENDED" || r.Outcome != "revoked" || status("queued") != "READY" {
		t.Fatal("queued run", r.Status, status("queued"))
	}
	if cancel, err := s.Heartbeat(ctx, co, running.ID, runLease); err != nil || !cancel {
		t.Fatal("running run not asked to stop", cancel, err)
	}
	apply := func(run protocol.RunSpec, lease, outcome string) {
		t.Helper()
		if err := s.Apply(ctx, co, protocol.Wrap("agent.completed", run.ID, "", protocol.Event{RunID: run.ID, Lease: lease, Status: "ENDED", Outcome: outcome, Result: "x"})); err != nil {
			t.Fatal(err)
		}
	}
	apply(running, runLease, "revoked")
	apply(finishing, finLease, "succeeded") // the turn ended before the interrupt landed
	if status("running") != "READY" || status("finishing") != "IN_REVIEW" {
		t.Fatal(status("running"), status("finishing"))
	}
	// An owner cancel still cancels the task.
	again, _ := s.Task(ctx, co, "running")
	run := dispatchEngineTask(t, s, again, "r2")
	lease, _ := s.Claim(ctx, co, run.ID)
	again, _ = s.Task(ctx, co, "running")
	if _, err = s.TaskAction(ctx, co, "running", tasks.Action{Version: again.Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RevokeLive(ctx, co, "owner"); err != nil {
		t.Fatal(err)
	}
	apply(run, lease, "revoked")
	if status("running") != "CANCELLED" {
		t.Fatal("owner cancel lost", status("running"))
	}
}

// TestCompanyFeedSubjects: company-wide feed events (pauses) get a valid subject; an empty project token made
// JetStream refuse the message and stopped the outbox publisher.
func TestCompanyFeedSubjects(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	if err := s.SetPause(ctx, DefaultCompany, Pause{Scope: "company", Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearPause(ctx, DefaultCompany, "company", "", "owner"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DB.Query("SELECT subject FROM outbox")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var subject string
		_ = rows.Scan(&subject)
		n++
		if strings.Contains(subject, "..") || strings.HasSuffix(subject, ".") {
			t.Errorf("invalid subject %q", subject)
		}
	}
	if n == 0 {
		t.Fatal("no feed events")
	}
}

// TestOutboxBackoff: a message that fails is retried later; the others stay pending and are not blocked.
func TestOutboxBackoff(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	for _, id := range []string{"bad", "good"} {
		if _, err := s.DB.Exec("INSERT INTO outbox(id,subject,body,company_id) VALUES(?,?,?,?)", id, "factory.events.x", "{}", DefaultCompany); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PublishFailed(ctx, "bad", errors.New("nats: no response from stream")); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.Pending(ctx)
	if len(pending) != 1 || pending[0].ID != "good" {
		t.Fatalf("%+v", pending)
	}
	fake.Advance(time.Second)
	if pending, _ = s.Pending(ctx); len(pending) != 2 {
		t.Fatalf("failed message not retried after its backoff: %+v", pending)
	}
	var lastError string
	_ = s.DB.QueryRow("SELECT last_error FROM outbox WHERE id='bad'").Scan(&lastError)
	if !strings.Contains(lastError, "no response") {
		t.Fatal(lastError)
	}
}

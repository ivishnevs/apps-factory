package core

import (
	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSchedulerAutomaticRetry: a failed task runs again after 1 and 5 minutes of backoff with the previous
// outcome in its prompt; the third attempt without progress escalates (retries_exhausted) to a manager, and
// the retry answer runs it again with fresh counters.
func TestSchedulerAutomaticRetry(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Agents: agents}
	c.Handler() // wires escalation routing
	co := storage.DefaultCompany
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/core-scheduler"}); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, co, tasks.Create{ProjectID: "p", Title: "task", Assignee: "demo", Ready: true})
	if err != nil {
		t.Fatal(err)
	}
	fail := func(attempt int) protocol.RunSpec {
		t.Helper()
		if err = c.Schedule(ctx); err != nil {
			t.Fatal(err)
		}
		task, _ = s.Task(ctx, co, task.ID)
		if task.Attempt != attempt || task.Status != "IN_PROGRESS" {
			t.Fatalf("attempt %d: %+v", attempt, task)
		}
		run, err := s.Get(ctx, co, task.CurrentRunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Spec.ConfigHash == "" || len(run.Spec.ConfigSnapshot) == 0 || len(run.Spec.Skills) == 0 {
			t.Fatal("missing config snapshot", run)
		}
		lease, err := s.Claim(ctx, co, run.Spec.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Apply(ctx, co, protocol.Wrap("agent.failed", run.Spec.ID, "", protocol.Event{RunID: run.Spec.ID, Lease: lease, Status: "FAILED", Result: "known failure", Outcome: "failed"})); err != nil {
			t.Fatal(err)
		}
		return run.Spec
	}
	fail(1)
	for _, wait := range []time.Duration{time.Minute, 5 * time.Minute} {
		// Not before the backoff.
		fake.Advance(wait - time.Second)
		if err = c.Schedule(ctx); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Task(ctx, co, task.ID); got.Status != "READY" {
			t.Fatalf("dispatched during the backoff: %+v", got)
		}
		fake.Advance(time.Second)
		spec := fail(task.Attempt + 1)
		if !strings.Contains(spec.Prompt, "Previous run: failed (automatic retry)") {
			t.Fatal(spec.Prompt)
		}
	}
	// Three attempts without progress: escalated to the least loaded manager, not retried.
	fake.Advance(time.Hour)
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, co, task.ID)
	open, _ := s.Escalations(ctx, co, true)
	if task.Status != "FAILED" || task.Attempt != 3 || len(open) != 1 || open[0].Reason != "retries_exhausted" || open[0].Target != "manager" {
		t.Fatalf("%+v %+v", task, open)
	}
	q, _ := s.Message(ctx, co, open[0].MessageID)
	if _, err = s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "manager", Body: "try again", Answers: map[string]any{"answer": "retry"}, RequestID: "retry"}); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, co, task.ID)
	if task.Status != "READY" || task.AttemptsNoProgress != 0 || task.FailStreak != 0 || task.NotBefore != 0 {
		t.Fatalf("%+v", task)
	}
	fail(4)
}

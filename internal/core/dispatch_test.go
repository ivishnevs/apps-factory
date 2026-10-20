package core

import (
	"context"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

func schedulerCore(t *testing.T) (*Core, *storage.Store, *clock.Fake) {
	t.Helper()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Agents: agents}
	c.Handler()
	if _, err = s.CreateProject(context.Background(), storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/core-dispatch"}); err != nil {
		t.Fatal(err)
	}
	return c, s, fake
}

func taskAction(t *testing.T, s *storage.Store, id, action string) {
	t.Helper()
	task, err := s.Task(context.Background(), storage.DefaultCompany, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TaskAction(context.Background(), storage.DefaultCompany, id, tasks.Action{Version: task.Version, Action: action, Reason: action}); err != nil {
		t.Fatal(id, action, err)
	}
}

// TestDispatchOrder (trigger-log-scheduling D4): runnable tasks are dispatched by priority, then by their
// oldest unhandled trigger, then by id; the project's one writer slot goes to the first.
func TestDispatchOrder(t *testing.T) {
	ctx := context.Background()
	c, s, _ := schedulerCore(t)
	co := storage.DefaultCompany
	for id, priority := range map[string]int{"a": 0, "b": 0, "c": 5} {
		if _, err := s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "p", Title: id, Assignee: "demo", Priority: priority}); err != nil {
			t.Fatal(err)
		}
	}
	// b is released before a: its trigger is older, although a sorts first by id.
	taskAction(t, s, "b", "ready")
	taskAction(t, s, "a", "ready")
	taskAction(t, s, "c", "ready")
	for _, want := range []string{"c", "b", "a"} { // priority first, then the oldest trigger
		if err := c.Schedule(ctx); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"a", "b", "c"} {
			if task, _ := s.Task(ctx, co, id); (task.Status == "IN_PROGRESS") != (id == want) {
				t.Fatalf("want %s dispatched, %s is %s", want, id, task.Status)
			}
		}
		taskAction(t, s, want, "cancel") // frees the writer slot for the next pick
	}
}

// TestIdleTaskIsNotDispatched: a READY task whose triggers are all handled (wake_seq = handled_seq) waits
// with reason idle; a new trigger dispatches it.
func TestIdleTaskIsNotDispatched(t *testing.T) {
	ctx := context.Background()
	c, s, _ := schedulerCore(t)
	co := storage.DefaultCompany
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "idle", ProjectID: "p", Title: "idle", Assignee: "demo", Ready: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE tasks SET handled_seq=wake_seq WHERE id='idle'"); err != nil {
		t.Fatal(err)
	}
	if err := c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	snap, _ := s.TaskSnapshot(ctx, co, "idle")
	if got, v := snap.Task, c.evaluate(snap); got.Status != "READY" || got.CurrentRunID != "" || v.Reason != tasks.ReasonIdle {
		t.Fatalf("%+v %+v", got, v)
	}
	if _, err := s.CommentTask(ctx, co, "idle", tasks.Comment{Text: "please continue", RequestID: "wake"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Task(ctx, co, "idle"); got.Status != "IN_PROGRESS" {
		t.Fatalf("the owner's comment did not wake the idle task: %+v", got)
	}
}

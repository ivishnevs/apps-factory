package core

import (
	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"context"
	"path/filepath"
	"testing"
)

func TestSchedulerRetryLimitsAndSnapshots(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	a := agents["demo"]
	a.Spec.Execution.MaxRetries = 1
	agents["demo"] = a
	c := &Core{Store: s, Agents: agents}
	if _, err = s.CreateProject(ctx, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/core-scheduler"}); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, tasks.Create{ProjectID: "p", Title: "task", Assignee: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.TaskAction(ctx, task.ID, tasks.Action{Version: task.Version, Action: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err = c.Schedule(ctx); err != nil {
			t.Fatal(err)
		}
		task, _ = s.Task(ctx, task.ID)
		if task.Attempt != attempt || task.Status != "IN_PROGRESS" {
			t.Fatal(task)
		}
		run, err := s.Get(ctx, task.CurrentRunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Spec.ConfigHash == "" || len(run.Spec.ConfigSnapshot) == 0 || len(run.Spec.Skills) == 0 {
			t.Fatal("missing config snapshot", run)
		}
		lease, err := s.Claim(ctx, run.Spec.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Apply(ctx, protocol.Wrap("agent.failed", run.Spec.ID, "", protocol.Event{RunID: run.Spec.ID, Lease: lease, Status: "FAILED", Result: "known failure"})); err != nil {
			t.Fatal(err)
		}
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, task.ID)
	if task.Status != "FAILED" || task.Attempt != 2 {
		t.Fatal("retry limit ignored", task)
	}
}

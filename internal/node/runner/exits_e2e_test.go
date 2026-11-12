package runner

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

// TestExplicitExitEndToEnd: Core with explicit exits, a v2 node, a runtime (fake) that calls
// factory_exit_submit through the real factory-mcp binary. The exit is accepted, the task is in review,
// and the run ends succeeded with exit_state accepted, although the turn kept running after the exit.
func TestExplicitExitEndToEnd(t *testing.T) {
	bin := t.TempDir()
	buildNodeBin(t, bin)
	beat := storage.HeartbeatSeconds
	storage.HeartbeatSeconds = 1
	defer func() { storage.HeartbeatSeconds = beat }()
	grace := worker.ExitGrace
	worker.ExitGrace = 500 * time.Millisecond
	defer func() { worker.ExitGrace = grace }()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &core.Core{Store: s, Agents: agents, Token: "test-token", ExplicitExits: true}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	go c.Run(ctx)
	co := storage.DefaultCompany
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "notes", Name: "notes", Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "t1", ProjectID: "notes", Title: "write the notes", Assignee: "demo", Ready: true, RequestID: "c"}); err != nil {
		t.Fatal(err)
	}
	// The runtime calls the exit, then keeps its turn open: the node stops it after the exit grace.
	d := &fake.Driver{MCP: true, Script: fake.Script{Activity: []fake.Activity{
		{Tool: &fake.ToolCall{Name: "factory_exit_submit", Arguments: map[string]any{"summary": "notes written", "checks": []any{"proofread"}}}},
	}}}
	cfg := node.Defaults()
	cfg.Slots, cfg.StateDir, cfg.BinDir = 1, t.TempDir(), bin
	api := enrolled(t, s, cfg.StateDir, server.URL)
	nodeCtx, stopNode := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(nodeCtx, Node{Config: cfg, API: api, Drivers: []driver.Driver{perRunSessions{d}}})
	}()
	var run storage.Run
	for {
		task, _ := s.Task(ctx, co, "t1")
		if task.CurrentRunID != "" {
			run, _ = s.Get(ctx, co, task.CurrentRunID)
		}
		if run.Status == "ENDED" {
			break
		}
		select {
		case err = <-done:
			t.Fatal("node stopped", err)
		case <-ctx.Done():
			t.Fatalf("timeout: task %+v run %+v", task, run)
		case <-time.After(100 * time.Millisecond):
		}
	}
	task, _ := s.Task(ctx, co, "t1")
	if run.Outcome != "succeeded" || task.Status != "IN_REVIEW" || task.Submission == nil || task.Submission.Summary != "notes written" || task.Implementer != "demo" {
		t.Fatalf("run %+v\ntask %+v", run, task)
	}
	events, _ := s.Events(ctx, co, run.Spec.ID)
	var terminal protocol.Event
	for _, e := range events {
		var p protocol.Event
		if json.Unmarshal(e.Payload, &p) == nil && p.Status != "" {
			terminal = p
		}
	}
	if terminal.ExitState != "accepted" {
		t.Fatalf("terminal event %+v", terminal)
	}
	stopNode()
	<-done
}

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

// TestNodesCommandAgainstCore: `factory nodes token create|list|revoke` and `nodes list`; `factory tasks why`
// shows the node reasons (no_capable_node while no registered node has the assignee's driver).
func TestNodesCommandAgainstCore(t *testing.T) {
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
	c := &core.Core{Store: s, Agents: agents, Token: "owner"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	api := worker.API{URL: server.URL, Token: "owner"}
	run := func(args ...string) string {
		t.Helper()
		return capture(t, func() error { return nodesCommand(ctx, api, args) })
	}
	var issued storage.IssuedNodeToken
	if err = json.Unmarshal([]byte(run("nodes", "token", "create", "mini-1", "--node-id", "n1")), &issued); err != nil || !strings.HasPrefix(issued.Token, storage.NodeTokenPrefix) || issued.NodeID != "n1" {
		t.Fatal(issued, err)
	}
	if out := run("nodes", "token", "list"); !strings.Contains(out, issued.ID) || strings.Contains(out, issued.Token) {
		t.Fatal(out)
	}
	if out := run("nodes", "token", "revoke", issued.ID); !strings.Contains(out, `"revoked_at"`) {
		t.Fatal(out)
	}
	if err = nodesCommand(ctx, api, []string{"nodes", "token", "create", "x", "--bogus", "y"}); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatal(err)
	}

	co := storage.DefaultCompany
	if _, err = s.RegisterNode(ctx, co, storage.NodeRegistration{NodeID: "n2", Name: "linux-1", Epoch: 1, ProtocolVersion: 2, Slots: 1,
		Drivers: []storage.NodeDriver{{Name: "claude", OK: true}}}); err != nil {
		t.Fatal(err)
	}
	if out := run("nodes", "list"); !strings.Contains(out, `"linux-1"`) {
		t.Fatal(out)
	}
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/nodes-why"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "a", ProjectID: "p", Title: "a", Assignee: "demo", Ready: true}); err != nil {
		t.Fatal(err)
	}
	out := capture(t, func() error { return whyCommand(ctx, api, []string{"a"}) })
	if !strings.Contains(out, "no_capable_node") || !strings.Contains(out, "no usable codex driver") {
		t.Fatal(out)
	}
}

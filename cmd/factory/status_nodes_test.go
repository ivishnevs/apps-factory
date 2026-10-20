package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"appfactory/internal/core"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

// TestStatusNodes: factory status ends with the node section (online/offline, epoch, busy/total slots,
// drivers, disk, load, failing checks); status of one project has none.
func TestStatusNodes(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	c := &core.Core{Store: s, Token: "owner"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	api := worker.API{URL: server.URL, Token: "owner"}
	co := storage.DefaultCompany
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "Calories", Workspace: "/tmp/console"}); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"n1", "n2"} {
		if _, err = s.RegisterNode(ctx, co, storage.NodeRegistration{NodeID: id, Name: "mini-" + id, Epoch: int64(3 + i), ProtocolVersion: 2, Slots: 2,
			Drivers: []storage.NodeDriver{{Name: "codex", OK: true}, {Name: "claude", OK: false}}, Resources: map[string]int{"simulator": 2, "build": 1}}); err != nil {
			t.Fatal(err)
		}
	}
	// One claimed run holding a simulator on n1.
	if err = s.Create(ctx, co, protocol.RunSpec{ID: "r1", Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex, Workspace: "/tmp/console", Prompt: "work", TimeoutSeconds: 60,
		ConfigSnapshot: json.RawMessage(`{"spec":{"resources":{"simulator":1}}}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimV2(ctx, co, "r1", storage.ClaimRequest{NodeID: "n1", Epoch: 3, ClaimID: "c1"}); err != nil {
		t.Fatal(err)
	}
	health := `{"load1":1.5,"cpus":8,"free_disk_bytes":12884901888,"busy_slots":1,"disk":"low","checks":[{"check":"stack.simctl","status":"fail"},{"check":"claude.auth","status":"fail"}]}`
	if _, err = s.NodeHeartbeat(ctx, co, "n1", storage.NodeHeartbeat{Epoch: 3, Health: json.RawMessage(health)}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE nodes SET online=0 WHERE id='n2'"); err != nil {
		t.Fatal(err)
	}
	out := capture(t, func() error { return consoleCommand(ctx, api, []string{"status"}) })
	for _, want := range []string{"nodes:", "NODE", "FAILING CHECKS", "RESOURCES", "mini-n1  online", "1/2", "build 0/1, simulator 1/2", "build 0/1, simulator 0/2", "codex ok, claude off", "low 12.0 GiB", "1.50", "stack.simctl, claude.auth", "mini-n2  offline", "0/2"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	if one := capture(t, func() error { return consoleCommand(ctx, api, []string{"status", "p"}) }); strings.Contains(one, "nodes:") {
		t.Errorf("project status shows nodes:\n%s", one)
	}
	t.Log("\n" + out)
}

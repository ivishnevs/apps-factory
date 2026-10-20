package runner

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/worker"
)

// TestJetStreamWatchdogKilledRuntime (node-agent 3b.2): a run's real runtime process is killed from outside
// the node while the run is in progress; the watchdog cancels the run, the run ends, and the node's only
// slot is free again: the next run is claimed and starts.
func TestJetStreamWatchdogKilledRuntime(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to a dedicated JetStream instance")
	}
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	defer b.JS.DeleteStream("FACTORY_COMMANDS")
	defer b.JS.DeleteStream("FACTORY_EVENTS")
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &core.Core{Store: s, Bus: b, Agents: agents, Token: "test-token"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	go c.Run(ctx)
	queue := func(id string) {
		t.Helper()
		spec := protocol.RunSpec{ID: id, Project: id, Agent: id, Provider: protocol.RuntimeCodex, Workspace: t.TempDir(), Prompt: "work", TimeoutSeconds: 60}
		if err := s.Create(ctx, storage.DefaultCompany, spec); err != nil {
			t.Fatal(err)
		}
	}
	waitRun := func(id string, ok func(storage.Run) bool) storage.Run {
		t.Helper()
		for {
			r, e := s.Get(ctx, storage.DefaultCompany, id)
			if e == nil && ok(r) {
				return r
			}
			select {
			case <-ctx.Done():
				t.Fatalf("%s: timeout in %s", id, r.Status)
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	running := func(r storage.Run) bool { return r.Status == "RUNNING" && r.SessionID != "" }

	// No status: each session lasts until interrupted; its runtime is a real process (group leader).
	d := &fake.Driver{Command: []string{"sleep", "60"}}
	cfg := node.Defaults()
	cfg.Slots, cfg.StateDir = 1, t.TempDir()
	nodeCtx, stopNode := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(nodeCtx, Node{Config: cfg, Bus: b, API: worker.API{URL: server.URL, Token: "test-token"}, Drivers: []driver.Driver{perRunSessions{d}},
			FetchWait: 200 * time.Millisecond, Watchdog: &node.Watchdog{Interval: 100 * time.Millisecond}})
	}()
	defer func() { stopNode(); <-done }()

	queue("killed-run")
	waitRun("killed-run", running)
	var group int
	for group == 0 {
		tracked, _ := worker.TrackedGroups(cfg.StateDir)
		for _, g := range tracked {
			if g.Run == "killed-run" {
				group = g.Group
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("runtime group not tracked")
		case <-time.After(20 * time.Millisecond):
		}
	}
	queue("next-run") // waits for the only slot
	time.Sleep(500 * time.Millisecond)
	if r, _ := s.Get(ctx, storage.DefaultCompany, "next-run"); r.Status != "QUEUED" {
		t.Fatalf("next run %s while the slot is busy", r.Status)
	}

	// Killed outside the node (an OOM kill, a user's kill -9).
	if err = syscall.Kill(group, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	r := waitRun("killed-run", func(r storage.Run) bool { return r.Status == "ENDED" })
	if r.Outcome != "lost" { // the runtime vanished: no terminal evidence
		t.Fatalf("killed run %+v", r)
	}
	waitRun("next-run", running)
	if l := d.Launches(); len(l) != 2 || l[1].Spec.ID != "next-run" {
		t.Fatalf("launches %d", len(l))
	}
	if tracked, _ := worker.TrackedGroups(cfg.StateDir); len(tracked) != 1 || tracked[0].Run != "next-run" {
		t.Fatalf("tracked runtimes %+v", tracked)
	}
}

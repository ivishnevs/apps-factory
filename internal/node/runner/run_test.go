package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/claude"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/worker"
)

// TestDriversClaudeBinDir: the Claude driver probes (v2 register) under the node's bin_dir, not ./bin of
// whatever directory the node was started in.
func TestDriversClaudeBinDir(t *testing.T) {
	c := node.Defaults()
	on := true
	c.BinDir, c.Drivers.Claude.Enabled = "/opt/factory/bin", &on
	for _, d := range Drivers(c, func(string) string { return "" }) {
		if cd, ok := d.(claude.Driver); ok {
			if cd.BinDir != c.BinDir || cd.Shim != filepath.Join(c.BinDir, "..", "workers", "claude", "dist", "src", "shim.js") {
				t.Fatalf("%+v", cd)
			}
			return
		}
	}
	t.Fatal("claude driver not built")
}

// TestJetStreamNodeSlots runs three hanging fake-driver runs concurrently on a node with three slots,
// then shuts the node down, which interrupts all of them (node-agent 1.1).
func TestJetStreamNodeSlots(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go c.Run(ctx)
	runs := []string{"slot-run-1", "slot-run-2", "slot-run-3"}
	for _, id := range runs {
		spec := protocol.RunSpec{ID: id, Project: id, Agent: id, Provider: protocol.RuntimeCodex, Workspace: t.TempDir(), Prompt: "work", TimeoutSeconds: 60} // one running run per agent
		if err = s.Create(ctx, storage.DefaultCompany, spec); err != nil {
			t.Fatal(err)
		}
	}
	d := &fake.Driver{} // no status: each run lasts until interrupted
	cfg := node.Defaults()
	cfg.Slots, cfg.StateDir = 3, t.TempDir()
	nodeCtx, stopNode := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(nodeCtx, Node{Config: cfg, Bus: b, API: worker.API{URL: server.URL, Token: "test-token"}, Drivers: []driver.Driver{perRunSessions{d}}})
	}()
	for running := 0; running < len(runs); {
		select {
		case err = <-done:
			t.Fatal("node stopped", err)
		case <-ctx.Done():
			t.Fatal("timeout; launches:", len(d.Launches()))
		case <-time.After(50 * time.Millisecond):
		}
		running = 0
		for _, id := range runs {
			if r, e := s.Get(ctx, storage.DefaultCompany, id); e == nil && r.Status == "RUNNING" && r.SessionID != "" {
				running++
			}
		}
	}
	stopNode()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	for _, id := range runs {
		for {
			r, e := s.Get(ctx, storage.DefaultCompany, id)
			if e != nil {
				t.Fatal(e)
			}
			if r.Status == "ENDED" && r.Outcome == "lost" { // a node shutdown is a loss, never a revoke
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(id, r.Status)
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
}

// perRunSessions gives each fake session its own ID; Core keeps session IDs unique.
type perRunSessions struct{ *fake.Driver }
type perRunSession struct {
	driver.Session
	id string
}

func (s perRunSession) ID() string { return s.id }
func (d perRunSessions) Launch(ctx context.Context, l driver.Launch) (driver.Session, error) {
	s, err := d.Driver.Launch(ctx, l)
	if err != nil {
		return nil, err
	}
	return perRunSession{s, "session-" + l.Spec.ID}, nil
}

// TestWatchdogReapsPlantedOrphan: a runtime group recorded in the state dir without a busy slot is killed.
func TestWatchdogReapsPlantedOrphan(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sh", "-c", "sleep 60 & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	t.Cleanup(func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	started, err := exec.Command("ps", "-o", "lstart=", "-p", fmt.Sprint(cmd.Process.Pid)).Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := json.Marshal(map[string]any{"run": "planted", "group": cmd.Process.Pid, "started": strings.TrimSpace(string(started))})
	if err = os.WriteFile(filepath.Join(dir, "planted.pid.json"), pid, 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Pool{}
	w := node.Watchdog{Slots: p, Groups: groups(dir)}
	w.Check()
	if worker.GroupAlive(cmd.Process.Pid) {
		t.Fatal("orphan survived")
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*")); len(files) != 0 {
		t.Fatal(files)
	}
}

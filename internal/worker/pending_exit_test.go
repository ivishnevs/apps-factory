package worker

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"appfactory/internal/driver/fake"
	"appfactory/internal/procgroup"
	"appfactory/internal/protocol"
)

// pendingExit is a scripted runtime command that records a queued exit as the MCP adapter does when Core
// is unreachable.
func pendingExit(kind string) fake.Activity {
	return fake.Activity{Exec: []string{"sh", "-c", `printf '{"state":"pending","kind":"` + kind + `"}' > "$FACTORY_EXIT_FILE"`}}
}

// mcpScript writes a fake factory-mcp (named so, run by /bin/sh) with body.
func mcpScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "factory-mcp")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// startMCPGroup runs script as the leader of its own process group with the exit file in its env.
func startMCPGroup(t *testing.T, script, exitFile string) int {
	t.Helper()
	cmd := exec.Command(script)
	cmd.Env = append(os.Environ(), "FACTORY_EXIT_FILE="+exitFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	return cmd.Process.Pid
}

// TestAwaitExitDelivery: a pending exit keeps the runtime until it settles, factory-mcp leaves the group, the
// run is stopped or ExitDeliveryWait passes; any other exit state returns at once.
func TestAwaitExitDelivery(t *testing.T) {
	defer func(w time.Duration) { ExitDeliveryWait = w }(ExitDeliveryWait)
	const settle = `sleep 0.3; printf '{"state":"accepted","kind":"submit"}' > "$FACTORY_EXIT_FILE"; sleep 30`
	for name, c := range map[string]struct {
		state    string // initial exit file state ("" = no file)
		script   string // fake factory-mcp body; "" = no group
		mcp      string // executable name looked up in the group
		cancel   bool   // the run is stopped meanwhile
		bound    time.Duration
		want     string
		min, max time.Duration
	}{
		"no exit":          {"", "sleep 30", "factory-mcp", false, time.Minute, "none", 0, 200 * time.Millisecond},
		"accepted":         {"accepted", "sleep 30", "factory-mcp", false, time.Minute, "accepted", 0, 200 * time.Millisecond},
		"refused":          {"refused", "sleep 30", "factory-mcp", false, time.Minute, "refused", 0, 200 * time.Millisecond},
		"settles":          {"pending", settle, "factory-mcp", false, time.Minute, "accepted", 200 * time.Millisecond, 5 * time.Second},
		"refused later":    {"pending", `sleep 0.3; printf '{"state":"refused"}' > "$FACTORY_EXIT_FILE"; sleep 30`, "factory-mcp", false, time.Minute, "refused", 200 * time.Millisecond, 5 * time.Second},
		"mcp exits":        {"pending", "sleep 0.3", "factory-mcp", false, time.Minute, "pending", 200 * time.Millisecond, 5 * time.Second},
		"mcp not in group": {"pending", "sleep 30", "other-mcp", false, time.Minute, "pending", 0, 200 * time.Millisecond},
		"no group":         {"pending", "", "factory-mcp", false, time.Minute, "pending", 0, 200 * time.Millisecond},
		"revoked":          {"pending", "sleep 30", "factory-mcp", true, time.Minute, "pending", 150 * time.Millisecond, 5 * time.Second},
		"bounded":          {"pending", "sleep 30", "factory-mcp", false, 400 * time.Millisecond, "pending", 400 * time.Millisecond, 5 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			ExitDeliveryWait = c.bound
			file := filepath.Join(t.TempDir(), "exit.json")
			if c.state != "" {
				if err := os.WriteFile(file, []byte(`{"state":"`+c.state+`","kind":"submit"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			group := 0
			if c.script != "" {
				group = startMCPGroup(t, mcpScript(t, c.script), file)
				time.Sleep(100 * time.Millisecond) // the shell is running
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if c.cancel {
				time.AfterFunc(150*time.Millisecond, cancel)
			}
			start := time.Now()
			got := awaitExitDelivery(ctx, "r", file, group, c.mcp)
			el := time.Since(start)
			if got != c.want || el < c.min || el > c.max {
				t.Fatalf("state %s after %s, want %s within [%s, %s]", got, el, c.want, c.min, c.max)
			}
			if name == "settles" && !procgroup.Alive(group) {
				t.Fatal("the runtime group was stopped while waiting")
			}
		})
	}
}

// A turn that ends with its exit pending keeps the runtime (and factory-mcp in its group) until the
// delivery settles; the terminal event carries the settled state. Without a settle the runtime is stopped
// after ExitDeliveryWait and the terminal event says pending.
func TestJetStreamPendingExitDelivered(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	defer func(w time.Duration) { ExitDeliveryWait = w }(ExitDeliveryWait)
	wait := `while ! grep -q pending "$FACTORY_EXIT_FILE" 2>/dev/null; do sleep 0.05; done; `
	for name, c := range map[string]struct {
		body     string
		bound    time.Duration // ExitDeliveryWait
		want     string
		min, max time.Duration
	}{
		"delivered":   {wait + `sleep 0.5; printf '{"state":"accepted","kind":"submit"}' > "$FACTORY_EXIT_FILE"; sleep 30`, 30 * time.Second, "accepted", 500 * time.Millisecond, 15 * time.Second},
		"undelivered": {wait + `sleep 30`, time.Second, "pending", time.Second, 15 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			ExitDeliveryWait = c.bound
			mcp := mcpScript(t, c.body)
			sc := fake.Script{SessionID: "s", Status: "COMPLETED", Text: "queued", Activity: []fake.Activity{pendingExit("submit")}}
			start := time.Now()
			r, events := runDriver(t, url, Worker{Driver: &fake.Driver{Script: sc, MCP: true, Command: []string{mcp}}, MCPBinary: mcp}, false)
			el := time.Since(start)
			var terminal protocol.Event
			if err := json.Unmarshal(events[len(events)-1].Payload, &terminal); err != nil {
				t.Fatal(err)
			}
			if terminal.Status != "COMPLETED" || terminal.ExitState != c.want || r.Status != "COMPLETED" {
				t.Fatalf("terminal %s exit %s, run %s; want COMPLETED exit %s", terminal.Status, terminal.ExitState, r.Status, c.want)
			}
			if el < c.min || el > c.max {
				t.Fatalf("run took %s, want [%s, %s]", el, c.min, c.max)
			}
		})
	}
}

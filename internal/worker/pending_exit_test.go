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
	// The scripted changes come 1 s after the group is up: a loaded host must not make them happen before
	// awaitExitDelivery starts. Upper bounds only separate the right behaviour from the wrong one, which waits
	// ExitDeliveryWait (a minute): on a loaded host (-race, parallel packages) each poll runs ps and a scripted
	// shell can take seconds, so "at once" is bounded by 10 s and a settle by 30 s.
	const settle = `sleep 1; printf '{"state":"accepted","kind":"submit"}' > "$FACTORY_EXIT_FILE"; sleep 30`
	const now, later = 10 * time.Second, 30 * time.Second
	for name, c := range map[string]struct {
		state    string // initial exit file state ("" = no file)
		script   string // fake factory-mcp body; "" = no group
		mcp      string // executable name looked up in the group
		cancel   bool   // the run is stopped meanwhile
		bound    time.Duration
		want     string
		min, max time.Duration
	}{
		"no exit":          {"", "sleep 30", "factory-mcp", false, time.Minute, "none", 0, now},
		"accepted":         {"accepted", "sleep 30", "factory-mcp", false, time.Minute, "accepted", 0, now},
		"refused":          {"refused", "sleep 30", "factory-mcp", false, time.Minute, "refused", 0, now},
		"settles":          {"pending", settle, "factory-mcp", false, time.Minute, "accepted", 200 * time.Millisecond, later},
		"refused later":    {"pending", `sleep 1; printf '{"state":"refused"}' > "$FACTORY_EXIT_FILE"; sleep 30`, "factory-mcp", false, time.Minute, "refused", 200 * time.Millisecond, later},
		"mcp exits":        {"pending", "sleep 1", "factory-mcp", false, time.Minute, "pending", 200 * time.Millisecond, later},
		"mcp not in group": {"pending", "sleep 30", "other-mcp", false, time.Minute, "pending", 0, now},
		"no group":         {"pending", "", "factory-mcp", false, time.Minute, "pending", 0, now},
		"revoked":          {"pending", "sleep 30", "factory-mcp", true, time.Minute, "pending", 150 * time.Millisecond, later},
		"bounded":          {"pending", "sleep 30", "factory-mcp", false, 400 * time.Millisecond, "pending", 400 * time.Millisecond, later},
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
				for deadline := time.Now().Add(5 * time.Second); !mcpRunning(group, "factory-mcp"); time.Sleep(10 * time.Millisecond) {
					if time.Now().After(deadline) {
						t.Fatal("the fake factory-mcp does not show up in its group")
					}
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			start := time.Now()
			if c.cancel {
				time.AfterFunc(150*time.Millisecond, cancel)
			}
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
			if terminal.Outcome != "succeeded" || terminal.ExitState != c.want || r.Status != "ENDED" || r.Outcome != "succeeded" {
				t.Fatalf("terminal %s exit %s, run %s; want COMPLETED exit %s", terminal.Status, terminal.ExitState, r.Status, c.want)
			}
			if el < c.min || el > c.max {
				t.Fatalf("run took %s, want [%s, %s]", el, c.min, c.max)
			}
		})
	}
}

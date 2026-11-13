package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"appfactory/internal/driver/fake"
	"appfactory/internal/factorytools"
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
func TestPendingExitDelivered(t *testing.T) {
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
			r, events := runDriver(t, Worker{Driver: &fake.Driver{Script: sc, MCP: true, Command: []string{mcp}}, MCPBinary: mcp}, false)
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

// TestRedeliverExit (exit-redelivery): the node delivers the exit factory-mcp left pending, as the run, with its
// ack_seq, until Core answers; the answer settles the exit file and the pending body goes. Core unreachable for longer
// than ExitRedeliverWait leaves it pending.
func TestRedeliverExit(t *testing.T) {
	defer func(w, b time.Duration) { ExitRedeliverWait, exitRedeliverBase = w, b }(ExitRedeliverWait, exitRedeliverBase)
	exitRedeliverBase = 10 * time.Millisecond
	for name, c := range map[string]struct {
		answers []int // status per delivery; the last repeats
		wait    time.Duration
		want    string
	}{
		"accepted after an outage": {[]int{503, 502, 200}, time.Minute, "accepted"},
		"refused":                  {[]int{409}, time.Minute, "refused"},
		"run ended":                {[]int{410}, time.Minute, "refused"},
		"still unreachable":        {[]int{503}, 200 * time.Millisecond, "pending"},
	} {
		t.Run(name, func(t *testing.T) {
			ExitRedeliverWait = c.wait
			var calls int
			var auth, ack, kind string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				auth, ack, kind = r.Header.Get("Authorization"), r.Header.Get("X-Factory-Ack-Seq"), fmt.Sprint(body["kind"])
				w.WriteHeader(c.answers[min(calls, len(c.answers)-1)])
				calls++
			}))
			defer srv.Close()
			file := filepath.Join(t.TempDir(), "exit.json")
			if err := os.WriteFile(file, []byte(`{"state":"pending","kind":"done","exit_sha":"abc"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(factorytools.PendingExitFile(file), []byte(`{"body":{"kind":"done","arguments":{"summary":"s"}},"ack_seq":7}`), 0o600); err != nil {
				t.Fatal(err)
			}
			redeliverExit(context.Background(), "r", file, srv.URL, "run.tok")
			rec := readExitRecord(file)
			if rec.State != c.want || rec.Kind != "done" || rec.ExitSHA != "abc" {
				t.Fatalf("exit file %+v, want %s", rec, c.want)
			}
			if auth != "Bearer run.tok" || ack != "7" || kind != "done" {
				t.Fatalf("delivery auth %q ack %q kind %q", auth, ack, kind)
			}
			_, kept := factorytools.ReadPendingExit(file)
			if kept != (c.want == "pending") {
				t.Fatalf("pending body kept %v", kept)
			}
		})
	}
	// Nothing pending beside the exit file: no delivery.
	file := filepath.Join(t.TempDir(), "exit.json")
	_ = os.WriteFile(file, []byte(`{"state":"pending","kind":"done"}`), 0o600)
	redeliverExit(context.Background(), "r", file, "http://127.0.0.1:1", "tok")
	if readExitState(file).State != "pending" {
		t.Fatal("settled without a delivery")
	}
}

// TestRedeliverExitRules (node-wake-recovery): the node classifies refusals of a pending exit by reason; a stopped
// run still delivers its exit (bounded by StopExitDeliveryWait); a pending body left by a factory-mcp killed
// mid-request is delivered although the exit file never said pending.
func TestRedeliverExitRules(t *testing.T) {
	defer func(w, b, s time.Duration) { ExitRedeliverWait, exitRedeliverBase, StopExitDeliveryWait = w, b, s }(ExitRedeliverWait, exitRedeliverBase, StopExitDeliveryWait)
	exitRedeliverBase = 10 * time.Millisecond
	type answer struct {
		status int
		reason string
	}
	setup := func(t *testing.T, state string, answers []answer) (string, string, *int) {
		t.Helper()
		calls := new(int)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a := answers[min(*calls, len(answers)-1)]
			*calls++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(a.status)
			_ = json.NewEncoder(w).Encode(map[string]string{"reason": a.reason})
		}))
		t.Cleanup(srv.Close)
		file := filepath.Join(t.TempDir(), "exit.json")
		if state != "" {
			_ = os.WriteFile(file, []byte(`{"state":"`+state+`","kind":"done"}`), 0o600)
		}
		if err := os.WriteFile(factorytools.PendingExitFile(file), []byte(`{"body":{"kind":"done"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return file, srv.URL, calls
	}
	for name, c := range map[string]struct {
		answers []answer
		want    string
	}{
		"temporary 401 retried":   {[]answer{{401, "signature_window"}, {401, "signature_window"}, {200, ""}}, "accepted"},
		"lease changed is final":  {[]answer{{401, "lease_changed"}, {200, ""}}, "refused"},
		"agent deleted is final":  {[]answer{{403, "agent_unavailable"}, {200, ""}}, "refused"},
		"busy then accepted":      {[]answer{{429, ""}, {200, ""}}, "accepted"},
		"released run: 410 final": {[]answer{{410, "not_current_run"}, {200, ""}}, "refused"},
	} {
		t.Run(name, func(t *testing.T) {
			ExitRedeliverWait = time.Minute
			file, url, _ := setup(t, "pending", c.answers)
			redeliverExit(context.Background(), "r", file, url, "run.tok")
			if got := readExitState(file).State; got != c.want {
				t.Fatalf("exit %s, want %s", got, c.want)
			}
		})
	}
	t.Run("stopped run still delivers", func(t *testing.T) {
		ExitRedeliverWait, StopExitDeliveryWait = time.Minute, time.Minute
		file, url, calls := setup(t, "pending", []answer{{503, ""}, {200, ""}})
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Core's stop reached the node first
		redeliverExit(ctx, "r", file, url, "run.tok")
		if got := readExitState(file).State; got != "accepted" || *calls != 2 {
			t.Fatalf("exit %s after %d deliveries", got, *calls)
		}
	})
	t.Run("stopped run is bounded", func(t *testing.T) {
		ExitRedeliverWait, StopExitDeliveryWait = time.Minute, 100*time.Millisecond
		file, url, _ := setup(t, "pending", []answer{{503, ""}})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()
		redeliverExit(ctx, "r", file, url, "run.tok")
		if el := time.Since(start); el > 10*time.Second || readExitState(file).State != "pending" {
			t.Fatalf("took %s, state %s", el, readExitState(file).State)
		}
	})
	t.Run("killed mid-request", func(t *testing.T) {
		ExitRedeliverWait = time.Minute
		file, url, _ := setup(t, "", []answer{{200, ""}})
		if !exitUndelivered(file, readExitState(file).State) {
			t.Fatal("a pending body without an answer is not undelivered")
		}
		redeliverExit(context.Background(), "r", file, url, "run.tok")
		if got := readExitState(file).State; got != "accepted" {
			t.Fatal(got)
		}
		if exitUndelivered(file, readExitState(file).State) {
			t.Fatal("still undelivered after acceptance")
		}
	})
}

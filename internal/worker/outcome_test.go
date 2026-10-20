package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"appfactory/internal/driver"
	"appfactory/internal/driver/claude"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
)

// TestRunEnd is the executor's mapping of a started run's result to its terminal status and outcome
// (reliable-recovery 3.4; docs/runtime-protocol.md, Run outcomes from workers).
func TestRunEnd(t *testing.T) {
	missing := &exec.Error{Name: "codex", Err: exec.ErrNotFound}
	for name, c := range map[string]struct {
		result          driver.Result
		stop            string
		quota           bool
		status, outcome string
	}{
		"completed":                        {driver.Result{Status: "COMPLETED"}, "", false, "COMPLETED", ""},
		"completed after timeout":          {driver.Result{Status: "COMPLETED"}, "timed_out", false, "COMPLETED", ""},
		"completed after quota":            {driver.Result{Status: "COMPLETED"}, "", true, "COMPLETED", ""},
		"failed":                           {driver.Result{Status: "FAILED"}, "", false, "FAILED", "failed"},
		"max turns":                        {driver.Result{Status: "FAILED", Outcome: "max_turns"}, "", false, "FAILED", "max_turns"},
		"auth during turn":                 {driver.Result{Status: "FAILED", Outcome: "paused_out"}, "", false, "FAILED", "paused_out"},
		"quota hit":                        {driver.Result{Status: "FAILED"}, "", true, "FAILED", "paused_out"},
		"failed after timeout interrupt":   {driver.Result{Status: "FAILED"}, "timed_out", false, "FAILED", "timed_out"},
		"revoked":                          {driver.Result{Status: "CANCELLED"}, "", false, "CANCELLED", ""},
		"hard timeout":                     {driver.Result{Status: "CANCELLED"}, "timed_out", false, "CANCELLED", "timed_out"},
		"idle stop":                        {driver.Result{Status: "CANCELLED"}, "idle_timeout", false, "CANCELLED", "idle_timeout"},
		"timeout without evidence":         {driver.Result{Err: errors.New("interrupt result unknown")}, "timed_out", false, "FAILED", "timed_out"},
		"idle stop without evidence":       {driver.Result{Err: errors.New("interrupt result unknown")}, "idle_timeout", false, "FAILED", "idle_timeout"},
		"revoke without evidence":          {driver.Result{Err: errors.New("interrupt result unknown")}, "", false, "", ""},
		"app-server disconnect":            {driver.Result{Err: errors.New("app-server disconnected: EOF")}, "", false, "", ""},
		"disconnect after quota":           {driver.Result{Err: errors.New("app-server disconnected: EOF")}, "", true, "", ""},
		"shim exit without result":         {driver.Result{Err: &claude.Error{Class: "no_result", Message: "shim exited"}}, "", false, "", ""},
		"claude auth without result":       {driver.Result{Err: &claude.Error{Class: "auth_error", Message: "authentication_failed"}}, "", false, "FAILED", "paused_out"},
		"claude broken stream":             {driver.Result{Err: &claude.Error{Class: "protocol", Message: "invalid shim line"}}, "", false, "FAILED", "failed"},
		"claude executable":                {driver.Result{Err: &claude.Error{Class: "executable", Message: "no bundled Claude Code"}}, "", false, "FAILED", "paused_out"},
		"runtime binary missing (wrapped)": {driver.Result{Err: fmt.Errorf("launch: %w", missing)}, "", false, "FAILED", "paused_out"},
	} {
		t.Run(name, func(t *testing.T) {
			status, outcome := runEnd(c.result, c.stop, c.quota)
			if status != c.status || outcome != c.outcome {
				t.Fatalf("runEnd = %s/%s, want %s/%s", status, outcome, c.status, c.outcome)
			}
		})
	}
}

// TestStopOutcome: the run's own timeout and Core's v2 stops timed_out/idle_timeout carry an outcome;
// revokes, other v2 reasons, lost heartbeats and shutdowns do not.
func TestStopOutcome(t *testing.T) {
	timeout, stop := context.WithTimeoutCause(context.Background(), time.Nanosecond, errRunTimeout)
	defer stop()
	<-timeout.Done()
	if o := stopOutcome(timeout); o != "timed_out" {
		t.Fatalf("own timeout: %q", o)
	}
	for reason, want := range map[string]string{"timed_out": "timed_out", "idle_timeout": "idle_timeout", "revoked": "", "terminal": "", "unknown_run": ""} {
		parent, cancel := context.WithCancelCause(context.Background())
		// The executor's own timeout context below a node stop sees the node's cause.
		ctx, stop := context.WithTimeoutCause(parent, time.Hour, errRunTimeout)
		cancel(&node.StopCause{Reason: reason})
		if o := stopOutcome(ctx); o != want {
			t.Errorf("v2 stop %s: %q, want %q", reason, o, want)
		}
		stop()
	}
	plain, cancel := context.WithCancel(context.Background())
	cancel() // v1 heartbeat cancel (owner cancel or Core's idle stop), lost heartbeat, shutdown
	if o := stopOutcome(plain); o != "" {
		t.Fatalf("plain cancel: %q", o)
	}
}

// TestEndError: setup failures and other runs that never reached the runtime carry their outcome.
func TestEndError(t *testing.T) {
	err := setupFailed(fmt.Errorf("worktree: %w", os.ErrPermission))
	var end *endError
	if !errors.As(err, &end) || end.outcome != "setup_failed" || !end.notStarted || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("%#v", err)
	}
	if err = ended(driver.Result{Err: errors.New("app-server disconnected")}, "", false); errors.As(err, &end) {
		t.Fatalf("unknown end became %#v", end)
	}
	if err = ended(driver.Result{Err: errors.New("interrupt result unknown")}, "timed_out", false); !errors.As(err, &end) || end.outcome != "timed_out" || end.notStarted {
		t.Fatalf("%#v", err)
	}
}

// TestJetStreamRunOutcomes runs the executor with the fake driver against Core's store and checks the
// terminal event's status/outcome and the run as Core records it.
func TestJetStreamRunOutcomes(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	load := func(name string) fake.Script {
		sc, err := fake.Load(filepath.Join("testdata", "fake", name))
		if err != nil {
			t.Fatal(err)
		}
		return sc
	}
	short := func(s *protocol.RunSpec) { s.TimeoutSeconds = 1 }
	for name, c := range map[string]struct {
		w                     Worker
		mods                  []any
		event, outcome        string // terminal event status and outcome
		runStatus, runOutcome string // as Core records the run ("" outcome: not checked)
	}{
		"hard timeout":   {Worker{Driver: &fake.Driver{Script: load("hanging.json")}}, []any{short}, "CANCELLED", "timed_out", "FAILED", "timed_out"},
		"v2 idle stop":   {Worker{Driver: &fake.Driver{Script: load("hanging.json")}}, []any{stopCause{&node.StopCause{Reason: "idle_timeout"}}}, "CANCELLED", "idle_timeout", "FAILED", "idle_timeout"},
		"v2 timed_out":   {Worker{Driver: &fake.Driver{Script: load("hanging.json")}}, []any{stopCause{&node.StopCause{Reason: "timed_out"}}}, "CANCELLED", "timed_out", "FAILED", "timed_out"},
		"v2 revoked":     {Worker{Driver: &fake.Driver{Script: load("hanging.json")}}, []any{stopCause{&node.StopCause{Reason: "revoked"}}}, "CANCELLED", "", "CANCELLED", ""},
		"failed":         {Worker{Driver: &fake.Driver{Script: load("failed.json")}}, nil, "FAILED", "failed", "FAILED", "failed"},
		"max turns":      {Worker{Driver: &fake.Driver{Script: load("max_turns.json")}}, nil, "FAILED", "max_turns", "FAILED", "max_turns"},
		"quota":          {Worker{Driver: &fake.Driver{Script: load("quota_failed.json")}}, nil, "FAILED", "paused_out", "FAILED", "paused_out"},
		"disconnected":   {Worker{Driver: &fake.Driver{Script: load("disconnected.json")}}, nil, "FAILED", "failed", "FAILED", "failed"},
		"launch error":   {Worker{Driver: &fake.Driver{Script: fake.Script{LaunchErr: "app-server refused the thread"}}}, nil, "FAILED", "failed", "FAILED", "failed"},
		"missing binary": {Worker{Driver: &fake.Driver{Script: load("completed.json"), MCP: true}, MCPBinary: filepath.Join(t.TempDir(), "no-factory-mcp")}, nil, "FAILED", "setup_failed", "FAILED", "setup_failed"},
	} {
		t.Run(name, func(t *testing.T) {
			r, events := runDriver(t, url, c.w, false, c.mods...)
			var terminal protocol.Event
			if err := json.Unmarshal(events[len(events)-1].Payload, &terminal); err != nil {
				t.Fatal(err)
			}
			if terminal.Status != c.event || terminal.Outcome != c.outcome {
				t.Fatalf("terminal event %s/%s, want %s/%s (%s)", terminal.Status, terminal.Outcome, c.event, c.outcome, terminal.Result)
			}
			if r.Status != c.runStatus || c.runOutcome != "" && r.Outcome != c.runOutcome {
				t.Fatalf("run %s/%s, want %s/%s", r.Status, r.Outcome, c.runStatus, c.runOutcome)
			}
		})
	}
}

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

// TestRunEnd is the executor's mapping of a started run's result to its outcome (reliable-recovery 3.4;
// docs/runtime-protocol-v2.md §8): terminal evidence or a known cause names the outcome, anything else is lost.
func TestRunEnd(t *testing.T) {
	missing := &exec.Error{Name: "codex", Err: exec.ErrNotFound}
	unknown := errors.New("interrupt result unknown")
	for name, c := range map[string]struct {
		result  driver.Result
		stop    string
		quota   bool
		outcome string
	}{
		"completed":                        {driver.Result{Status: "COMPLETED"}, "", false, "succeeded"},
		"completed after timeout":          {driver.Result{Status: "COMPLETED"}, "timed_out", false, "succeeded"},
		"completed after quota":            {driver.Result{Status: "COMPLETED"}, "", true, "succeeded"},
		"completed after shutdown":         {driver.Result{Status: "COMPLETED"}, "lost", false, "succeeded"},
		"failed":                           {driver.Result{Status: "FAILED"}, "", false, "failed"},
		"max turns":                        {driver.Result{Status: "FAILED", Outcome: "max_turns"}, "", false, "max_turns"},
		"auth during turn":                 {driver.Result{Status: "FAILED", Outcome: "paused_out"}, "", false, "paused_out"},
		"quota hit":                        {driver.Result{Status: "FAILED"}, "", true, "paused_out"},
		"failed after timeout interrupt":   {driver.Result{Status: "FAILED"}, "timed_out", false, "timed_out"},
		"failed after revoke":              {driver.Result{Status: "FAILED"}, "revoked", false, "revoked"},
		"failed after shutdown":            {driver.Result{Status: "FAILED"}, "lost", false, "lost"},
		"revoked":                          {driver.Result{Status: "CANCELLED"}, "revoked", false, "revoked"},
		"hard timeout":                     {driver.Result{Status: "CANCELLED"}, "timed_out", false, "timed_out"},
		"idle stop":                        {driver.Result{Status: "CANCELLED"}, "idle_timeout", false, "idle_timeout"},
		"node shutdown":                    {driver.Result{Status: "CANCELLED"}, "lost", false, "lost"},
		"cancelled by the runtime itself":  {driver.Result{Status: "CANCELLED"}, "", false, "failed"},
		"unknown turn status":              {driver.Result{Status: "WEIRD"}, "", false, "lost"},
		"timeout without evidence":         {driver.Result{Err: unknown}, "timed_out", false, "timed_out"},
		"idle stop without evidence":       {driver.Result{Err: unknown}, "idle_timeout", false, "idle_timeout"},
		"revoke without evidence":          {driver.Result{Err: unknown}, "revoked", false, "lost"},
		"shutdown without evidence":        {driver.Result{Err: unknown}, "lost", false, "lost"},
		"app-server disconnect":            {driver.Result{Err: errors.New("app-server disconnected: EOF")}, "", false, "lost"},
		"disconnect after quota":           {driver.Result{Err: errors.New("app-server disconnected: EOF")}, "", true, "lost"},
		"shim exit without result":         {driver.Result{Err: &claude.Error{Class: "no_result", Message: "shim exited"}}, "", false, "lost"},
		"claude auth without result":       {driver.Result{Err: &claude.Error{Class: "auth_error", Message: "authentication_failed"}}, "", false, "paused_out"},
		"claude broken stream":             {driver.Result{Err: &claude.Error{Class: "protocol", Message: "invalid shim line"}}, "", false, "failed"},
		"claude executable":                {driver.Result{Err: &claude.Error{Class: "executable", Message: "no bundled Claude Code"}}, "", false, "paused_out"},
		"runtime binary missing (wrapped)": {driver.Result{Err: fmt.Errorf("launch: %w", missing)}, "", false, "paused_out"},
	} {
		t.Run(name, func(t *testing.T) {
			if outcome := runEnd(c.result, c.stop, c.quota); outcome != c.outcome {
				t.Fatalf("runEnd = %s, want %s", outcome, c.outcome)
			}
		})
	}
}

// TestStopOutcome: the run's own timeout and Core's v2 stops timed_out/idle_timeout carry their outcome,
// Core's other stops (v2 reasons, a v1 heartbeat cancel) are revoked, and a stop Core did not ask for (a
// node shutdown, a lost heartbeat) is lost, never revoked.
func TestStopOutcome(t *testing.T) {
	if o := stopOutcome(context.Background()); o != "" {
		t.Fatalf("not stopped: %q", o)
	}
	timeout, stop := context.WithTimeoutCause(context.Background(), time.Nanosecond, errRunTimeout)
	defer stop()
	<-timeout.Done()
	if o := stopOutcome(timeout); o != "timed_out" {
		t.Fatalf("own timeout: %q", o)
	}
	for reason, want := range map[string]string{"timed_out": "timed_out", "idle_timeout": "idle_timeout", "revoked": "revoked", "terminal": "revoked", "unknown_run": "revoked"} {
		parent, cancel := context.WithCancelCause(context.Background())
		// The executor's own timeout context below a node stop sees the node's cause.
		ctx, stop := context.WithTimeoutCause(parent, time.Hour, errRunTimeout)
		cancel(&node.StopCause{Reason: reason})
		if o := stopOutcome(ctx); o != want {
			t.Errorf("v2 stop %s: %q, want %q", reason, o, want)
		}
		stop()
	}
	for cause, want := range map[error]string{errRevoked: "revoked", errHeartbeatLost: "lost", errExitGrace: "lost"} {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(cause)
		if o := stopOutcome(ctx); o != want {
			t.Errorf("%v: %q, want %q", cause, o, want)
		}
	}
	plain, cancel := context.WithCancel(context.Background())
	cancel() // node shutdown
	if o := stopOutcome(plain); o != "lost" {
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
	if err = endedWithout(driver.Result{Err: errors.New("app-server disconnected")}, "", false); errors.As(err, &end) {
		t.Fatalf("unknown end became %#v", end)
	}
	if err = endedWithout(driver.Result{Err: errors.New("interrupt result unknown")}, "revoked", false); errors.As(err, &end) {
		t.Fatalf("revoke without evidence became %#v", end)
	}
	if err = endedWithout(driver.Result{Err: errors.New("interrupt result unknown")}, "timed_out", false); !errors.As(err, &end) || end.outcome != "timed_out" || end.notStarted {
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
		runStatus, runOutcome string // as Core records the run
	}{
		"completed":      {Worker{Driver: &fake.Driver{Script: load("completed.json")}}, nil, "ENDED", "succeeded", "ENDED", "succeeded"},
		"hard timeout":   {Worker{Driver: &fake.Driver{Script: load("hanging.json")}}, []any{short}, "ENDED", "timed_out", "ENDED", "timed_out"},
		"v2 idle stop":   {Worker{Driver: &fake.Driver{Script: load("hanging.json")}}, []any{stopCause{&node.StopCause{Reason: "idle_timeout"}}}, "ENDED", "idle_timeout", "ENDED", "idle_timeout"},
		"v2 timed_out":   {Worker{Driver: &fake.Driver{Script: load("hanging.json")}}, []any{stopCause{&node.StopCause{Reason: "timed_out"}}}, "ENDED", "timed_out", "ENDED", "timed_out"},
		"v2 revoked":     {Worker{Driver: &fake.Driver{Script: load("hanging.json")}}, []any{stopCause{&node.StopCause{Reason: "revoked"}}}, "ENDED", "revoked", "ENDED", "revoked"},
		"node shutdown":  {Worker{Driver: &fake.Driver{Script: load("hanging.json")}}, []any{stopCause{errors.New("node shutting down")}}, "ENDED", "lost", "ENDED", "lost"},
		"failed":         {Worker{Driver: &fake.Driver{Script: load("failed.json")}}, nil, "ENDED", "failed", "ENDED", "failed"},
		"max turns":      {Worker{Driver: &fake.Driver{Script: load("max_turns.json")}}, nil, "ENDED", "max_turns", "ENDED", "max_turns"},
		"quota":          {Worker{Driver: &fake.Driver{Script: load("quota_failed.json")}}, nil, "ENDED", "paused_out", "ENDED", "paused_out"},
		"disconnected":   {Worker{Driver: &fake.Driver{Script: load("disconnected.json")}}, nil, "ENDED", "lost", "ENDED", "lost"},
		"launch error":   {Worker{Driver: &fake.Driver{Script: fake.Script{LaunchErr: "app-server refused the thread"}}}, nil, "ENDED", "failed", "ENDED", "failed"},
		"missing binary": {Worker{Driver: &fake.Driver{Script: load("completed.json"), MCP: true}, MCPBinary: filepath.Join(t.TempDir(), "no-factory-mcp")}, nil, "ENDED", "setup_failed", "ENDED", "setup_failed"},
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
			if r.Status != c.runStatus || r.Outcome != c.runOutcome {
				t.Fatalf("run %s/%s, want %s/%s", r.Status, r.Outcome, c.runStatus, c.runOutcome)
			}
		})
	}
}

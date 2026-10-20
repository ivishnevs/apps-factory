package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/protocol"
)

func outcomeOf(t *testing.T, s *Store, id string) (status, outcome string) {
	t.Helper()
	if err := s.DB.QueryRow("SELECT status,outcome FROM runs WHERE id=?", id).Scan(&status, &outcome); err != nil {
		t.Fatal(err)
	}
	return
}

func end(t *testing.T, s *Store, id, lease string, p protocol.Event) error {
	t.Helper()
	p.RunID, p.Lease = id, lease
	return s.Apply(context.Background(), DefaultCompany, protocol.Wrap("agent.ended", id, "", p))
}

// TestOutcomes: every way a run ends records exactly one outcome; late terminal evidence replaces `lost`,
// an owner's reconciliation does not.
func TestOutcomes(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	claim := func(id string) string {
		t.Helper()
		queue(t, s, id, "p-"+id, "a-"+id, "")
		l, err := s.Claim(ctx, co, id)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	for _, c := range []struct {
		id      string
		event   protocol.Event
		status  string
		outcome string
	}{
		{"done", protocol.Event{Status: "ENDED", Outcome: "succeeded"}, "ENDED", "succeeded"},
		{"crash", protocol.Event{Status: "ENDED", Outcome: "failed", Result: "codex exited"}, "ENDED", "failed"},
		{"preflight", protocol.Event{Status: "ENDED", Outcome: "setup_failed", Result: "OpenSpec preflight failed before runtime startup: x"}, "ENDED", "setup_failed"},
		{"stopped", protocol.Event{Status: "ENDED", Outcome: "revoked"}, "ENDED", "revoked"},
		{"timeout", protocol.Event{Status: "ENDED", Outcome: "timed_out"}, "ENDED", "timed_out"},
		{"turns", protocol.Event{Status: "ENDED", Outcome: "max_turns"}, "ENDED", "max_turns"},
		{"quota", protocol.Event{Status: "ENDED", Outcome: "paused_out"}, "ENDED", "paused_out"},
		{"no-evidence", protocol.Event{Status: "ENDED", Outcome: "lost", Result: "app-server disconnected: EOF"}, "ENDED", "lost"},
	} {
		if err := end(t, s, c.id, claim(c.id), c.event); err != nil {
			t.Fatal(c.id, err)
		}
		if st, out := outcomeOf(t, s, c.id); st != c.status || out != c.outcome {
			t.Errorf("%s: %s/%s, want %s/%s", c.id, st, out, c.status, c.outcome)
		}
	}
	// Core's outcomes are not the worker's to report, a terminal event names its outcome, and only terminal
	// events carry one; the retired terminal statuses are refused.
	lease := claim("bad")
	for _, p := range []protocol.Event{{Status: "ENDED", Outcome: "no_exit"}, {Status: "ENDED", Outcome: "stale"}, {Status: "ENDED"}, {Outcome: "failed"},
		{Status: "ENDED", Outcome: "nonsense"}, {Status: "FAILED", Outcome: "failed"}, {Status: "COMPLETED"}, {Status: "CANCELLED"}} {
		if err := end(t, s, "bad", lease, p); !errors.Is(err, ErrInvalid) {
			t.Fatal(p, err)
		}
	}
	// Cancelled while QUEUED: revoked.
	queue(t, s, "queued", "p-queued", "a-queued", "")
	if err := s.Cancel(ctx, co, "queued"); err != nil {
		t.Fatal(err)
	}
	if _, out := outcomeOf(t, s, "queued"); out != "revoked" {
		t.Fatal(out)
	}
	// Lease lost, then late terminal evidence: lost becomes the reported outcome.
	late := claim("late")
	lostThenOwner := claim("resolved")
	fake.Advance(LeaseTTL + time.Second)
	if err := s.Expire(ctx, co); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"late", "resolved"} {
		if st, out := outcomeOf(t, s, id); st != "ENDED" || out != "lost" {
			t.Fatal(id, st, out)
		}
	}
	if err := end(t, s, "late", late, protocol.Event{Status: "ENDED", Outcome: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	if st, out := outcomeOf(t, s, "late"); st != "ENDED" || out != "succeeded" {
		t.Fatal(st, out)
	}
	_ = lostThenOwner
	// As a v2 run of a silent node, the lost run still holds its slots until the owner resolves it.
	if _, err := s.DB.Exec("UPDATE runs SET unreleased=1 WHERE id='resolved'"); err != nil {
		t.Fatal(err)
	}
	if err := s.Resolve(ctx, co, "resolved", "continue", "checked the workspace"); err != nil {
		t.Fatal(err)
	}
	if st, out := outcomeOf(t, s, "resolved"); st != "ENDED" || out != "lost" {
		t.Fatal(st, out)
	}
	if r, _ := s.Get(ctx, co, "done"); r.Outcome != "succeeded" {
		t.Fatalf("%+v", r)
	}
}

// TestTimedOutTaskIsNotCancelled: a worker stopping its run on its own timeout reports outcome timed_out; the task retries after the backoff instead of being cancelled.
func TestTimedOutTaskIsNotCancelled(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	task := readyEngineTask(t, s, createEngineTask(t, s, "t1"))
	spec := dispatchEngineTask(t, s, task, "r1")
	lease, err := s.Claim(ctx, DefaultCompany, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, spec.ID, lease, protocol.Event{Status: "ENDED", Outcome: "timed_out", Result: "hard timeout"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Task(ctx, DefaultCompany, "t1")
	if got.Status != "READY" || got.AttemptsNoProgress != 1 || got.NotBefore == 0 {
		t.Fatalf("%+v", got)
	}
}

// TestIdleRunIsStopped: a started run without events for IdleTimeout gets a stop (v1: cancel in the
// heartbeat answer); its revoked end (or one without evidence) is idle_timeout and the task retries instead of
// being cancelled. Events keep a run active; a fresh continuity window (host sleep) delays the detector.
func TestIdleRunIsStopped(t *testing.T) {
	s := engineStore(t)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	ctx := context.Background()
	co := DefaultCompany
	task := readyEngineTask(t, s, createEngineTask(t, s, "t1"))
	spec := dispatchEngineTask(t, s, task, "r1")
	lease, err := s.Claim(ctx, co, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	idleFor := func(d time.Duration, progress bool) bool {
		t.Helper()
		var cancel bool
		for step := time.Duration(0); step < d; step += 10 * time.Second {
			fake.Advance(10 * time.Second)
			if cancel, err = s.Heartbeat(ctx, co, spec.ID, lease); err != nil {
				t.Fatal(err)
			}
			if progress {
				if err = end(t, s, spec.ID, lease, protocol.Event{Method: "item/completed"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.StopIdle(ctx, co); err != nil {
				t.Fatal(err)
			}
		}
		cancel, _ = s.Heartbeat(ctx, co, spec.ID, lease)
		return cancel
	}
	if idleFor(IdleTimeout+time.Minute, true) {
		t.Fatal("an active run was stopped")
	}
	// The host slept: the detector waits for a full window of continuous ticking.
	s.Continuity = clock.NewContinuity(3 * time.Second)
	fake.Advance(2 * time.Hour)
	s.Continuity.Observe(fake.Now())
	if n, _ := s.StopIdle(ctx, co); n != 0 {
		t.Fatal("sleep made the run idle")
	}
	if _, err = s.Heartbeat(ctx, co, spec.ID, lease); err != nil { // the worker beats after the wake
		t.Fatal(err)
	}
	s.Continuity = nil
	if !idleFor(IdleTimeout+20*time.Second, false) {
		t.Fatal("idle run not stopped")
	}
	if err = end(t, s, spec.ID, lease, protocol.Event{Status: "ENDED", Outcome: "revoked"}); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Get(ctx, co, spec.ID)
	if r.Status != "ENDED" || r.Outcome != "idle_timeout" || !strings.HasPrefix(r.Result, "idle_timeout") {
		t.Fatalf("%+v", r)
	}
	if got, _ := s.Task(ctx, co, "t1"); got.Status != "READY" || got.AttemptsNoProgress != 1 || got.NotBefore == 0 {
		t.Fatalf("%+v", got)
	}
}

// TestIdleStopV2: a v2 run's idle stop arrives as a heartbeat stop with reason idle_timeout.
func TestIdleStopV2(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	register(t, s, "n1", 1)
	queue(t, s, "r", "p", "dev", "")
	c, err := claimV2(s, "r", "n1", 1, "1-0-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRun(ctx, DefaultCompany, "r", c.Lease); err != nil {
		t.Fatal(err)
	}
	var a HeartbeatAnswer
	for elapsed := time.Duration(0); elapsed <= IdleTimeout+20*time.Second; elapsed += 10 * time.Second {
		fake.Advance(10 * time.Second)
		if _, err = s.StopIdle(ctx, DefaultCompany); err != nil {
			t.Fatal(err)
		}
		if a, err = s.NodeHeartbeat(ctx, DefaultCompany, "n1", NodeHeartbeat{Epoch: 1, Inventory: []InventoryRun{{RunID: "r", State: "running", ElapsedMS: elapsed.Milliseconds()}}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.Stop) != 1 || a.Stop[0].Reason != "idle_timeout" {
		t.Fatal(a)
	}
}

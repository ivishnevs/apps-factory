package storage

import (
	"context"
	"errors"
	"path/filepath"
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
		{"done", protocol.Event{Status: "COMPLETED"}, "COMPLETED", "succeeded"},
		{"crash", protocol.Event{Status: "FAILED", Result: "codex exited"}, "FAILED", "failed"},
		{"preflight", protocol.Event{Status: "FAILED", Result: "OpenSpec preflight failed before runtime startup: x"}, "FAILED", "setup_failed"},
		{"diverged", protocol.Event{Status: "FAILED", Result: "workspace_diverged: task t"}, "FAILED", "setup_failed"},
		{"stopped", protocol.Event{Status: "CANCELLED"}, "CANCELLED", "revoked"},
		{"timeout", protocol.Event{Status: "CANCELLED", Outcome: "timed_out"}, "FAILED", "timed_out"},
		{"turns", protocol.Event{Status: "FAILED", Outcome: "max_turns"}, "FAILED", "max_turns"},
		{"quota", protocol.Event{Status: "FAILED", Outcome: "paused_out"}, "FAILED", "paused_out"},
	} {
		if err := end(t, s, c.id, claim(c.id), c.event); err != nil {
			t.Fatal(c.id, err)
		}
		if st, out := outcomeOf(t, s, c.id); st != c.status || out != c.outcome {
			t.Errorf("%s: %s/%s, want %s/%s", c.id, st, out, c.status, c.outcome)
		}
	}
	// Core's outcomes are not the worker's to report, and only terminal events carry one.
	lease := claim("bad")
	for _, p := range []protocol.Event{{Status: "FAILED", Outcome: "lost"}, {Outcome: "failed"}, {Status: "FAILED", Outcome: "nonsense"}} {
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
		if st, out := outcomeOf(t, s, id); st != "UNKNOWN" || out != "lost" {
			t.Fatal(id, st, out)
		}
	}
	if err := end(t, s, "late", late, protocol.Event{Status: "COMPLETED"}); err != nil {
		t.Fatal(err)
	}
	if st, out := outcomeOf(t, s, "late"); st != "COMPLETED" || out != "succeeded" {
		t.Fatal(st, out)
	}
	_ = lostThenOwner
	if err := s.Resolve(ctx, co, "resolved", "COMPLETED", "checked the workspace"); err != nil {
		t.Fatal(err)
	}
	if st, out := outcomeOf(t, s, "resolved"); st != "COMPLETED" || out != "lost" {
		t.Fatal(st, out)
	}
	if r, _ := s.Get(ctx, co, "done"); r.Outcome != "succeeded" {
		t.Fatalf("%+v", r)
	}
}

// TestTimedOutTaskIsNotCancelled: a worker stopping its run on its own timeout reports CANCELLED with
// outcome timed_out; the task fails (retry-eligible) instead of being cancelled.
func TestTimedOutTaskIsNotCancelled(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	task := readyEngineTask(t, s, createEngineTask(t, s, "t1"))
	spec := dispatchEngineTask(t, s, task, "r1")
	lease, err := s.Claim(ctx, DefaultCompany, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, spec.ID, lease, protocol.Event{Status: "CANCELLED", Outcome: "timed_out", Result: "hard timeout"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Task(ctx, DefaultCompany, "t1")
	if got.Status != "FAILED" || !got.RetryEligible {
		t.Fatalf("%+v", got)
	}
}

// TestIdleRunIsStopped: a started run without events for IdleTimeout gets a stop (v1: cancel in the
// heartbeat answer); its CANCELLED end becomes FAILED idle_timeout and the task fails instead of being
// cancelled. Events keep a run active; a fresh continuity window (host sleep) delays the detector.
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
	if err = end(t, s, spec.ID, lease, protocol.Event{Status: "CANCELLED"}); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Get(ctx, co, spec.ID)
	if r.Status != "FAILED" || r.Outcome != "idle_timeout" || !strings.HasPrefix(r.Result, "idle_timeout") {
		t.Fatalf("%+v", r)
	}
	if got, _ := s.Task(ctx, co, "t1"); got.Status != "FAILED" || !got.RetryEligible {
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

// TestOutcomeBackfill: runs that ended before outcomes existed get one from their status.
func TestOutcomeBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	queue(t, s, "old-done", "p1", "a1", "")
	queue(t, s, "old-lost", "p2", "a2", "")
	queue(t, s, "live", "p3", "a3", "")
	rollbackTo(t, s.DB, 15)
	if _, err = s.DB.Exec("UPDATE runs SET status='COMPLETED' WHERE id='old-done'; UPDATE runs SET status='UNKNOWN' WHERE id='old-lost'"); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	for id, want := range map[string]string{"old-done": "succeeded", "old-lost": "lost", "live": ""} {
		if _, out := outcomeOf(t, s, id); out != want {
			t.Errorf("%s: %q, want %q", id, out, want)
		}
	}
	_ = ctx
}

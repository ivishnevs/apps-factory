package runner

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"appfactory/internal/driver"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
)

// fakeClock is the auth hold-off's clock in tests.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// TestRunAuthFailureDisablesDriver (runtime-drivers 3.2): a run ending on the runtime's authentication failure
// disables that driver only, like a failing probe, without waiting for the next probe; a failing probe keeps
// it disabled; the first passing probe after the hold-off re-enables it.
func TestRunAuthFailureDisablesDriver(t *testing.T) {
	codex, claude := newFlip(protocol.RuntimeCodex), newFlip(protocol.RuntimeClaude)
	h := newDriverHealth([]driver.Driver{codex, claude}, node.Defaults().Probes)
	clock := &fakeClock{t: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)}
	h.now = clock.now
	var changes atomic.Int32
	h.changed = func() { changes.Add(1) }
	ctx := context.Background()
	h.check(ctx, true)

	h.authFailed("claude", errors.New("claude auth_error: authentication_failed"))
	if h.usable("claude") || !h.usable("codex") || !h.anyUsable() || changes.Load() != 1 {
		t.Fatalf("after auth failure: %+v, changes %d", h.infos(), changes.Load())
	}
	if got := h.infos(); got[1].OK || !got[0].OK || got[1].Until != clock.t.Add(authHoldOffs[0]).Unix() || got[0].Until != 0 {
		t.Fatalf("register drivers (with the hold-off end): %+v", got)
	}
	h.authFailed("claude", errors.New("again")) // already disabled: no second change
	if changes.Load() != 1 {
		t.Fatalf("changes %d", changes.Load())
	}
	h.authFailed("gemini", errors.New("unknown driver")) // ignored

	claude.probeFails.Store(true)
	if h.check(ctx, false) || h.usable("claude") {
		t.Fatal("a failing probe re-enabled the driver")
	}
	claude.probeFails.Store(false)
	if h.check(ctx, false) || h.usable("claude") {
		t.Fatal("a passing probe re-enabled the driver during its hold-off")
	}
	clock.advance(10 * time.Minute)
	if !h.check(ctx, false) || !h.usable("claude") {
		t.Fatalf("passing probe after the hold-off: %+v", h.infos())
	}

	h.authFailed("codex", errors.New("ChatGPT login required"))
	h.authFailed("claude", errors.New("logged out"))
	if h.anyUsable() {
		t.Fatal("both drivers failed auth: the node must claim nothing")
	}
	clock.advance(30 * time.Minute) // claude's second hold-off; codex's first
	if !h.check(ctx, false) || !h.usable("codex") || !h.usable("claude") {
		t.Fatalf("probes passing: %+v", h.infos())
	}
}

// TestAuthHoldOffEscalates: repeated auth failures hold the driver off 10 min, 30 min, then 2 h (cap) before a
// passing probe re-enables it; failures of runs in flight while it is disabled do not escalate; a successful
// run, or the cap period without an auth failure, starts the escalation over.
func TestAuthHoldOffEscalates(t *testing.T) {
	d := newFlip(protocol.RuntimeClaude)
	h := newDriverHealth([]driver.Driver{d}, node.Defaults().Probes)
	clock := &fakeClock{t: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)}
	h.now = clock.now
	ctx := context.Background()
	h.check(ctx, true)
	// heldFor fails a run's auth and reports how long passing probes (every minute) keep the driver disabled.
	heldFor := func() time.Duration {
		t.Helper()
		h.authFailed("claude", errors.New("logged out"))
		start := clock.t
		for h.usable("claude") || !h.check(ctx, false) {
			if clock.t.Sub(start) > 3*time.Hour {
				t.Fatal("never re-enabled")
			}
			clock.advance(time.Minute)
		}
		return clock.t.Sub(start)
	}
	for i, want := range []time.Duration{10 * time.Minute, 30 * time.Minute, 2 * time.Hour, 2 * time.Hour} {
		if got := heldFor(); got != want {
			t.Fatalf("failure %d held %s, want %s", i+1, got, want)
		}
	}
	h.authFailed("claude", errors.New("logged out"))
	h.authFailed("claude", errors.New("in flight")) // already disabled: no escalation
	h.runSucceeded("claude")                        // a run that completed while disabled does not reset
	clock.advance(2 * time.Hour)
	if !h.check(ctx, false) || !h.usable("claude") {
		t.Fatal("cap hold-off did not end")
	}
	h.runSucceeded("claude")
	if got := heldFor(); got != 10*time.Minute {
		t.Fatalf("after a successful run held %s, want 10m", got)
	}
	if got := heldFor(); got != 30*time.Minute {
		t.Fatalf("second failure held %s, want 30m", got)
	}
	clock.advance(2 * time.Hour) // the cap period without an auth failure
	if got := heldFor(); got != 10*time.Minute {
		t.Fatalf("after the cap period held %s, want 10m", got)
	}
}

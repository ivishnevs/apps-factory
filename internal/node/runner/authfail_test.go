package runner

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"appfactory/internal/driver"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
)

// TestRunAuthFailureDisablesDriver (runtime-drivers 3.2): a run ending on the runtime's authentication failure
// disables that driver only, like a failing probe, without waiting for the next probe; a failing probe keeps
// it disabled; the next passing probe re-enables it.
func TestRunAuthFailureDisablesDriver(t *testing.T) {
	codex, claude := newFlip(protocol.RuntimeCodex), newFlip(protocol.RuntimeClaude)
	h := newDriverHealth([]driver.Driver{codex, claude}, node.Defaults().Probes)
	var changes atomic.Int32
	h.changed = func() { changes.Add(1) }
	ctx := context.Background()
	h.check(ctx, true)

	h.authFailed("claude", errors.New("claude auth_error: authentication_failed"))
	if h.usable("claude") || !h.usable("codex") || !h.anyUsable() || changes.Load() != 1 {
		t.Fatalf("after auth failure: %+v, changes %d", h.infos(), changes.Load())
	}
	if got := h.infos(); got[1].OK || !got[0].OK {
		t.Fatalf("register drivers: %+v", got)
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
	if !h.check(ctx, false) || !h.usable("claude") {
		t.Fatalf("passing probe: %+v", h.infos())
	}

	h.authFailed("codex", errors.New("ChatGPT login required"))
	h.authFailed("claude", errors.New("logged out"))
	if h.anyUsable() {
		t.Fatal("both drivers failed auth: the node must claim nothing")
	}
	if !h.check(ctx, false) || !h.usable("codex") || !h.usable("claude") {
		t.Fatalf("probes passing: %+v", h.infos())
	}
}

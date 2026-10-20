package runner

import (
	"context"
	"sync"
	"testing"
	"time"

	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
)

// scriptDriver is a fake driver whose probe answers come from a script: "ok", "timeout" (transient, as the
// probe killed by its timeout) or "auth" (definitive); the last answer repeats. It records the command
// timeout each attempt ran under.
type scriptDriver struct {
	*fake.Driver
	mu       sync.Mutex
	script   []string
	timeouts []time.Duration
}

func (d *scriptDriver) Probe(ctx context.Context) []node.CheckResult {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.timeouts = append(d.timeouts, node.CommandTimeoutOf(ctx))
	a := d.script[0]
	if len(d.script) > 1 {
		d.script = d.script[1:]
	}
	switch a {
	case "timeout":
		return []node.CheckResult{{Check: "claude.version", Status: node.Fail, Transient: true,
			Detail: "claude probe: unexpected end of JSON input (signal: killed)"}}
	case "auth":
		return []node.CheckResult{{Check: "claude.auth", Status: node.Fail, Detail: "not logged in"}}
	}
	return []node.CheckResult{{Check: "claude.auth", Status: node.Pass}}
}

func (d *scriptDriver) set(script ...string) {
	d.mu.Lock()
	d.script, d.timeouts = script, nil
	d.mu.Unlock()
}

func (d *scriptDriver) attempts() []time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]time.Duration(nil), d.timeouts...)
}

func newScripted(t *testing.T) (*scriptDriver, *driverHealth) {
	t.Helper()
	d := &scriptDriver{Driver: &fake.Driver{Provider: protocol.RuntimeClaude}, script: []string{"ok"}}
	p := node.Defaults().Probes
	p.RetryBackoff = time.Millisecond
	h := newDriverHealth([]driver.Driver{d}, p)
	if h.check(context.Background(), true) || !h.usable("claude") {
		t.Fatal("passes at start")
	}
	return d, h
}

// TestTransientProbeRetriesWithLongerTimeout: a probe killed once by its timeout (the live-run failure under
// load) is retried with the command timeout doubled and, passing then, leaves the driver enabled.
func TestTransientProbeRetriesWithLongerTimeout(t *testing.T) {
	d, h := newScripted(t)
	d.set("timeout", "ok")
	if h.check(context.Background(), false) || !h.usable("claude") {
		t.Fatalf("one timeout disabled the driver: %+v", h.infos())
	}
	if got := d.attempts(); len(got) != 2 || got[0] != node.CommandTimeout || got[1] != 2*node.CommandTimeout {
		t.Fatalf("attempt timeouts %v", got)
	}
}

// TestTransientProbeDisablesOnlyWhenRepeated: a check whose every attempt times out leaves the driver
// enabled (a warning); the third such check in a row (probes.transient_failures) disables it; a pass in
// between starts the count over; a probe that passes again re-enables it.
func TestTransientProbeDisablesOnlyWhenRepeated(t *testing.T) {
	d, h := newScripted(t)
	ctx := context.Background()
	d.set("timeout")
	if h.check(ctx, false) || !h.usable("claude") {
		t.Fatalf("first transient check disabled: %+v", h.infos())
	}
	if got := d.attempts(); len(got) != 3 || got[2] != 4*node.CommandTimeout {
		t.Fatalf("attempt timeouts %v", got)
	}
	if h.check(ctx, false) || !h.usable("claude") {
		t.Fatal("second transient check disabled")
	}
	if got := h.infos()[0]; got.TransientFailures != 2 || !got.OK {
		t.Fatalf("transient failures reported: %+v", got)
	}
	d.set("ok")
	h.check(ctx, false)
	d.set("timeout")
	h.check(ctx, false)
	if h.check(ctx, false) || !h.usable("claude") {
		t.Fatal("a pass did not restart the count")
	}
	if !h.check(ctx, false) || h.usable("claude") {
		t.Fatalf("third transient check in a row kept the driver: %+v", h.infos())
	}
	if got := h.infos()[0]; got.Check != "probe" || got.Detail == "" {
		t.Fatalf("%+v", got)
	}
	if h.check(ctx, false) { // still failing: no new flip (one event per episode)
		t.Fatal("repeated failure flipped again")
	}
	d.set("ok")
	if !h.check(ctx, false) || !h.usable("claude") {
		t.Fatal("pass did not re-enable")
	}
}

// TestDefinitiveProbeDisablesAtOnce: not logged in disables the driver at the first check, without retries.
func TestDefinitiveProbeDisablesAtOnce(t *testing.T) {
	d, h := newScripted(t)
	d.set("auth")
	if !h.check(context.Background(), false) || h.usable("claude") {
		t.Fatalf("definitive failure kept the driver: %+v", h.infos())
	}
	if got := d.attempts(); len(got) != 1 {
		t.Fatalf("definitive failure retried: %v", got)
	}
}

// TestRecentRunKeepsDriverOnTransientProbe: a run that succeeded, or produced runtime activity, since the
// last check is evidence of health: transient probe failures then never disable the driver, however many;
// without new evidence the count starts over.
func TestRecentRunKeepsDriverOnTransientProbe(t *testing.T) {
	d, h := newScripted(t)
	ctx := context.Background()
	d.set("timeout")
	for i := range 5 {
		if i%2 == 0 {
			h.runSucceeded("claude")
		} else {
			h.activity("claude")
		}
		if h.check(ctx, false) || !h.usable("claude") {
			t.Fatalf("check %d with a recent run disabled the driver", i)
		}
	}
	h.check(ctx, false)
	h.check(ctx, false)
	if !h.check(ctx, false) || h.usable("claude") {
		t.Fatal("three transient checks without evidence kept the driver")
	}
	// Evidence never hides a definitive failure.
	d2, h2 := newScripted(t)
	d2.set("auth")
	h2.runSucceeded("claude")
	if !h2.check(ctx, false) || h2.usable("claude") {
		t.Fatal("definitive failure with a recent run kept the driver")
	}
}

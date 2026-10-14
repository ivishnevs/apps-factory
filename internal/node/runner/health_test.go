package runner

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
)

// flipDriver is a fake driver whose probe and version check fail on demand.
type flipDriver struct {
	*fake.Driver
	probeFails, versionFails atomic.Bool
	probes                   atomic.Int32
}

func newFlip(p protocol.RuntimeProvider) *flipDriver {
	return &flipDriver{Driver: &fake.Driver{Provider: p}}
}

func (d *flipDriver) Probe(context.Context) []node.CheckResult {
	d.probes.Add(1)
	if d.probeFails.Load() {
		return []node.CheckResult{{Check: string(d.Name()) + ".auth", Status: node.Fail, Detail: "not logged in"}}
	}
	return []node.CheckResult{{Check: string(d.Name()) + ".auth", Status: node.Pass}}
}

func (d *flipDriver) CheckVersion(context.Context) (string, error) {
	if d.versionFails.Load() {
		return "9.9.9", errors.New("unsupported version 9.9.9")
	}
	return "1.0.0", nil
}

// TestDriverHealthDisablesOnlyFailingDriver: a failing probe disables that driver only, a failing version
// check too; the node has no usable driver only when all fail; passing checks re-enable.
func TestDriverHealthDisablesOnlyFailingDriver(t *testing.T) {
	codex, claude := newFlip(protocol.RuntimeCodex), newFlip(protocol.RuntimeClaude)
	h := newDriverHealth([]driver.Driver{codex, claude}, node.Defaults().Probes)
	ctx := context.Background()
	if h.check(ctx, true) || !h.usable("codex") || !h.usable("claude") {
		t.Fatal("all pass at start")
	}
	claude.probeFails.Store(true)
	if !h.check(ctx, false) || !h.usable("codex") || h.usable("claude") || !h.anyUsable() {
		t.Fatalf("claude probe failing: %+v", h.infos())
	}
	if h.check(ctx, false) { // no change, no flip
		t.Fatal("unchanged check reported a flip")
	}
	codex.versionFails.Store(true)
	if h.check(ctx, false) || !h.usable("codex") { // a probe tick does not re-run the version check
		t.Fatal("probe tick changed the version state")
	}
	if !h.check(ctx, true) || h.usable("codex") || h.anyUsable() {
		t.Fatalf("both failing: %+v", h.infos())
	}
	if got := h.infos(); got[0] != (DriverInfo{Name: "codex", Version: "9.9.9", OK: false}) || got[1].OK {
		t.Fatalf("%+v", got)
	}
	codex.versionFails.Store(false)
	claude.probeFails.Store(false)
	if !h.check(ctx, true) || !h.usable("codex") || !h.usable("claude") {
		t.Fatalf("re-enable: %+v", h.infos())
	}
}

// TestDriverHealthRunsOnIntervals: the configured intervals drive the checks and a flip calls changed.
func TestDriverHealthRunsOnIntervals(t *testing.T) {
	d := newFlip(protocol.RuntimeCodex)
	h := newDriverHealth([]driver.Driver{d}, node.Probes{Interval: 5 * time.Millisecond, VersionInterval: time.Hour})
	var changes atomic.Int32
	h.changed = func() { changes.Add(1) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.run(ctx)
	eventually(t, "periodic probes", func() bool { return d.probes.Load() >= 2 })
	d.probeFails.Store(true)
	eventually(t, "driver disabled", func() bool { return !h.usable("codex") && changes.Load() == 1 })
	d.probeFails.Store(false)
	eventually(t, "driver re-enabled", func() bool { return h.usable("codex") && changes.Load() == 2 })
}

// TestPoolSkipsDisabledSource: slots stop fetching a disabled driver's subject and keep claiming the other.
func TestPoolSkipsDisabledSource(t *testing.T) {
	codexQ, claudeQ := &queue{}, &queue{}
	codexQ.push("c1", "c2")
	claudeQ.push("k1")
	runs := &fakeRuns{running: map[string]bool{}, release: make(chan struct{})}
	close(runs.release)
	var claudeOK atomic.Bool
	p := &Pool{Slots: 1, Pause: time.Millisecond, Sources: []*Source{
		{Name: "codex", Fetch: codexQ.fetch, Process: runs.process},
		{Name: "claude", Fetch: claudeQ.fetch, Process: runs.process, Claimable: claudeOK.Load},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	ended := func(n int) func() bool {
		return func() bool { runs.mu.Lock(); defer runs.mu.Unlock(); return len(runs.ended) == n }
	}
	eventually(t, "codex runs", ended(2))
	time.Sleep(20 * time.Millisecond)
	if !ended(2)() {
		t.Fatal("disabled driver's command was claimed")
	}
	claudeOK.Store(true)
	eventually(t, "claude run after re-enable", ended(3))
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestRunNeedsAUsableDriver: a node whose drivers all fail at start does not start.
func TestRunNeedsAUsableDriver(t *testing.T) {
	d := newFlip(protocol.RuntimeCodex)
	d.probeFails.Store(true)
	n := Node{Config: node.Defaults(), Drivers: []driver.Driver{d}}
	n.Config.StateDir = t.TempDir()
	if err := Run(context.Background(), n); !errors.Is(err, ErrNoDriver) {
		t.Fatal(err)
	}
}

// TestV2DeclaresDriverChanges: a disabled driver is declared with ok:false by registering a new epoch once the
// node holds no run; a busy node keeps the change local; re-enabling is declared the same way, and a driver
// that flipped back before the node was idle causes no register.
func TestV2DeclaresDriverChanges(t *testing.T) {
	dir := t.TempDir()
	f, api := newFakeCore(t, dir)
	ctx := context.Background()
	codex, claude := newFlip(protocol.RuntimeCodex), newFlip(protocol.RuntimeClaude)
	n := v2Node(t, api, dir)
	n.Drivers = []driver.Driver{codex, claude}
	s, err := newSession(ctx, n, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := s.drivers
	h.changed = func() { s.redeclare.Store(true) }
	flip := func() {
		if h.check(ctx, true) {
			h.changed()
		}
	}
	registers := func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.registers) }

	s.inv.Add("r1", s.inv.Epoch(), func() {})
	claude.probeFails.Store(true)
	flip()
	if err = s.declareDrivers(ctx); err != nil || registers() != 1 || !s.claimable() {
		t.Fatal("busy node re-registered", err, registers())
	}
	s.inv.End("r1")
	if err = s.declareDrivers(ctx); err != nil || registers() != 1 {
		t.Fatal("unacked run: re-registered", err)
	}
	s.inv.Ack([]string{"r1"})
	if err = s.declareDrivers(ctx); err != nil || registers() != 2 {
		t.Fatal("idle node did not re-register", err, registers())
	}
	r := f.registers[1]
	if r.Epoch != 2 || len(r.Drivers) != 2 || !r.Drivers[0].OK || r.Drivers[1] != (DriverInfo{Name: "claude", Version: "1.0.0", OK: false}) {
		t.Fatalf("%+v", r)
	}
	if err = s.declareDrivers(ctx); err != nil || registers() != 2 {
		t.Fatal("declared twice", err)
	}

	claude.probeFails.Store(false)
	flip()
	if err = s.declareDrivers(ctx); err != nil || registers() != 3 || !f.registers[2].Drivers[1].OK || f.registers[2].Epoch != 3 {
		t.Fatal("re-enable not declared", err, f.registers)
	}

	codex.probeFails.Store(true) // flips and flips back while busy
	flip()
	s.busy = func() int { return 1 }
	_ = s.declareDrivers(ctx)
	codex.probeFails.Store(false)
	flip()
	s.busy = func() int { return 0 }
	if err = s.declareDrivers(ctx); err != nil || registers() != 3 || s.redeclare.Load() {
		t.Fatal("unchanged drivers re-registered", err, registers())
	}
}

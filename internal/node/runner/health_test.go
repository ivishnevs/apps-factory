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
	if got := h.infos(); got[0] != (DriverInfo{Name: "codex", Version: "9.9.9", OK: false, Check: "version", Detail: "unsupported version 9.9.9"}) || got[1].OK || got[1].Check != "probe" {
		t.Fatalf("%+v", got)
	}
	codex.versionFails.Store(false)
	claude.probeFails.Store(false)
	if !h.check(ctx, true) || !h.usable("codex") || !h.usable("claude") {
		t.Fatalf("re-enable: %+v", h.infos())
	}
	if got := h.infos(); got[0].Check != "" || got[1].Check != "" {
		t.Fatalf("%+v", got)
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

// TestV2DeclaresDriverChanges: a disabled or re-enabled driver reaches Core in the next heartbeat's drivers
// (with the failing check), without a new epoch and also while the node holds a run; a flip asks for a
// heartbeat at once.
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
	h.changed = s.driversChanged
	flip := func() {
		t.Helper()
		if !h.check(ctx, true) {
			t.Fatal("no flip")
		}
		h.changed()
		select {
		case <-s.kick:
		default:
			t.Fatal("a driver flip did not ask for a heartbeat")
		}
	}
	declared := func() []DriverInfo {
		t.Helper()
		if err := s.beat(ctx); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.registers) != 1 {
			t.Fatal("re-registered", len(f.registers))
		}
		b := f.beats[len(f.beats)-1]
		if b.Epoch != 1 || len(b.Drivers) != 2 {
			t.Fatalf("%+v", b)
		}
		return b.Drivers
	}

	s.inv.Add("r1", s.inv.Epoch(), func() {}) // busy: declared all the same
	claude.probeFails.Store(true)
	flip()
	d := declared()
	if !d[0].OK || d[1].OK || d[1].Check != "probe" || d[1].Detail == "" || d[1].Name != "claude" {
		t.Fatalf("%+v", d)
	}
	if !s.claimable() {
		t.Fatal("a driver change paused claiming")
	}
	claude.probeFails.Store(false)
	flip()
	if d = declared(); !d[1].OK || d[1].Check != "" {
		t.Fatalf("%+v", d)
	}
}

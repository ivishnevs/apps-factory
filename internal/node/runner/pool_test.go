package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"appfactory/internal/worker"
)

// fakeRuns processes an offer as a run that lasts until released or cancelled; refuse lists runs whose claim it
// refuses (once each).
type fakeRuns struct {
	mu      sync.Mutex
	running map[string]bool
	max     int
	release chan struct{}
	ended   []string
	refuse  map[string]bool
	done    func(run string) // called when a run ends (the fake Core unqueues it)
}

func newRuns() *fakeRuns {
	return &fakeRuns{running: map[string]bool{}, release: make(chan struct{}), refuse: map[string]bool{}}
}

func (f *fakeRuns) process(ctx context.Context, run string, running func(string, context.CancelFunc)) bool {
	f.mu.Lock()
	if f.refuse[run] {
		delete(f.refuse, run)
		f.mu.Unlock()
		return true
	}
	f.mu.Unlock()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	running(run, cancel)
	f.mu.Lock()
	f.running[run] = true
	f.max = max(f.max, len(f.running))
	f.mu.Unlock()
	select {
	case <-runCtx.Done():
	case <-f.release:
	}
	f.mu.Lock()
	delete(f.running, run)
	f.ended = append(f.ended, run)
	done := f.done
	f.mu.Unlock()
	if done != nil {
		done(run)
	}
	return false
}

func (f *fakeRuns) endedRuns() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ended...)
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timeout waiting for", what)
}

func busy(p *Pool) int {
	n := 0
	for _, s := range p.Status() {
		if s.Busy {
			n++
		}
	}
	return n
}

func startPool(t *testing.T, p *Pool) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	eventually(t, "slots started", func() bool { return len(p.Status()) == p.Slots+p.ConversationSlots })
	t.Cleanup(cancel)
	return cancel, done
}

// offer hands o to the pool, retrying while the slot loops pick up (an idle slot takes it at once).
func offer(t *testing.T, p *Pool, run string) {
	t.Helper()
	eventually(t, "a slot takes "+run, func() bool { return p.Offer(Offer{RunID: run, Provider: "codex"}) })
}

// TestPoolRunsUpToSlotsConcurrently: idle slots take offers; with every slot busy an offer is refused (the run
// stays queued); a slot that frees takes the next; shutdown cancels running runs and waits for them.
func TestPoolRunsUpToSlotsConcurrently(t *testing.T) {
	f := newRuns()
	p := &Pool{Slots: 3, Sources: []*Source{{Name: "codex", Process: f.process}}}
	cancel, done := startPool(t, p)
	for _, r := range []string{"r1", "r2", "r3"} {
		offer(t, p, r)
	}
	eventually(t, "three busy slots", func() bool { return busy(p) == 3 })
	if p.Offer(Offer{RunID: "r4", Provider: "codex"}) {
		t.Fatal("a fourth run was taken without a free slot")
	}
	if free := p.Free(); free["codex"] != 0 {
		t.Fatal("free with every slot busy:", free)
	}
	f.release <- struct{}{}
	eventually(t, "a slot freed", func() bool { return busy(p) == 2 })
	offer(t, p, "r4")
	eventually(t, "fourth run", func() bool { return busy(p) == 3 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if f.max != 3 || len(f.endedRuns()) != 4 || busy(p) != 0 {
		t.Fatalf("max %d ended %v", f.max, f.endedRuns())
	}
}

// TestPoolFree: Free counts idle slots for each driver the node may claim with, none while the node may not claim;
// an offer of a driver that stopped claiming is not run.
func TestPoolFree(t *testing.T) {
	f := newRuns()
	var claimable, claudeOK atomic.Bool
	claimable.Store(true)
	p := &Pool{Slots: 2, Claimable: claimable.Load, Sources: []*Source{{Name: "codex", Process: f.process}, {Name: "claude", Process: f.process, Claimable: claudeOK.Load}}}
	if free := p.Free(); len(free) != 0 {
		t.Fatal("free before the slots run:", free)
	}
	startPool(t, p)
	if free := p.Free(); free["codex"] != 2 || len(free) != 1 {
		t.Fatal(free)
	}
	claudeOK.Store(true)
	if free := p.Free(); free["codex"] != 2 || free["claude"] != 2 {
		t.Fatal(free)
	}
	offer(t, p, "r1")
	eventually(t, "busy", func() bool { return busy(p) == 1 })
	if free := p.Free(); free["codex"] != 1 || free["claude"] != 1 {
		t.Fatal(free)
	}
	claimable.Store(false)
	if free := p.Free(); len(free) != 0 {
		t.Fatal("free while paused:", free)
	}
	// An offer that arrives after the node stopped claiming is not run: it stays queued at Core.
	if p.Offer(Offer{RunID: "late", Provider: "codex"}) {
		time.Sleep(50 * time.Millisecond)
	}
	claudeOK.Store(false)
	claimable.Store(true)
	if p.Offer(Offer{RunID: "claude-late", Provider: "claude"}) {
		time.Sleep(50 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running["late"] || f.running["claude-late"] || len(f.running) != 1 {
		t.Fatal("ran an offer it may not claim:", f.running)
	}
}

// TestPoolReportsRefusals: a claim Core refused for now goes to Refused and frees the slot.
func TestPoolReportsRefusals(t *testing.T) {
	f := newRuns()
	f.refuse["r1"] = true
	var mu sync.Mutex
	var refused []string
	p := &Pool{Slots: 1, Sources: []*Source{{Name: "codex", Process: f.process}}, Refused: func(run string) { mu.Lock(); refused = append(refused, run); mu.Unlock() }}
	startPool(t, p)
	offer(t, p, "r1")
	eventually(t, "refusal reported", func() bool { mu.Lock(); defer mu.Unlock(); return len(refused) == 1 && refused[0] == "r1" })
	eventually(t, "slot free", func() bool { return busy(p) == 0 })
}

func TestPoolCancelAndRestart(t *testing.T) {
	f := newRuns()
	p := &Pool{Slots: 2, Sources: []*Source{{Name: "codex", Process: f.process}}}
	startPool(t, p)
	offer(t, p, "r1")
	eventually(t, "busy", func() bool { return busy(p) == 1 })
	p.Cancel("r1")
	eventually(t, "cancelled run frees its slot", func() bool { return busy(p) == 0 })
	p.Restart(1)
	offer(t, p, "r2")
	eventually(t, "restarted slot claims", func() bool { return busy(p) == 1 })
}

func TestPoolNeedsSlotsAndSources(t *testing.T) {
	if err := (&Pool{Slots: 1}).Run(context.Background()); err == nil {
		t.Fatal("pool without sources ran")
	}
}

// TestPollLoop: offers of the node's poll go to idle slots; a refused run is skipped by the polls for RetryDelay and
// then offered and run; the free slots and epoch reach Core; a failing poll backs off and recovers.
func TestPollLoop(t *testing.T) {
	delay := worker.RetryDelay
	worker.RetryDelay = 300 * time.Millisecond
	defer func() { worker.RetryDelay = delay }()
	dir := t.TempDir()
	f, api := newFakeCore(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := newSession(ctx, v2Node(t, api, dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.Poll.WaitSeconds = 1
	runs := newRuns()
	close(runs.release) // a run ends at once
	runs.refuse["p2"] = true
	runs.done = func(run string) { f.set(func() { f.unqueue(run) }) }
	pool := &Pool{Slots: 2, Sources: []*Source{{Name: "codex", Process: runs.process}}}
	go pool.Run(ctx)
	eventually(t, "pool started", func() bool { return len(pool.Status()) == 2 })
	f.set(func() { f.queue = []string{"p1", "p2"} })
	go s.poll(ctx, pool)
	eventually(t, "both runs", func() bool { e := runs.endedRuns(); return slices.Contains(e, "p1") && slices.Contains(e, "p2") })
	f.set(func() {
		var skipped bool
		for _, p := range f.polls {
			skipped = skipped || slices.Contains(p.Skip, "p2")
			if p.Epoch != 1 || p.WaitSeconds != 1 {
				t.Errorf("poll %+v", p)
			}
		}
		if !skipped {
			t.Error("the refused run was never skipped")
		}
		if f.polls[0].Free["codex"] != 2 {
			t.Errorf("first poll %+v", f.polls[0])
		}
	})
}

// TestPollBacksOff: a failing poll is retried after a growing pause, not in a loop, and work flows once it answers.
func TestPollBacksOff(t *testing.T) {
	dir := t.TempDir()
	f, api := newFakeCore(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := newSession(ctx, v2Node(t, api, dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		f.poll(w, r)
	}))
	defer failing.Close()
	s.api.URL = failing.URL
	s.cfg.Poll.WaitSeconds = 1
	runs := newRuns()
	close(runs.release)
	runs.done = func(run string) { f.set(func() { f.unqueue(run) }) }
	pool := &Pool{Slots: 1, Sources: []*Source{{Name: "codex", Process: runs.process}}}
	go pool.Run(ctx)
	f.set(func() { f.queue = []string{"b1"} })
	start := time.Now()
	go s.poll(ctx, pool)
	eventually(t, "run after recovery", func() bool { return slices.Contains(runs.endedRuns(), "b1") })
	if took := time.Since(start); took < 2*time.Second || calls.Load() < 3 {
		t.Fatalf("recovered after %s and %d polls: no backoff", took, calls.Load())
	}
	if n := calls.Load(); n > 20 {
		t.Fatal("polled in a loop:", n)
	}
}

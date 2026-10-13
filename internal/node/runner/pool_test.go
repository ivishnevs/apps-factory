package runner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// queue is a fake subscription: Fetch hands out queued commands, then times out.
type queue struct {
	mu   sync.Mutex
	msgs []string
}

func (q *queue) push(run ...string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.msgs = append(q.msgs, run...)
}
func (q *queue) fetch() ([]*nats.Msg, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.msgs) == 0 {
		q.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		q.mu.Lock()
		return nil, nats.ErrTimeout
	}
	m := &nats.Msg{Data: []byte(q.msgs[0])}
	q.msgs = q.msgs[1:]
	return []*nats.Msg{m}, nil
}

// fakeRuns processes a command as a run that lasts until released or cancelled.
type fakeRuns struct {
	mu      sync.Mutex
	running map[string]bool
	max     int
	release chan struct{}
	ended   []string
}

func (f *fakeRuns) process(ctx context.Context, m *nats.Msg, running func(string, context.CancelFunc)) error {
	run := string(m.Data)
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
	f.mu.Unlock()
	return nil
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

func TestPoolRunsUpToSlotsConcurrently(t *testing.T) {
	q := &queue{}
	q.push("r1", "r2", "r3", "r4")
	f := &fakeRuns{running: map[string]bool{}, release: make(chan struct{})}
	p := &Pool{Slots: 3, Sources: []*Source{{Name: "codex", Fetch: q.fetch, Process: f.process}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	eventually(t, "three busy slots", func() bool { return busy(p) == 3 })
	time.Sleep(50 * time.Millisecond)
	if q.mu.Lock(); len(q.msgs) != 1 {
		t.Fatal("a fourth run was taken without a free slot")
	}
	q.mu.Unlock()
	f.release <- struct{}{}
	eventually(t, "fourth run", func() bool { q.mu.Lock(); defer q.mu.Unlock(); return len(q.msgs) == 0 && busy(p) == 3 })
	// Shutdown cancels running runs and waits for them.
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if f.max != 3 || len(f.ended) != 4 || busy(p) != 0 {
		t.Fatalf("max %d ended %v", f.max, f.ended)
	}
}

func TestPoolPollsEveryDriver(t *testing.T) {
	codex, claude := &queue{}, &queue{}
	codex.push("codex-run")
	claude.push("claude-run")
	f := &fakeRuns{running: map[string]bool{}, release: make(chan struct{})}
	p := &Pool{Slots: 1, Sources: []*Source{{Name: "codex", Fetch: codex.fetch, Process: f.process}, {Name: "claude", Fetch: claude.fetch, Process: f.process}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	eventually(t, "first run", func() bool { return busy(p) == 1 })
	f.release <- struct{}{}
	eventually(t, "second run", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return len(f.running) == 1 && len(f.ended) == 1 })
	if s := p.Status()[0]; s.Run != "claude-run" {
		t.Fatal(s)
	}
}

func TestPoolPausesWhileNotClaimable(t *testing.T) {
	q := &queue{}
	q.push("r1")
	f := &fakeRuns{running: map[string]bool{}, release: make(chan struct{})}
	var claimable atomic.Bool
	p := &Pool{Slots: 2, Sources: []*Source{{Fetch: q.fetch, Process: f.process}}, Claimable: claimable.Load, Pause: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	time.Sleep(50 * time.Millisecond)
	if busy(p) != 0 {
		t.Fatal("claimed while paused")
	}
	claimable.Store(true)
	eventually(t, "claim after resume", func() bool { return busy(p) == 1 })
}

func TestPoolCancelAndRestart(t *testing.T) {
	q := &queue{}
	q.push("r1")
	f := &fakeRuns{running: map[string]bool{}, release: make(chan struct{})}
	p := &Pool{Slots: 2, Sources: []*Source{{Fetch: q.fetch, Process: f.process}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	eventually(t, "busy", func() bool { return busy(p) == 1 })
	p.Cancel("r1")
	eventually(t, "cancelled run frees its slot", func() bool { return busy(p) == 0 })
	p.Restart(1)
	time.Sleep(50 * time.Millisecond) // the replaced loop finishes its fetch
	q.push("r2")
	eventually(t, "restarted slot claims", func() bool { return busy(p) == 1 })
}

func TestPoolStopsOnSubscriptionFailure(t *testing.T) {
	broken := errors.New("consumer deleted")
	p := &Pool{Slots: 2, Sources: []*Source{{Fetch: func() ([]*nats.Msg, error) { return nil, broken }}}}
	if err := p.Run(context.Background()); !errors.Is(err, broken) {
		t.Fatal(err)
	}
}

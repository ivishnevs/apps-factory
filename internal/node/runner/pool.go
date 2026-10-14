// Package runner hosts the node's executor slots (node-agent D1): N slot goroutines share one
// subscription per driver subject and each runs the existing executor (internal/worker).
package runner

import (
	"context"
	"errors"
	"sync"
	"time"

	"appfactory/internal/node"
	"github.com/nats-io/nats.go"
)

// Source is one driver's run command subscription; its fetches are serialized across slots.
type Source struct {
	Name string
	// Fetch waits a bounded time for at most one command; nats.ErrTimeout means none.
	Fetch func() ([]*nats.Msg, error)
	// Process is worker.Worker.Process.
	Process func(ctx context.Context, m *nats.Msg, running func(run string, cancel context.CancelFunc)) error

	mu sync.Mutex
}

// Pool runs Slots slot loops. A free slot polls the sources in turn while Claimable allows it.
type Pool struct {
	Slots     int
	Sources   []*Source
	Claimable func() bool   // nil always claims
	Pause     time.Duration // wait while not claimable; default 1 s
	Now       func() time.Time

	mu    sync.Mutex
	ctx   context.Context
	fail  func(error)
	wg    sync.WaitGroup
	slots []*slot
}

type slot struct {
	gen    int
	stop   context.CancelFunc
	busy   bool
	run    string
	cancel context.CancelFunc
	beat   time.Time
}

func (p *Pool) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Run starts the slots and returns when ctx ends, after running runs were interrupted, or on the
// first failure of a subscription, which stops every slot.
func (p *Pool) Run(ctx context.Context) error {
	if p.Slots < 1 || len(p.Sources) == 0 {
		return errors.New("pool needs slots and sources")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var once sync.Once
	var failure error
	p.mu.Lock()
	p.ctx = ctx
	p.fail = func(err error) { once.Do(func() { failure = err; cancel() }) }
	p.slots = make([]*slot, p.Slots)
	for i := range p.slots {
		p.slots[i] = &slot{}
		p.start(i)
	}
	p.mu.Unlock()
	<-ctx.Done()
	p.wg.Wait()
	return failure
}

// start launches a new loop generation for slot i; p.mu is held.
func (p *Pool) start(i int) {
	s := p.slots[i]
	if s.stop != nil {
		s.stop()
	}
	s.gen++
	s.beat = p.now()
	ctx, stop := context.WithCancel(p.ctx)
	s.stop = stop
	p.wg.Add(1)
	go func(gen int) {
		defer p.wg.Done()
		defer stop()
		p.loop(node.WithSlot(ctx, i), i, gen)
	}(s.gen)
}

func (p *Pool) loop(ctx context.Context, i, gen int) {
	next := i
	for ctx.Err() == nil {
		if !p.update(i, gen, func(s *slot) { s.beat = p.now() }) {
			return
		}
		if p.Claimable != nil && !p.Claimable() {
			pause := p.Pause
			if pause <= 0 {
				pause = time.Second
			}
			select {
			case <-ctx.Done():
			case <-time.After(pause):
			}
			continue
		}
		src := p.Sources[next%len(p.Sources)]
		next++
		src.mu.Lock()
		msgs, err := src.Fetch()
		src.mu.Unlock()
		if errors.Is(err, nats.ErrTimeout) {
			continue
		}
		if err != nil {
			p.fail(err)
			return
		}
		if ctx.Err() != nil { // stopped or replaced while fetching: hand the command back
			for _, m := range msgs {
				_ = m.Nak()
			}
			return
		}
		for _, m := range msgs {
			err = src.Process(ctx, m, func(run string, cancel context.CancelFunc) {
				p.update(i, gen, func(s *slot) { s.busy, s.run, s.cancel = true, run, cancel })
			})
			p.update(i, gen, func(s *slot) { s.busy, s.run, s.cancel, s.beat = false, "", nil, p.now() })
			if err != nil {
				p.fail(err)
				return
			}
		}
	}
}

// update applies f to slot i if gen is its current loop; a replaced loop gets false and exits.
func (p *Pool) update(i, gen int, f func(*slot)) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.slots[i]
	if s.gen != gen {
		return false
	}
	f(s)
	return true
}

func (p *Pool) Status() []node.Slot {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := make([]node.Slot, len(p.slots))
	for i, s := range p.slots {
		r[i] = node.Slot{Index: i, Busy: s.busy, Run: s.run, Beat: s.beat}
	}
	return r
}

func (p *Pool) Restart(i int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.slots) || p.slots[i].busy || p.ctx.Err() != nil {
		return
	}
	p.start(i)
}

func (p *Pool) Cancel(run string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.slots {
		if s.busy && s.run == run && s.cancel != nil {
			s.cancel()
		}
	}
}

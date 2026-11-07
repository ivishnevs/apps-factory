// Package runner hosts the node's executor slots (node-agent D1): N slot goroutines take the offers of the node's
// poll (http-node-transport D3) and each runs the existing executor (internal/worker) for its provider.
package runner

import (
	"context"
	"errors"
	"sync"
	"time"

	"appfactory/internal/node"
)

// Source is one driver's executor.
type Source struct {
	Name string
	// Process is worker.Worker.ProcessRun: claim the offered run and execute it; true when Core refused the
	// claim for now.
	Process func(ctx context.Context, run string, running func(run string, cancel context.CancelFunc)) bool
	// Claimable is false while the source's driver is disabled on this node; nil always claims.
	Claimable func() bool
}

// Offer is a queued run the node's poll offered.
type Offer struct {
	RunID    string
	Provider string
}

// Pool runs Slots slot loops. An idle slot takes one offer at a time from Offer.
type Pool struct {
	Slots     int
	Sources   []*Source
	Claimable func() bool // nil always claims
	Now       func() time.Time
	// Refused is called for an offer whose claim Core refused for now (the poller skips it a while).
	Refused func(run string)
	// StateDir keeps the local activity log (connector-trust): what ran on this machine, readable without Core.
	StateDir string

	mu     sync.Mutex
	ctx    context.Context
	wg     sync.WaitGroup
	slots  []*slot
	offers chan Offer
	freed  chan struct{}
}

// Freed is signalled when a slot becomes idle, so a poll sent while every slot was busy is redone at once.
func (p *Pool) Freed() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.freed == nil {
		p.freed = make(chan struct{}, 1)
	}
	return p.freed
}

func (p *Pool) signalFreed() {
	select {
	case <-p.Freed():
	default:
	}
	select {
	case p.freed <- struct{}{}:
	default:
	}
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

func (p *Pool) source(provider string) *Source {
	for _, s := range p.Sources {
		if s.Name == provider {
			return s
		}
	}
	return nil
}

// Free is how many offers the pool takes now per provider: its idle slots for each driver it may claim with, none
// while the node may not claim.
func (p *Pool) Free() map[string]int {
	free := map[string]int{}
	if p.Claimable != nil && !p.Claimable() {
		return free
	}
	idle := 0
	for _, s := range p.Status() {
		if !s.Busy {
			idle++
		}
	}
	if idle == 0 {
		return free
	}
	for _, s := range p.Sources {
		if s.Claimable == nil || s.Claimable() {
			free[s.Name] = idle
		}
	}
	return free
}

// Offer hands o to an idle slot; false when none takes it now (the run stays queued and is offered again).
func (p *Pool) Offer(o Offer) bool {
	p.mu.Lock()
	ch := p.offers
	p.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- o:
		return true
	default:
		return false
	}
}

// Run starts the slots and returns when ctx ends, after running runs were interrupted.
func (p *Pool) Run(ctx context.Context) error {
	if p.Slots < 1 || len(p.Sources) == 0 {
		return errors.New("pool needs slots and sources")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p.mu.Lock()
	p.ctx = ctx
	p.offers = make(chan Offer)
	p.slots = make([]*slot, p.Slots)
	for i := range p.slots {
		p.slots[i] = &slot{}
		p.start(i)
	}
	p.mu.Unlock()
	<-ctx.Done()
	p.wg.Wait()
	return nil
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
		p.loop(node.WithSlot(ctx, i), i, gen, p.offers)
	}(s.gen)
}

func (p *Pool) loop(ctx context.Context, i, gen int, offers <-chan Offer) {
	beat := time.NewTicker(time.Second)
	defer beat.Stop()
	for ctx.Err() == nil {
		if !p.update(i, gen, func(s *slot) { s.beat = p.now() }) {
			return
		}
		var o Offer
		select {
		case <-ctx.Done():
			return
		case <-beat.C:
			continue
		case o = <-offers:
		}
		src := p.source(o.Provider)
		if src == nil || src.Claimable != nil && !src.Claimable() || p.Claimable != nil && !p.Claimable() {
			continue // offered just before the driver or the node stopped claiming: it stays queued
		}
		if !p.update(i, gen, func(s *slot) { s.busy, s.run = true, o.RunID }) {
			return
		}
		node.LogActivity(p.StateDir, node.Activity{Event: "run.offered", Run: o.RunID, Runtime: o.Provider})
		refused := src.Process(ctx, o.RunID, func(run string, cancel context.CancelFunc) {
			p.update(i, gen, func(s *slot) { s.run, s.cancel = run, cancel })
		})
		outcome := "ended"
		if refused {
			outcome = "refused"
		}
		node.LogActivity(p.StateDir, node.Activity{Event: "run." + outcome, Run: o.RunID})
		p.update(i, gen, func(s *slot) { s.busy, s.run, s.cancel, s.beat = false, "", nil, p.now() })
		if refused && p.Refused != nil {
			p.Refused(o.RunID)
		}
		p.signalFreed()
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

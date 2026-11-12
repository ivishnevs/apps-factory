// Package runner hosts the node's executor slots (node-agent D1): N slot goroutines take the offers of the node's
// poll (http-node-transport D3) and each runs the existing executor (internal/worker) for its provider.
package runner

import (
	"context"
	"errors"
	"slices"
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
	// Conversation: a conversation run, which takes an idle conversation slot first (work-and-conversation-runs).
	Conversation bool
}

// Pool runs Slots slot loops and ConversationSlots more that take only conversation runs (work-and-conversation-runs:
// a full node still answers). An idle slot takes one offer at a time from Offer.
type Pool struct {
	Slots             int
	ConversationSlots int
	Sources           []*Source
	Claimable         func() bool // nil always claims
	// WorkClaimable is false while the node takes conversation runs only (Core's pause_work, the critical disk;
	// disk-pause-conversations): work slots then take conversations too. nil always claims.
	WorkClaimable func() bool
	Now           func() time.Time
	// Refused is called for an offer whose claim Core refused for now (the poller skips it a while).
	Refused func(run string)
	// StateDir keeps the local activity log (connector-trust): what ran on this machine, readable without Core.
	StateDir string

	mu     sync.Mutex
	ctx    context.Context
	wg     sync.WaitGroup
	slots  []*slot
	offers chan Offer
	// convOffers feed the conversation slots (index Slots and up).
	convOffers chan Offer
	freed      chan struct{}
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
	// conv: a conversation slot (takes conversation runs only). retired: removed by Resize; an idle one stopped, a
	// busy one finishes its run and then stops. A later Resize may reuse it.
	conv, retired bool
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

// Free is how many offers the pool takes now per provider: its idle work slots for each driver it may claim with,
// none while the node may not claim or takes conversations only.
func (p *Pool) Free() map[string]int { return p.free(false) }

// FreeConversation is how many conversation runs the pool's idle conversation slots take now per provider; while
// the node takes conversations only, its idle work slots count too.
func (p *Pool) FreeConversation() map[string]int { return p.free(true) }

func (p *Pool) workClaimable() bool { return p.WorkClaimable == nil || p.WorkClaimable() }

func (p *Pool) free(conversation bool) map[string]int {
	free := map[string]int{}
	if p.Claimable != nil && !p.Claimable() {
		return free
	}
	work := p.workClaimable()
	if !conversation && !work {
		return free
	}
	idle := 0
	p.mu.Lock()
	for _, s := range p.slots {
		if !s.busy && !s.retired && (s.conv == conversation || conversation && !work) {
			idle++
		}
	}
	p.mu.Unlock()
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

// Offer hands o to an idle slot, a conversation run to a conversation slot first; false when none takes it now (the
// run stays queued and is offered again).
func (p *Pool) Offer(o Offer) bool {
	p.mu.Lock()
	ch, conv := p.offers, p.convOffers
	p.mu.Unlock()
	if ch == nil {
		return false
	}
	if o.Conversation {
		select {
		case conv <- o:
			return true
		default:
		}
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
	p.mu.Lock()
	if p.Slots < 1 || p.ConversationSlots < 0 || len(p.Sources) == 0 {
		p.mu.Unlock()
		return errors.New("pool needs slots and sources")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p.ctx = ctx
	p.offers, p.convOffers = make(chan Offer), make(chan Offer)
	p.slots = make([]*slot, p.Slots+p.ConversationSlots)
	for i := range p.slots {
		p.slots[i] = &slot{conv: i >= p.Slots}
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
	offers := p.offers
	if s.conv {
		offers = p.convOffers // a conversation slot takes conversation runs only
	}
	p.wg.Add(1)
	go func(gen int) {
		defer p.wg.Done()
		defer stop()
		p.loop(node.WithSlot(ctx, i), i, gen, offers)
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
		if src == nil || src.Claimable != nil && !src.Claimable() || p.Claimable != nil && !p.Claimable() || !o.Conversation && !p.workClaimable() {
			continue // offered just before the driver or the node stopped claiming: it stays queued
		}
		if !p.take(i, gen, o.RunID) {
			return // retired meanwhile: the run stays queued and is offered again
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

// update applies f to slot i if gen is its current loop; a replaced loop gets false and exits, and so does a retired
// slot's loop (after f: a retired slot's run still ends normally).
func (p *Pool) update(i, gen int, f func(*slot)) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.slots[i]
	if s.gen != gen {
		return false
	}
	f(s)
	return !s.retired
}

// take marks slot i busy with run unless its loop was replaced or the slot retired.
func (p *Pool) take(i, gen int, run string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.slots[i]
	if s.gen != gen || s.retired {
		return false
	}
	s.busy, s.run = true, run
	return true
}

// Resize sets the work and conversation slots while the pool runs (settings from the web, node-packaging-updates B):
// new slots start at once, a removed idle slot stops, a removed busy slot finishes its run and then stops. Before Run
// it only sets the counts.
func (p *Pool) Resize(slots, conversation int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Slots, p.ConversationSlots = slots, conversation
	if p.ctx == nil || p.ctx.Err() != nil {
		return
	}
	for _, conv := range []bool{false, true} {
		want := slots
		if conv {
			want = conversation
		}
		var live []int
		for i, s := range p.slots {
			if !s.retired && s.conv == conv {
				live = append(live, i)
			}
		}
		// Shrink: idle slots first, the newest first.
		for k := len(live) - 1; k >= 0 && len(live) > want; k-- {
			if s := p.slots[live[k]]; !s.busy {
				p.retire(live[k])
				live = slices.Delete(live, k, k+1)
			}
		}
		for k := len(live) - 1; k >= 0 && len(live) > want; k-- {
			p.retire(live[k])
			live = slices.Delete(live, k, k+1)
		}
		// Grow: reuse a retired slot that is idle again, else add one.
		for n := len(live); n < want; n++ {
			i := slices.IndexFunc(p.slots, func(s *slot) bool { return s.retired && !s.busy })
			if i < 0 {
				p.slots = append(p.slots, &slot{})
				i = len(p.slots) - 1
			}
			s := p.slots[i]
			s.retired, s.conv = false, conv
			p.start(i)
		}
	}
}

// retire removes slot i: an idle slot stops now, a busy one when its run ends; p.mu is held.
func (p *Pool) retire(i int) {
	s := p.slots[i]
	s.retired = true
	if !s.busy && s.stop != nil {
		s.stop()
	}
}

// Status lists the slots in use: every slot except a retired one that finished.
func (p *Pool) Status() []node.Slot {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := make([]node.Slot, 0, len(p.slots))
	for i, s := range p.slots {
		if s.retired && !s.busy {
			continue
		}
		r = append(r, node.Slot{Index: i, Busy: s.busy, Run: s.run, Beat: s.beat})
	}
	return r
}

func (p *Pool) Restart(i int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.slots) || p.slots[i].busy || p.slots[i].retired || p.ctx.Err() != nil {
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

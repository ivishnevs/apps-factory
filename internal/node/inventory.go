package node

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// RunState is a run's state in the inventory heartbeat (runtime protocol v2 §7).
type RunState string

const (
	RunClaimed  RunState = "claimed"
	RunStarting RunState = "starting"
	RunRunning  RunState = "running"
	RunEnded    RunState = "ended_unacked"
)

// InventoryRun is one inventory entry; ElapsedMS counts from the claim on the monotonic clock,
// which excludes system sleep.
type InventoryRun struct {
	RunID     string   `json:"run_id"`
	State     RunState `json:"state"`
	ElapsedMS int64    `json:"elapsed_ms"`
}

// Inventory is the runs of the node's current epoch (runtime protocol v2): executors record their
// runs here, the heartbeat reports and stops them. An ended run stays until Core acknowledges it.
type Inventory struct {
	NodeID string

	mu     sync.Mutex
	epoch  int64
	seq    int64
	runs   map[string]*entry
	closed bool // the epoch takes no more runs (re-registering)
}

type entry struct {
	state   RunState
	claimed time.Time
	cancel  context.CancelFunc
}

func NewInventory(nodeID string, epoch int64) *Inventory {
	return &Inventory{NodeID: nodeID, epoch: epoch, runs: map[string]*entry{}}
}

func (v *Inventory) Epoch() int64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.epoch
}

// Claim returns the epoch and the claim_id of one claim attempt of slot: "<epoch>-<slot>-<n>", unique
// within the epoch. Retries of that attempt reuse the id.
func (v *Inventory) Claim(slot int) (int64, string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.seq++
	return v.epoch, fmt.Sprintf("%d-%d-%d", v.epoch, slot, v.seq)
}

// Add records a run claimed in epoch; false when that epoch is over or closed (the node re-registers).
func (v *Inventory) Add(run string, epoch int64, cancel context.CancelFunc) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if epoch != v.epoch || v.closed {
		return false
	}
	v.runs[run] = &entry{state: RunClaimed, claimed: time.Now(), cancel: cancel}
	return true
}

// Set moves an unended run to s.
func (v *Inventory) Set(run string, s RunState) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if e := v.runs[run]; e != nil && e.state != RunEnded {
		e.state = s
	}
}

// End marks the run ended; it is reported as ended_unacked until Ack.
func (v *Inventory) End(run string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if e := v.runs[run]; e != nil {
		e.state, e.cancel = RunEnded, nil
	}
}

// Ack drops ended runs whose terminal event Core applied.
func (v *Inventory) Ack(runs []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, run := range runs {
		if e := v.runs[run]; e != nil && e.state == RunEnded {
			delete(v.runs, run)
		}
	}
}

// Stop cancels a run's execution (the executor interrupts the runtime and reports the end). An ended
// run is dropped: Core asks to stop only runs it no longer expects. It reports whether run was listed.
func (v *Inventory) Stop(run string) bool {
	v.mu.Lock()
	e := v.runs[run]
	var cancel context.CancelFunc
	switch {
	case e == nil:
	case e.state == RunEnded:
		delete(v.runs, run)
	default:
		cancel = e.cancel
	}
	v.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return e != nil
}

// StopAll cancels every unended run.
func (v *Inventory) StopAll() {
	v.mu.Lock()
	var cancels []context.CancelFunc
	for _, e := range v.runs {
		if e.cancel != nil {
			cancels = append(cancels, e.cancel)
		}
	}
	v.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// Active counts unended runs.
func (v *Inventory) Active() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	n := 0
	for _, e := range v.runs {
		if e.state != RunEnded {
			n++
		}
	}
	return n
}

// Snapshot is the inventory of the heartbeat, ordered by run id.
func (v *Inventory) Snapshot() []InventoryRun {
	v.mu.Lock()
	defer v.mu.Unlock()
	r := make([]InventoryRun, 0, len(v.runs))
	for run, e := range v.runs {
		r = append(r, InventoryRun{RunID: run, State: e.state, ElapsedMS: time.Since(e.claimed).Milliseconds()})
	}
	sort.Slice(r, func(i, j int) bool { return r[i].RunID < r[j].RunID })
	return r
}

// Reset starts epoch and forgets every older run and claim.
func (v *Inventory) Reset(epoch int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.epoch, v.seq, v.runs, v.closed = epoch, 0, map[string]*entry{}, false
}

// Close ends the current epoch for new runs: Add refuses until Reset.
func (v *Inventory) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.closed = true
}

type slotKey struct{}

// WithSlot tells the executor which slot runs it (the v2 claim names the slot).
func WithSlot(ctx context.Context, slot int) context.Context {
	return context.WithValue(ctx, slotKey{}, slot)
}

// SlotOf is the slot of WithSlot, else 0.
func SlotOf(ctx context.Context) int {
	slot, _ := ctx.Value(slotKey{}).(int)
	return slot
}

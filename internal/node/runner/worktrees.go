package runner

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"appfactory/internal/protocol"
)

// worktree-inventory-cleanup: the node reports the task worktrees it holds with the heartbeat and removes what
// Core answers under removals; a removal repeats while the worktree is reported, so nothing here remembers what was
// removed.

const (
	// inventoryScan is how often the worktrees are listed again (also right after a removal).
	inventoryScan = time.Minute
	// inventoryFull: the full list goes at least this often even when its hash did not change.
	inventoryFull = 10 * time.Minute
)

type treeKey struct{ project, task string }

// treeInventory is the session's worktree inventory state.
type treeInventory struct {
	mu       sync.Mutex
	scanned  bool
	entries  []protocol.WorktreeEntry
	sentHash string
	fullAt   time.Time
	resend   bool
	removing map[treeKey]bool   // queued or being removed
	failed   map[treeKey]string // the last removal error, reported with the entry
	queue    chan protocol.WorktreeRemoval
	rescan   chan struct{}
}

func newTreeInventory() *treeInventory {
	return &treeInventory{removing: map[treeKey]bool{}, failed: map[treeKey]string{}, queue: make(chan protocol.WorktreeRemoval, 64), rescan: make(chan struct{}, 1)}
}

// inventoryOn: this node reports worktrees (worktrees enabled and a company to list them for).
func (s *session) inventoryOn() bool { return s.trees != nil && s.treeCompany() != "" }

func (s *session) treeCompany() string {
	if s.invCompany != "" {
		return s.invCompany
	}
	return s.company
}

// inventoryLoop lists the worktrees now, every inventoryScan and after each removal.
func (s *session) inventoryLoop(ctx context.Context) {
	t := time.NewTicker(inventoryScan)
	defer t.Stop()
	for {
		s.scanTrees(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.inv2.rescan:
		}
	}
}

func (s *session) scanTrees(ctx context.Context) {
	found, err := s.trees.Inventory(ctx, s.treeCompany())
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("listing worktrees", "error", err)
		}
		return
	}
	entries := make([]protocol.WorktreeEntry, 0, len(found))
	for _, e := range found {
		entries = append(entries, protocol.WorktreeEntry{Task: e.Task, Project: e.Project, SealedHead: e.SealedHead, Unpushed: e.Unpushed,
			LastUsed: e.LastUsed, Bytes: e.Bytes, Busy: e.Busy})
	}
	in := s.inv2
	in.mu.Lock()
	defer in.mu.Unlock()
	live := map[treeKey]bool{}
	for _, e := range entries {
		live[treeKey{e.Project, e.Task}] = true
	}
	for k := range in.failed {
		if !live[k] {
			delete(in.failed, k)
		}
	}
	in.entries, in.scanned = entries, true
}

// worktreesForBeat is the heartbeat's inventory: the full list when it changed, Core asked for it or inventoryFull
// passed, otherwise only the hash; nothing before the first scan.
func (s *session) worktreesForBeat() (entries []protocol.WorktreeEntry, hash string, full bool) {
	in := s.inv2
	in.mu.Lock()
	defer in.mu.Unlock()
	if !in.scanned {
		return nil, "", false
	}
	entries = make([]protocol.WorktreeEntry, len(in.entries))
	copy(entries, in.entries)
	for i := range entries {
		entries[i].Error = in.failed[treeKey{entries[i].Project, entries[i].Task}]
	}
	hash = protocol.WorktreesHash(entries)
	full = in.resend || hash != in.sentHash || time.Since(in.fullAt) >= inventoryFull
	return entries, hash, full
}

// worktreesDelivered records a delivered heartbeat's inventory and Core's answer.
func (s *session) worktreesDelivered(hash string, full, resend bool) {
	in := s.inv2
	in.mu.Lock()
	defer in.mu.Unlock()
	if full {
		in.sentHash, in.fullAt, in.resend = hash, time.Now(), false
	}
	if resend {
		in.resend = true
	}
}

// queueRemovals queues Core's removals that are not queued or running already.
func (s *session) queueRemovals(rms []protocol.WorktreeRemoval) {
	in := s.inv2
	in.mu.Lock()
	defer in.mu.Unlock()
	for _, rm := range rms {
		k := treeKey{rm.Project, rm.Task}
		if in.removing[k] {
			continue
		}
		select {
		case in.queue <- rm:
			in.removing[k] = true
		default: // full: Core answers it again
		}
	}
}

// remover removes queued worktrees one at a time.
func (s *session) remover(ctx context.Context) {
	in := s.inv2
	for {
		select {
		case <-ctx.Done():
			return
		case rm := <-in.queue:
			var err error
			if s.removeWorktree != nil {
				err = s.removeWorktree(ctx, rm)
			}
			k := treeKey{rm.Project, rm.Task}
			in.mu.Lock()
			delete(in.removing, k)
			if err != nil {
				in.failed[k] = err.Error()
			} else {
				delete(in.failed, k)
			}
			in.mu.Unlock()
			select {
			case in.rescan <- struct{}{}:
			default:
			}
		}
	}
}

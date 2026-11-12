package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// FeatureWorktreeInventory: the node reports its task worktrees in the heartbeat and removes what Core answers
// under removals (worktree-inventory-cleanup); Core then no longer lists cleanup for it from workspaces.node_id.
const FeatureWorktreeInventory = "worktree_inventory"

// MaxWorktreeEntries bounds one heartbeat's worktree inventory; the node sends the most recently used ones.
const MaxWorktreeEntries = 500

// WorktreeEntry is one task worktree a node holds (worktree-inventory-cleanup D2).
type WorktreeEntry struct {
	Task    string `json:"task"`
	Project string `json:"project"`
	// SealedHead is the HEAD of the last seal ("" for a worktree never sealed).
	SealedHead string `json:"sealed_head,omitempty"`
	// Unpushed: the worktree holds work no remote has (commits on no origin branch, or a WIP snapshot not saved
	// to origin), as of the last seal.
	Unpushed bool `json:"unpushed,omitempty"`
	// LastUsed is the Unix time of the last seal or, without one, of the worktree's creation.
	LastUsed int64 `json:"last_used"`
	// Bytes is the worktree's size on disk (refreshed at most hourly; 0 not measured yet).
	Bytes int64 `json:"bytes,omitempty"`
	// Busy: a run holds the worktree lock now.
	Busy bool `json:"busy,omitempty"`
	// Error is why the last removal Core answered failed (empty: none failed, or busy).
	Error string `json:"error,omitempty"`
}

// WorktreeRemoval is a worktree Core wants removed from the node, with the reason (closed, unknown_task,
// stale_copy). Only closed runs the company's on_task_closed hooks; the others are aborted when the seal finds
// work no remote has.
type WorktreeRemoval struct {
	Task    string `json:"task"`
	Project string `json:"project"`
	Reason  string `json:"reason"`
}

// Removal reasons.
const (
	RemovalClosed      = "closed"
	RemovalUnknownTask = "unknown_task"
	RemovalStaleCopy   = "stale_copy"
)

// WorktreesHash is the hash of an inventory: entries sorted by project and task, Bytes left out (it changes on its
// own) so an unchanged inventory sends only the hash.
func WorktreesHash(entries []WorktreeEntry) string {
	c := make([]WorktreeEntry, len(entries))
	copy(c, entries)
	for i := range c {
		c[i].Bytes = 0
	}
	sort.Slice(c, func(i, j int) bool {
		if c[i].Project != c[j].Project {
			return c[i].Project < c[j].Project
		}
		return c[i].Task < c[j].Task
	})
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

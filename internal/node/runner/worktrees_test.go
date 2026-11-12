package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/workspace"
)

// TestWorktreeInventoryHeartbeat: a node with worktrees announces worktree_inventory, reports its worktrees (in
// full when they changed or Core asked, otherwise by hash), removes what Core answers and reports a failed
// removal with the entry (worktree-inventory-cleanup).
func TestWorktreeInventoryHeartbeat(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(t.TempDir(), "trees")
	for _, task := range []string{"t1", "t2"} {
		if err := os.MkdirAll(filepath.Join(root, "acme", "app", "tasks", task), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	trees, err := workspace.NewWorktrees(workspace.WorktreesConfig{Enabled: true, Root: root, ScanCommand: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	f, api := newFakeCore(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, err := newSession(ctx, v2Node(t, api, dir), trees)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.registers[0].Features, protocol.FeatureWorktreeInventory) {
		t.Fatal("feature not announced", f.registers[0].Features)
	}
	removed := make(chan string, 4)
	s.invCompany = "acme"
	s.removeWorktree = func(_ context.Context, rm protocol.WorktreeRemoval) error {
		removed <- rm.Task + ":" + rm.Reason
		if rm.Task == "t2" {
			return errors.New("seal failed")
		}
		return os.RemoveAll(filepath.Join(root, "acme", rm.Project, "tasks", rm.Task))
	}

	if err = s.beat(ctx); err != nil || f.lastBeat().Worktrees != nil || f.lastBeat().WorktreesHash != "" {
		t.Fatal("inventory sent before the first scan", err)
	}
	s.scanTrees(ctx)
	f.answers = []HeartbeatAnswer{{Removals: []protocol.WorktreeRemoval{{Task: "t1", Project: "app", Reason: protocol.RemovalClosed}, {Task: "t2", Project: "app", Reason: protocol.RemovalStaleCopy}}}}
	if err = s.beat(ctx); err != nil {
		t.Fatal(err)
	}
	hb := f.lastBeat()
	if hb.Worktrees == nil || len(*hb.Worktrees) != 2 || hb.WorktreesHash != protocol.WorktreesHash(*hb.Worktrees) {
		t.Fatalf("full inventory not sent: %+v", hb.Worktrees)
	}
	// Unchanged: only the hash; Core asking again gets the list.
	f.answers = []HeartbeatAnswer{{WorktreesResend: true}}
	if err = s.beat(ctx); err != nil || f.lastBeat().Worktrees != nil || f.lastBeat().WorktreesHash != hb.WorktreesHash {
		t.Fatal("unchanged inventory sent in full", err)
	}
	if err = s.beat(ctx); err != nil || f.lastBeat().Worktrees == nil {
		t.Fatal("resend not honoured", err)
	}

	go s.remover(ctx)
	got := []string{<-removed, <-removed}
	slices.Sort(got)
	if !slices.Equal(got, []string{"t1:closed", "t2:stale_copy"}) {
		t.Fatal(got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.scanTrees(ctx)
		entries, _, _ := s.worktreesForBeat()
		if len(entries) == 1 && entries[0].Task == "t2" && entries[0].Error == "seal failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after removal: %+v", entries)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// A removal Core answers again while one is queued or running is not queued twice.
	s.inv2.mu.Lock()
	s.inv2.removing[treeKey{"app", "t2"}] = true
	s.inv2.mu.Unlock()
	s.queueRemovals([]protocol.WorktreeRemoval{{Task: "t2", Project: "app", Reason: protocol.RemovalStaleCopy}})
	if len(s.inv2.queue) != 0 {
		t.Fatal("queued twice")
	}
}

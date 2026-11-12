package storage

import (
	"context"
	"slices"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/protocol"
)

func inventoryNode(r *NodeRegistration) {
	r.Features = []string{protocol.FeatureWorktreeInventory}
}

func setWorkspace(t *testing.T, s *Store, task, node string) {
	t.Helper()
	if _, err := s.DB.Exec("INSERT INTO workspaces(company_id,task_id,mode,branch,node_id,updated_at) VALUES(?,?,'worktree',?,?,1) ON CONFLICT(company_id,task_id) DO UPDATE SET node_id=excluded.node_id",
		DefaultCompany, task, "af/"+task, node); err != nil {
		t.Fatal(err)
	}
}

func setTaskStatus(t *testing.T, s *Store, task, status string) {
	t.Helper()
	if _, err := s.DB.Exec("UPDATE tasks SET status=? WHERE company_id=? AND id=?", status, DefaultCompany, task); err != nil {
		t.Fatal(err)
	}
}

func feedKinds(t *testing.T, s *Store, kind string) []string {
	t.Helper()
	rows, err := s.DB.Query("SELECT task_id FROM feed_events WHERE company_id=? AND kind=? ORDER BY seq", DefaultCompany, kind)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var task string
		if err = rows.Scan(&task); err != nil {
			t.Fatal(err)
		}
		out = append(out, task)
	}
	return out
}

func removalsOf(a HeartbeatAnswer) []string {
	var out []string
	for _, r := range a.Removals {
		out = append(out, r.Task+":"+r.Reason)
	}
	slices.Sort(out)
	return out
}

// TestWorktreeInventoryRemovals: removals are judged from what the node reports, not from workspaces.node_id
// (worktree-inventory-cleanup D3). The 2026-11-12 case: a task that moved to another node before it closed is
// removed from the node that ran it first.
func TestWorktreeInventoryRemovals(t *testing.T) {
	s := engineStore(t)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	ctx := context.Background()
	register(t, s, "n1", 1, inventoryNode)
	register(t, s, "n2", 1, inventoryNode)
	for _, id := range []string{"moved", "owned", "stale", "unpushed", "closedhere"} {
		createEngineTask(t, s, id)
	}
	setWorkspace(t, s, "moved", "n2") // ran on n1, review fix on n2, then DONE
	setTaskStatus(t, s, "moved", "DONE")
	setWorkspace(t, s, "owned", "n1")
	setWorkspace(t, s, "stale", "n2")
	setWorkspace(t, s, "unpushed", "n2")
	setWorkspace(t, s, "closedhere", "n1") // not in n1's inventory: no old-style cleanup either
	setTaskStatus(t, s, "closedhere", "CANCELLED")

	now := fake.Now().Unix()
	entries := []protocol.WorktreeEntry{
		{Task: "moved", Project: "p", LastUsed: now, Bytes: 900_000_000},
		{Task: "owned", Project: "p", LastUsed: now},
		{Task: "stale", Project: "p", LastUsed: now},
		{Task: "unpushed", Project: "p", LastUsed: now, Unpushed: true},
		{Task: "ghost", Project: "p", LastUsed: now},
	}
	hash := protocol.WorktreesHash(entries)
	beat := func(list []protocol.WorktreeEntry, h string) HeartbeatAnswer {
		t.Helper()
		hb := NodeHeartbeat{Epoch: 1, WorktreesHash: h}
		if list != nil {
			hb.Worktrees = &list
		}
		a, err := s.NodeHeartbeat(ctx, DefaultCompany, "n1", hb)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := beat(entries, hash)
	if got := removalsOf(a); !slices.Equal(got, []string{"moved:closed"}) || len(a.Cleanup) != 0 || a.WorktreesResend {
		t.Fatalf("first beat: removals %v cleanup %v resend %v", got, a.Cleanup, a.WorktreesResend)
	}
	if got := feedKinds(t, s, "node.worktree_stuck"); !slices.Equal(got, []string{"unpushed"}) {
		t.Fatal("unpushed_elsewhere event", got)
	}

	// A day later: the unknown task's worktree and the stale copy go too; the owned and unpushed ones stay. The
	// closed one, still reported after an hour, raises one stuck event.
	fake.Advance(25 * time.Hour)
	a = beat(nil, hash)
	if got := removalsOf(a); !slices.Equal(got, []string{"ghost:unknown_task", "moved:closed", "stale:stale_copy"}) || a.WorktreesResend {
		t.Fatalf("day later: %v resend %v", got, a.WorktreesResend)
	}
	fake.Advance(2 * time.Hour)
	beat(nil, hash)
	beat(nil, hash)
	if got := feedKinds(t, s, "node.worktree_stuck"); !slices.Equal(got, []string{"unpushed", "moved", "ghost", "stale"}) {
		t.Fatal("stuck events", got)
	}

	// A hash Core has no list for asks for the list.
	if a = beat(nil, "other"); !a.WorktreesResend {
		t.Fatal("no resend for an unknown hash")
	}

	// The node removed them: the rows go, each removal is in the feed once, nothing is answered again.
	left := []protocol.WorktreeEntry{entries[1], entries[3]}
	a = beat(left, protocol.WorktreesHash(left))
	if len(a.Removals) != 0 {
		t.Fatal("removed worktrees answered again", removalsOf(a))
	}
	if got := feedKinds(t, s, "node.worktree_removed"); len(got) != 3 {
		t.Fatal("removed events", got)
	}

	// A reopened task's worktree is kept and its bookkeeping cleared.
	setTaskStatus(t, s, "unpushed", "READY")
	setWorkspace(t, s, "unpushed", "n1")
	if a = beat(nil, protocol.WorktreesHash(left)); len(a.Removals) != 0 {
		t.Fatal(removalsOf(a))
	}
}

// TestWorktreeStaleCopyLowDisk: below the low watermark a stale copy goes after an hour instead of a day.
func TestWorktreeStaleCopyLowDisk(t *testing.T) {
	s := engineStore(t)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	ctx := context.Background()
	register(t, s, "n1", 1, inventoryNode)
	createEngineTask(t, s, "stale")
	setWorkspace(t, s, "stale", "n2")
	list := []protocol.WorktreeEntry{{Task: "stale", Project: "p", LastUsed: fake.Now().Unix()}}
	fake.Advance(2 * time.Hour)
	for _, disk := range []string{"ok", "low"} {
		a, err := s.NodeHeartbeat(ctx, DefaultCompany, "n1", NodeHeartbeat{Epoch: 1, Worktrees: &list, WorktreesHash: protocol.WorktreesHash(list),
			Health: []byte(`{"disk":"` + disk + `"}`)})
		if err != nil {
			t.Fatal(err)
		}
		if want := disk == "low"; (len(a.Removals) == 1) != want {
			t.Fatal(disk, removalsOf(a))
		}
	}
}

// TestOldNodeKeepsCleanupListing: a node without the worktree_inventory feature keeps the workspaces.node_id listing.
func TestOldNodeKeepsCleanupListing(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	register(t, s, "n1", 1)
	createEngineTask(t, s, "done")
	setWorkspace(t, s, "done", "n1")
	setTaskStatus(t, s, "done", "DONE")
	a, err := s.NodeHeartbeat(ctx, DefaultCompany, "n1", NodeHeartbeat{Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.Cleanup, []string{"done"}) || len(a.Removals) != 0 {
		t.Fatal(a.Cleanup, removalsOf(a))
	}
	w, err := s.NodeWork(ctx, DefaultCompany, "n1", 1)
	if err != nil || !slices.Equal(w.Cleanup, []string{"done"}) {
		t.Fatal(w.Cleanup, err)
	}
	register(t, s, "n1", 2, inventoryNode)
	if w, err = s.NodeWork(ctx, DefaultCompany, "n1", 2); err != nil || len(w.Cleanup) != 0 {
		t.Fatal("inventory node polled cleanup", w.Cleanup, err)
	}
}

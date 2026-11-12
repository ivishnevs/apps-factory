package storage

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"appfactory/internal/protocol"
)

// worktree-inventory-cleanup: a worktree_inventory node reports the task worktrees it holds; Core keeps the last
// report in node_worktrees and answers removals judged from it (design D3), on every heartbeat while the node
// still reports the worktree.
const (
	// WorktreeStaleAfter: an open task's copy on a node that does not own its workspace, or a worktree of a task
	// Core does not know, unused this long is removed.
	WorktreeStaleAfter = 24 * time.Hour
	// WorktreeStaleLowDisk: the same below the low disk watermark, never for a copy used more recently.
	WorktreeStaleLowDisk = time.Hour
	// WorktreeStuckAfter: a removal still reported this long raises one node.worktree_stuck.
	WorktreeStuckAfter = time.Hour
	maxRemovals        = 64
)

// applyWorktreeInventory stores a full inventory (entries != nil) as the node's worktrees, or, for a heartbeat with
// only the hash, answers whether Core needs the full list again.
func applyWorktreeInventory(ctx context.Context, tx *Tx, node, stored string, entries *[]protocol.WorktreeEntry, hash string) (bool, error) {
	if entries == nil {
		return hash != "" && hash != stored, nil
	}
	list := *entries
	if len(list) > protocol.MaxWorktreeEntries {
		return false, inputError(fmt.Sprintf("at most %d worktrees", protocol.MaxWorktreeEntries))
	}
	now := tx.now().Unix()
	seen := map[[2]string]bool{}
	for _, e := range list {
		if !safeWorktreeID(e.Task) || !safeWorktreeID(e.Project) {
			return false, inputError("worktree task and project must be ids")
		}
		seen[[2]string{e.Project, e.Task}] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO node_worktrees(company_id,node_id,project_id,task_id,sealed_head,unpushed,busy,bytes,last_used,error,reported_at)
 VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(company_id,node_id,project_id,task_id) DO UPDATE SET sealed_head=excluded.sealed_head,
 unpushed=excluded.unpushed,busy=excluded.busy,bytes=CASE WHEN excluded.bytes>0 THEN excluded.bytes ELSE node_worktrees.bytes END,
 last_used=excluded.last_used,error=excluded.error,reported_at=excluded.reported_at`,
			tx.company, node, e.Project, e.Task, e.SealedHead, e.Unpushed, e.Busy, e.Bytes, e.LastUsed, truncate(e.Error, 500), now); err != nil {
			return false, err
		}
	}
	rows, err := tx.QueryContext(ctx, "SELECT project_id,task_id,remove_reason,stuck_raised,bytes FROM node_worktrees WHERE company_id=? AND node_id=?", tx.company, node)
	if err != nil {
		return false, err
	}
	type gone struct {
		project, task, reason string
		stuck                 bool
		bytes                 int64
	}
	var goneRows []gone
	for rows.Next() {
		var g gone
		if err = rows.Scan(&g.project, &g.task, &g.reason, &g.stuck, &g.bytes); err != nil {
			rows.Close()
			return false, err
		}
		if !seen[[2]string{g.project, g.task}] {
			goneRows = append(goneRows, g)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()
	var name string
	if len(goneRows) > 0 {
		if err = tx.QueryRowContext(ctx, "SELECT name FROM nodes WHERE company_id=? AND id=?", tx.company, node).Scan(&name); err != nil {
			return false, err
		}
	}
	for _, g := range goneRows {
		if _, err = tx.ExecContext(ctx, "DELETE FROM node_worktrees WHERE company_id=? AND node_id=? AND project_id=? AND task_id=?", tx.company, node, g.project, g.task); err != nil {
			return false, err
		}
		if g.reason == "" && !g.stuck {
			continue // a worktree the node removed on its own (an older cleanup, a reset)
		}
		summary := "worktree of task " + g.task + " removed (" + cmp.Or(g.reason, "gone") + ")"
		if g.bytes > 0 {
			summary += fmt.Sprintf(", %.1f GB freed", float64(g.bytes)/1e9)
		}
		item := FeedItem{EventID: protocol.ID(), Type: "node.worktree_removed", EntityID: "node:" + node, ProjectID: g.project, TaskID: g.task,
			CreatedAt: tx.now(), Actor: "node", Summary: "node " + nodeLabel(name, node) + ": " + summary, Ref: node}
		if err = feed(ctx, tx, item); err != nil {
			return false, err
		}
	}
	_, err = tx.ExecContext(ctx, "UPDATE nodes SET worktrees_hash=? WHERE company_id=? AND id=?", protocol.WorktreesHash(list), tx.company, node)
	return false, err
}

func safeWorktreeID(id string) bool {
	if id == "" || len(id) > 128 || id == "." || id == ".." {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// nodeDiskLow: the heartbeat's health says the disk is below the low watermark.
func nodeDiskLow(health []byte) bool {
	d := readDisk(health).Disk
	return d == "low" || d == "critical"
}

// worktreeVerdict judges one reported worktree (design D3): the removal reason, or "" to keep it; stuck names why
// a kept worktree needs the owner (unpushed_elsewhere).
func worktreeVerdict(node string, w nodeWorktree, now time.Time, diskLow bool) (reason, stuck string) {
	age := now.Sub(time.Unix(w.lastUsed, 0))
	switch {
	case !w.taskKnown:
		if age >= WorktreeStaleAfter {
			return protocol.RemovalUnknownTask, ""
		}
	case w.status == "DONE" || w.status == "CANCELLED":
		return protocol.RemovalClosed, ""
	case w.owner == node:
	case w.unpushed:
		return "", "unpushed_elsewhere"
	case age >= WorktreeStaleAfter || diskLow && age >= WorktreeStaleLowDisk:
		return protocol.RemovalStaleCopy, ""
	}
	return "", ""
}

type nodeWorktree struct {
	project, task, status, owner, errText, removeReason string
	taskKnown, unpushed, busy, stuckRaised              bool
	lastUsed, removeSince                               int64
}

// worktreeRemovals judges the node's reported worktrees, keeps the removal bookkeeping and raises
// node.worktree_stuck once per stuck worktree.
func worktreeRemovals(ctx context.Context, tx *Tx, node, name string, diskLow bool) ([]protocol.WorktreeRemoval, error) {
	rows, err := tx.QueryContext(ctx, `SELECT nw.project_id,nw.task_id,coalesce(t.status,''),coalesce(w.node_id,''),nw.error,nw.remove_reason,
 t.id IS NOT NULL,nw.unpushed,nw.busy,nw.stuck_raised,nw.last_used,nw.remove_since
 FROM node_worktrees nw LEFT JOIN tasks t ON t.company_id=nw.company_id AND t.id=nw.task_id
 LEFT JOIN workspaces w ON w.company_id=nw.company_id AND w.task_id=nw.task_id
 WHERE nw.company_id=? AND nw.node_id=? ORDER BY nw.last_used,nw.task_id`, tx.company, node)
	if err != nil {
		return nil, err
	}
	var all []nodeWorktree
	for rows.Next() {
		var w nodeWorktree
		if err = rows.Scan(&w.project, &w.task, &w.status, &w.owner, &w.errText, &w.removeReason, &w.taskKnown, &w.unpushed, &w.busy, &w.stuckRaised, &w.lastUsed, &w.removeSince); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, w)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	now := tx.now()
	out := []protocol.WorktreeRemoval{}
	for _, w := range all {
		reason, stuck := worktreeVerdict(node, w, now, diskLow)
		since, raised, raise := w.removeSince, w.stuckRaised, ""
		switch {
		case reason != "" && (w.removeReason != reason || since == 0):
			since, raised = now.Unix(), false
		case reason != "" && !raised && now.Sub(time.Unix(since, 0)) >= WorktreeStuckAfter:
			why := cmp.Or(w.errText, "the worktree stays locked by a run")
			raise = fmt.Sprintf("worktree of task %s (%s) not removed for an hour: %s", w.task, reason, why)
		case reason == "" && stuck != "" && !raised:
			since = 0
			raise = "worktree of task " + w.task + " holds work no remote has, but another node continues the task; push or drop it on this node"
		case reason == "" && stuck == "":
			since, raised = 0, false
		}
		if raise != "" {
			raised = true
			item := FeedItem{EventID: protocol.ID(), Type: "node.worktree_stuck", EntityID: "node:" + node, ProjectID: w.project, TaskID: w.task,
				CreatedAt: now, Actor: "node", Summary: "node " + nodeLabel(name, node) + ": " + raise, Ref: node, Attention: true}
			if err = feed(ctx, tx, item); err != nil {
				return nil, err
			}
		}
		if reason != w.removeReason || since != w.removeSince || raised != w.stuckRaised {
			if _, err = tx.ExecContext(ctx, "UPDATE node_worktrees SET remove_reason=?,remove_since=?,stuck_raised=? WHERE company_id=? AND node_id=? AND project_id=? AND task_id=?",
				reason, since, raised, tx.company, node, w.project, w.task); err != nil {
				return nil, err
			}
		}
		if reason != "" && len(out) < maxRemovals {
			out = append(out, protocol.WorktreeRemoval{Task: w.task, Project: w.project, Reason: reason})
		}
	}
	return out, nil
}

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

var (
	repoURL    = regexp.MustCompile(`^(https://|ssh://|file://|git@|/)[^\s\x00]+$`)
	branchName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	gitSHA     = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
)

func validRepo(url, branch string) error {
	if url != "" && (len(url) > 2048 || !repoURL.MatchString(url)) {
		return inputError("repo_url must be an https, ssh, file or scp-style git URL, or an absolute path")
	}
	if branch != "" && (!branchName.MatchString(branch) || strings.Contains(branch, "..") || strings.HasSuffix(branch, ".lock") || strings.HasSuffix(branch, "/")) {
		return inputError("default_branch is not a valid branch name")
	}
	if url == "" && branch != "" {
		return inputError("default_branch needs repo_url")
	}
	return nil
}

// WorkspaceMode is the agent's spec.workspace.mode in its JSON config; "" when unset or unknown.
func WorkspaceMode(agentJSON []byte) string {
	var a struct {
		Spec struct {
			Workspace struct {
				Mode string `json:"mode"`
			} `json:"workspace"`
		} `json:"spec"`
	}
	if json.Unmarshal(agentJSON, &a) != nil {
		return ""
	}
	switch m := a.Spec.Workspace.Mode; m {
	case "shared", "worktree", "scratch", "fresh_from_default":
		return m
	}
	return ""
}

// ReportWorkspace records the workspace of a task run as its worker reports it. The run's lease
// authenticates the report, also after the run ended (the seal follows the end).
func (s *Store) ReportWorkspace(ctx context.Context, company, runID, lease string, w tasks.Workspace) (tasks.Workspace, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return w, err
	}
	defer tx.Rollback()
	var token string
	var taskID sql.NullString
	err = tx.QueryRowContext(ctx, "SELECT lease,task_id FROM runs WHERE company_id=? AND id=?", company, runID).Scan(&token, &taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	if err != nil {
		return w, err
	}
	if token == "" || token != lease {
		return w, ErrConflict
	}
	if !taskID.Valid {
		return w, inputError("workspace reports belong to task runs")
	}
	switch w.Mode {
	case "shared", "worktree", "scratch":
	default:
		return w, inputError("mode must be shared, worktree or scratch")
	}
	for _, sha := range []string{w.ExitSHA, w.SealedHead} {
		if sha != "" && !gitSHA.MatchString(sha) {
			return w, inputError("exit_sha and sealed_head must be full git object IDs")
		}
	}
	if (w.Branch != "" && !branchName.MatchString(w.Branch)) || len(w.NodeID) > 128 {
		return w, inputError("invalid branch or node_id")
	}
	w.TaskID, w.RunID, w.UpdatedAt = taskID.String, runID, tx.now().Unix()
	// A fresh_from_default worktree starts every run from the default branch: nothing on it is continued
	// elsewhere, so it never pins the task to its node (task-worktrees D4, D8).
	if w.Unpushed {
		spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE company_id=? AND id=?", company, runID)
		if err != nil {
			return w, err
		}
		if spec.WorkspaceMode == "fresh_from_default" {
			w.Unpushed = false
		}
	}
	// exit_sha is set by the accepted exit (Core) or the node's report; an empty report keeps it.
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspaces(company_id,task_id,mode,branch,node_id,exit_sha,sealed_head,sealed_at,run_id,updated_at,unpushed) VALUES(?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(company_id,task_id) DO UPDATE SET mode=excluded.mode,branch=excluded.branch,node_id=excluded.node_id,
 exit_sha=CASE WHEN excluded.exit_sha!='' THEN excluded.exit_sha ELSE workspaces.exit_sha END,sealed_head=excluded.sealed_head,
 sealed_at=excluded.sealed_at,run_id=excluded.run_id,updated_at=excluded.updated_at,unpushed=excluded.unpushed`,
		company, w.TaskID, w.Mode, w.Branch, w.NodeID, w.ExitSHA, w.SealedHead, w.SealedAt, w.RunID, w.UpdatedAt, w.Unpushed); err != nil {
		return w, err
	}
	return w, tx.Commit()
}

func (s *Store) TaskWorkspace(ctx context.Context, company, task string) (tasks.Workspace, error) {
	var w tasks.Workspace
	err := s.DB.QueryRowContext(ctx, "SELECT task_id,mode,branch,node_id,exit_sha,sealed_head,sealed_at,run_id,updated_at,unpushed FROM workspaces WHERE company_id=? AND task_id=?", company, task).
		Scan(&w.TaskID, &w.Mode, &w.Branch, &w.NodeID, &w.ExitSHA, &w.SealedHead, &w.SealedAt, &w.RunID, &w.UpdatedAt, &w.Unpushed)
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	return w, err
}

// UnmergedWindow is how far back the unmerged-branch check looks at closed tasks (task-worktrees D9).
const UnmergedWindow = 30 * 24 * time.Hour

// ReportUnmergedBranches is the daily unmerged-branch check (task-worktrees D9): every task closed (DONE or
// CANCELLED) within UnmergedWindow whose last exit facts show its branch ahead of the default branch with a
// pull request that is not merged, and without an abandon_branch answer, gets one task.unmerged_branch feed
// event addressed to the manager of its last assignee (Agent) — exactly one per task, however often the check
// runs. It returns the number of events recorded.
func (s *Store) ReportUnmergedBranches(ctx context.Context, company string) (int, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	since := tx.now().Add(-UnmergedWindow).Unix()
	rows, err := tx.QueryContext(ctx, `SELECT t.id,(SELECT e.facts FROM run_exits e WHERE e.company_id=t.company_id AND e.task_id=t.id AND e.facts!='' ORDER BY e.created_at DESC, e.rowid DESC LIMIT 1)
 FROM tasks t WHERE t.company_id=? AND t.status IN ('DONE','CANCELLED') AND json_extract(t.body,'$.updated_at')>=?
 AND NOT EXISTS (SELECT 1 FROM workspaces w WHERE w.company_id=t.company_id AND w.task_id=t.id AND w.abandon_branch=1)
 AND NOT EXISTS (SELECT 1 FROM feed_events f WHERE f.company_id=t.company_id AND f.task_id=t.id AND f.kind='task.unmerged_branch')
 ORDER BY t.id`, company, since)
	if err != nil {
		return 0, err
	}
	type candidate struct {
		task  string
		facts ExitFacts
	}
	var found []candidate
	for rows.Next() {
		var id string
		var facts sql.NullString
		if err = rows.Scan(&id, &facts); err != nil {
			rows.Close()
			return 0, err
		}
		var f ExitFacts
		if !facts.Valid || json.Unmarshal([]byte(facts.String), &f) != nil {
			continue
		}
		if f.AheadOfDefault > 0 && f.PR != nil && f.PR.State != "merged" {
			found = append(found, candidate{id, f})
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, c := range found {
		t, err := taskTx(ctx, tx, c.task)
		if err != nil {
			return 0, err
		}
		manager := retryTarget(tx, t.Assignee)
		pr := "#" + strconv.Itoa(c.facts.PR.Number)
		if c.facts.PR.URL != "" {
			pr = c.facts.PR.URL
		}
		summary := fmt.Sprintf("for %s: task %s closed %s with branch %s %d commit(s) ahead of %s and pull request %s %s, not merged (last assignee %s); merge it, or answer abandon_branch (factory abandon-branch %s)",
			manager, t.ID, t.Status, c.facts.Branch, c.facts.AheadOfDefault, c.facts.DefaultBranch, pr, c.facts.PR.State, t.Assignee, t.ID)
		if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task.unmerged_branch", EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: tx.now(), Status: t.Status,
			TaskID: t.ID, Agent: manager, Actor: "system", Summary: summary, Attention: true, Ref: pr}); err != nil {
			return 0, err
		}
	}
	return len(found), tx.Commit()
}

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

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
	case "shared", "worktree", "scratch":
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

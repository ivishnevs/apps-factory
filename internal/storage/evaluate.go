package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// TaskSnapshot loads what tasks.Evaluate needs about one task in a single transaction.
// AgentConfigured is left for the caller, which owns the agent catalog.
func (s *Store) TaskSnapshot(ctx context.Context, company, id string) (tasks.Snapshot, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return tasks.Snapshot{}, err
	}
	defer tx.Rollback()
	t, err := taskTx(ctx, tx, id)
	if err != nil {
		return tasks.Snapshot{}, err
	}
	return snapshotTx(ctx, tx, t)
}

// TaskSnapshots loads snapshots of every non-terminal task, optionally of one project.
func (s *Store) TaskSnapshots(ctx context.Context, company, project string) ([]tasks.Snapshot, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ts, err := listDocs[tasks.Task](ctx, tx, "SELECT body FROM tasks WHERE company_id=? AND (?='' OR project_id=?) AND status NOT IN ('DONE','CANCELLED') ORDER BY priority DESC,created_at,id", company, project, project)
	if err != nil {
		return nil, err
	}
	out := make([]tasks.Snapshot, 0, len(ts))
	for _, t := range ts {
		snap, err := snapshotTx(ctx, tx, t)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, nil
}

func snapshotTx(ctx context.Context, tx *Tx, t tasks.Task) (tasks.Snapshot, error) {
	snap := tasks.Snapshot{Task: t}
	run := func(query string, args ...any) (*tasks.Run, error) {
		var r tasks.Run
		err := tx.QueryRowContext(ctx, query, args...).Scan(&r.ID, &r.Status, &r.TaskID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return &r, err
	}
	var err error
	if t.CurrentRunID != "" {
		if snap.CurrentRun, err = run("SELECT id,status,coalesce(task_id,'') FROM runs WHERE company_id=? AND id=?", tx.company, t.CurrentRunID); err != nil {
			return snap, err
		}
		spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE company_id=? AND id=?", tx.company, t.CurrentRunID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return snap, err
		}
		var captured config.Agent
		if len(spec.ConfigSnapshot) > 0 && json.Unmarshal(spec.ConfigSnapshot, &captured) == nil {
			snap.MaxRetries = captured.Spec.Execution.MaxRetries
		}
	}
	if t.WaitMessageID != "" {
		m, err := messageTx(ctx, tx, t.WaitMessageID)
		if err != nil {
			return snap, err
		}
		if m.Status != "ANSWERED" {
			snap.WaitMessage, snap.WaitMessageRecipient = m.ID, m.Recipient
		}
	}
	for _, dep := range t.Dependencies {
		d, err := taskTx(ctx, tx, dep)
		if err != nil {
			return snap, err
		}
		snap.Dependencies = append(snap.Dependencies, tasks.Dependency{ID: d.ID, Status: d.Status})
	}
	for _, ref := range t.RequiredArtifacts {
		a, err := artifactApprovalTx(ctx, tx, t.ProjectID, ref.ID, ref.Version)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return snap, err
		}
		if err != nil || a.Status != "APPROVED" || a.Artifact == nil || *a.Artifact != ref {
			snap.UnapprovedArtifacts = append(snap.UnapprovedArtifacts, ref)
		}
	}
	if t.Assignee != "" {
		if snap.AgentRun, err = run("SELECT id,status,coalesce(task_id,'') FROM runs WHERE company_id=? AND agent=? AND status IN ('QUEUED','RUNNING','CANCELLING') AND coalesce(task_id,'')!=? ORDER BY created_at LIMIT 1", tx.company, t.Assignee, t.ID); err != nil {
			return snap, err
		}
	}
	p, err := projectTx(ctx, tx, t.ProjectID)
	if err != nil {
		return snap, err
	}
	// The project and workspace writer indexes include UNKNOWN runs, which quarantine the project.
	snap.WriterRun, err = run("SELECT id,status,coalesce(task_id,'') FROM runs WHERE company_id=? AND (project=? OR workspace=?) AND status IN ('QUEUED','RUNNING','CANCELLING','UNKNOWN') AND coalesce(task_id,'')!=? ORDER BY status='UNKNOWN' DESC,created_at LIMIT 1", tx.company, t.ProjectID, p.Workspace, t.ID)
	return snap, err
}

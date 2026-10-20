package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// OpenSpec change lifecycle (core-owned-project-data D5): when the last task linked to an active change is
// DONE, Core creates one task to archive the change; a daily check reports active changes nobody works on.

// OrphanWindow: a change linked by a task closed within this window still counts as worked on.
const OrphanWindow = 7 * 24 * time.Hour

// archiveChangeTask runs in closedTask: done is DONE and links an OpenSpec change.
func archiveChangeTask(ctx context.Context, tx *Tx, done tasks.Task) error {
	change := done.OpenSpecChange
	if change == "" || done.Status != "DONE" {
		return nil
	}
	p, err := projectTx(ctx, tx, done.ProjectID)
	if err != nil || !p.OpenSpecEnabled() {
		return err
	}
	var open int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM tasks WHERE company_id=? AND project_id=? AND (body->>'openspec_change')=? AND status NOT IN ('DONE','CANCELLED')", tx.company, p.ID, change).Scan(&open); err != nil || open > 0 {
		return err
	}
	// Only an active change is archived (its proposal is current, not yet moved below changes/archive).
	if _, _, found, err := specDoc(ctx, tx, tx.company, p.ID, openspec.ProposalPath(change)); err != nil || !found {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO openspec_changes(company_id,project_id,change) VALUES(?,?,?) ON CONFLICT (company_id,project_id,change) DO NOTHING", tx.company, p.ID, change); err != nil {
		return err
	}
	var existing string
	if err = tx.QueryRowContext(ctx, "SELECT archive_task_id FROM openspec_changes WHERE company_id=? AND project_id=? AND change=?", tx.company, p.ID, change).Scan(&existing); err != nil || existing != "" {
		return err
	}
	assignee := done.Assignee
	if p.OpenSpec != nil && p.OpenSpec.ArchiveAssignee != "" {
		assignee = p.OpenSpec.ArchiveAssignee
	} else if tx.store.ManagerOf != nil {
		if m := tx.store.ManagerOf(done.Assignee); m != "" && m != "owner" {
			assignee = m
		}
	}
	now := tx.now().Unix()
	t := tasks.Task{ID: protocol.ID(), ProjectID: p.ID, Title: "Archive OpenSpec change " + change, Assignee: assignee, CreatedBy: "system", OpenSpecChange: change,
		Description:  fmt.Sprintf("Every task linked to OpenSpec change %s is DONE (the last: %s). Check that the implementation matches the change, then archive it with the official OpenSpec workflow (openspec archive %s in your worktree, which merges its delta specs into %s/specs and moves it below changes/archive); your exit uploads the result.", change, done.ID, change, p.OpenSpecDir()),
		Dependencies: []string{}, Status: "READY", Version: 1, CreatedAt: now, UpdatedAt: now}
	b, _ := json.Marshal(t)
	if _, err = tx.ExecContext(ctx, "INSERT INTO tasks(id,project_id,status,assignee,priority,version,body,created_at,company_id) VALUES(?,?,?,?,?,?,?,?,?)", t.ID, t.ProjectID, t.Status, t.Assignee, t.Priority, t.Version, b, t.CreatedAt, tx.company); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE openspec_changes SET archive_task_id=? WHERE company_id=? AND project_id=? AND change=?", t.ID, tx.company, p.ID, change); err != nil {
		return err
	}
	if err = entityEvent(ctx, tx, "task.created", t.ProjectID, t.ID, t); err != nil {
		return err
	}
	return addTrigger(ctx, tx, &t, "created_ready", true, "system", done.ID, "last task of OpenSpec change "+change+" is DONE")
}

// ReportOrphanChanges is the daily check: every active OpenSpec change of an enabled project that no open
// task, and no task closed within OrphanWindow, links gets one feed event for manager (the project's
// manager), once per change.
func (s *Store) ReportOrphanChanges(ctx context.Context, company, manager string) (int, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	projects, err := listDocs[tasks.Project](ctx, tx, "SELECT body FROM projects WHERE company_id=? ORDER BY id", company)
	if err != nil {
		return 0, err
	}
	if manager == "" {
		manager = "owner"
	}
	since := tx.now().Add(-OrphanWindow).Unix()
	reported := 0
	for _, p := range projects {
		if !p.OpenSpecEnabled() {
			continue
		}
		docs, err := specDocs(ctx, tx, company, p.ID, "changes/")
		if err != nil {
			return 0, err
		}
		for _, change := range openspec.Changes(docs) {
			var linked, done int64
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE company_id=? AND project_id=? AND (body->>'openspec_change')=?
 AND (status NOT IN ('DONE','CANCELLED') OR (body->>'updated_at')::bigint>=?)`, company, p.ID, change, since).Scan(&linked); err != nil {
				return 0, err
			}
			if err = tx.QueryRowContext(ctx, "SELECT coalesce(max(orphan_reported_at),0) FROM openspec_changes WHERE company_id=? AND project_id=? AND change=?", company, p.ID, change).Scan(&done); err != nil {
				return 0, err
			}
			if linked > 0 || done > 0 {
				continue
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO openspec_changes(company_id,project_id,change,orphan_reported_at) VALUES(?,?,?,?)
 ON CONFLICT (company_id,project_id,change) DO UPDATE SET orphan_reported_at=excluded.orphan_reported_at`, company, p.ID, change, tx.now().Unix()); err != nil {
				return 0, err
			}
			summary := fmt.Sprintf("for %s: OpenSpec change %s of project %s is not archived and no task works on it; create tasks for it or archive it", manager, change, p.ID)
			if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "openspec.change_orphaned", EntityID: change, ProjectID: p.ID, CreatedAt: tx.now(),
				Agent: manager, Actor: "system", Summary: summary, Attention: true, Ref: p.OpenSpecDir() + "/changes/" + change}); err != nil {
				return 0, err
			}
			reported++
		}
	}
	return reported, tx.Commit()
}

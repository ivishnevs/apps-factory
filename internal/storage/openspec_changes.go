package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// OpenSpec change tracking (openspec-in-git D2, D3): Core knows a project's changes from the tasks linking
// them; what only git knows (active, archived, unchecked items) arrives with the facts of a done exit. When
// the last open task of a change is DONE and the change is still active, the manager gets one feed event;
// a daily check reminds once more of a change that stayed so.

// OrphanWindow: a change linked by a task closed within this window still counts as worked on.
const OrphanWindow = 7 * 24 * time.Hour

// OpenSpecFacts are the node's facts about the task's linked change at a done exit.
type OpenSpecFacts struct {
	Change    string `json:"change"`
	State     string `json:"state"` // active, archived or missing
	Unchecked int    `json:"unchecked"`
	Ref       string `json:"ref,omitempty"`
}

func validOpenSpecFacts(f *OpenSpecFacts) bool {
	return f != nil && (f.State == "active" || f.State == "archived" || f.State == "missing") && f.Unchecked >= 0
}

// recordChangeFacts keeps the state a done exit reported for the task's linked change.
func recordChangeFacts(ctx context.Context, tx *Tx, t tasks.Task, f *ExitFacts) error {
	if t.OpenSpecChange == "" || f == nil || !validOpenSpecFacts(f.OpenSpec) || f.OpenSpec.Change != t.OpenSpecChange {
		return nil
	}
	o := f.OpenSpec
	_, err := tx.ExecContext(ctx, `INSERT INTO openspec_changes(company_id,project_id,change,state,unchecked,reported_task_id,reported_at) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT (company_id,project_id,change) DO UPDATE SET state=excluded.state,unchecked=excluded.unchecked,reported_task_id=excluded.reported_task_id,
 reported_at=excluded.reported_at,orphan_reported_at=0`, tx.company, t.ProjectID, o.Change, o.State, o.Unchecked, t.ID, tx.now().Unix())
	return err
}

// changeTaskDone runs in closedTask: done is DONE. When it was the last open task linking its change and the
// change's last reported state is active, the manager of its assignee gets one feed event.
func changeTaskDone(ctx context.Context, tx *Tx, done tasks.Task) error {
	change := done.OpenSpecChange
	if change == "" || done.Status != "DONE" {
		return nil
	}
	var open int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM tasks WHERE company_id=? AND project_id=? AND (body->>'openspec_change')=? AND status NOT IN ('DONE','CANCELLED')", tx.company, done.ProjectID, change).Scan(&open); err != nil || open > 0 {
		return err
	}
	var state, noticed string
	var unchecked int
	err := tx.QueryRowContext(ctx, "SELECT state,unchecked,notice_task_id FROM openspec_changes WHERE company_id=? AND project_id=? AND change=?", tx.company, done.ProjectID, change).Scan(&state, &unchecked, &noticed)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (state != "active" || noticed == done.ID) {
		return nil
	}
	if err != nil {
		return err
	}
	manager := "owner"
	if tx.store.ManagerOf != nil {
		if m := tx.store.ManagerOf(tx.company, done.Assignee); m != "" {
			manager = m
		}
	}
	typ, summary := "openspec.change_unarchived", fmt.Sprintf("for %s: every task of OpenSpec change %s is done (the last: %s) but the change was not archived; archive it (openspec archive %s in a task's pull request)", manager, change, done.ID, change)
	if unchecked > 0 {
		typ, summary = "openspec.change_unfinished", fmt.Sprintf("for %s: every task of OpenSpec change %s is done (the last: %s) but its tasks.md has %d unchecked items; create tasks for them, or update tasks.md and archive the change", manager, change, done.ID, unchecked)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE openspec_changes SET notice_task_id=? WHERE company_id=? AND project_id=? AND change=?", done.ID, tx.company, done.ProjectID, change); err != nil {
		return err
	}
	return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: typ, EntityID: change, ProjectID: done.ProjectID, TaskID: done.ID, CreatedAt: tx.now(),
		Agent: manager, Actor: "system", Summary: summary, Attention: true, Ref: "openspec/changes/" + change})
}

// ReportOrphanChanges is the daily check: a change whose last reported state is active, that no open task
// and no task closed within OrphanWindow links, gets one more feed event for manager (the default report),
// once until new facts arrive.
func (s *Store) ReportOrphanChanges(ctx context.Context, company, manager string) (int, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if manager == "" {
		manager = "owner"
	}
	since := tx.now().Add(-OrphanWindow).Unix()
	rows, err := tx.QueryContext(ctx, `SELECT c.project_id, c.change, c.unchecked FROM openspec_changes c WHERE c.company_id=? AND c.state='active' AND c.orphan_reported_at=0
 AND NOT EXISTS (SELECT 1 FROM tasks t WHERE t.company_id=c.company_id AND t.project_id=c.project_id AND (t.body->>'openspec_change')=c.change
 AND (t.status NOT IN ('DONE','CANCELLED') OR (t.body->>'updated_at')::bigint>=?)) ORDER BY c.project_id, c.change`, company, since)
	if err != nil {
		return 0, err
	}
	type orphan struct {
		project, change string
		unchecked       int
	}
	var found []orphan
	for rows.Next() {
		var o orphan
		if err = rows.Scan(&o.project, &o.change, &o.unchecked); err != nil {
			rows.Close()
			return 0, err
		}
		found = append(found, o)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, o := range found {
		if _, err = tx.ExecContext(ctx, "UPDATE openspec_changes SET orphan_reported_at=? WHERE company_id=? AND project_id=? AND change=?", tx.now().Unix(), company, o.project, o.change); err != nil {
			return 0, err
		}
		summary := fmt.Sprintf("for %s: OpenSpec change %s of project %s is not archived (%d unchecked items) and no task works on it; create tasks for it or archive it", manager, o.change, o.project, o.unchecked)
		if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "openspec.change_orphaned", EntityID: o.change, ProjectID: o.project, CreatedAt: tx.now(),
			Agent: manager, Actor: "system", Summary: summary, Attention: true, Ref: "openspec/changes/" + o.change}); err != nil {
			return 0, err
		}
	}
	return len(found), tx.Commit()
}

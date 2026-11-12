package storage

import (
	"context"

	"appfactory/internal/protocol"
)

// Alert is one stall condition on a task; RefID names what it waits on (e.g. the stuck dependency).
type Alert struct {
	TaskID  string `json:"task_id"`
	RefID   string `json:"ref_id,omitempty"`
	Summary string `json:"summary"`
}

// SyncAlerts makes the active alerts of a kind equal to want: a new condition raises an alert and an
// attention feed event once per episode; a condition that went away clears its alert.
func (s *Store) SyncAlerts(ctx context.Context, company, kind string, want []Alert) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := tx.now()
	rows, err := tx.QueryContext(ctx, "SELECT id,task_id,ref_id,summary FROM task_alerts WHERE company_id=? AND kind=? AND status='active' ORDER BY raised_at,id", company, kind)
	if err != nil {
		return err
	}
	type active struct{ id, task, ref, summary string }
	current := map[string]active{}
	for rows.Next() {
		var a active
		if err = rows.Scan(&a.id, &a.task, &a.ref, &a.summary); err != nil {
			rows.Close()
			return err
		}
		current[a.task+"\x00"+a.ref] = a
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	notify := func(taskID, ref, typ, summary string, attention bool) error {
		t, err := taskTx(ctx, tx, taskID)
		if err != nil {
			return err
		}
		return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: typ, EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: now, TaskID: t.ID, Agent: t.Assignee, Status: t.Status, Summary: summary, Attention: attention, Ref: ref})
	}
	for _, a := range want {
		key := a.TaskID + "\x00" + a.RefID
		if _, ok := current[key]; ok {
			delete(current, key)
			continue
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO task_alerts(id,company_id,task_id,kind,ref_id,summary,status,raised_at) VALUES(?,?,?,?,?,?,'active',?)", protocol.ID(), company, a.TaskID, kind, a.RefID, a.Summary, now.Unix()); err != nil {
			return err
		}
		if err = notify(a.TaskID, a.RefID, "task.alert."+kind, a.Summary, true); err != nil {
			return err
		}
	}
	for _, a := range current {
		if _, err = tx.ExecContext(ctx, "UPDATE task_alerts SET status='cleared',cleared_at=? WHERE id=?", now.Unix(), a.id); err != nil {
			return err
		}
		if err = notify(a.task, a.ref, "task.alert_cleared."+kind, a.summary, false); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ActiveAlert is one item of the Inbox's Alerts section (inbox-badge-parity): an active task alert of any kind, or a
// run parked for inspection (kind inspect). Core adds Action (how it clears, in words) and With (the lead expected to
// act, "" for the owner).
type ActiveAlert struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	ProjectID string `json:"project_id"`
	Assignee  string `json:"assignee,omitempty"`
	RefID     string `json:"ref_id,omitempty"`
	Summary   string `json:"summary"`
	Since     int64  `json:"since"`
	Action    string `json:"action,omitempty"`
	With      string `json:"with,omitempty"`
}

// ActiveAlerts lists every active task alert, whatever its kind, and every active inspect park, newest first: what the
// owner's pending count counts besides the Inbox's messages and reviews.
func (s *Store) ActiveAlerts(ctx context.Context, company string) ([]ActiveAlert, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id,a.kind,a.task_id,coalesce(t.body->>'title',''),t.project_id,t.assignee,a.ref_id,a.summary,a.raised_at
 FROM task_alerts a JOIN tasks t ON t.company_id=a.company_id AND t.id=a.task_id WHERE a.company_id=? AND a.status='active'
 UNION ALL SELECT p.id,'inspect',p.task_id,coalesce(t.body->>'title',''),t.project_id,t.assignee,p.ref_id,p.reason,p.created_at
 FROM task_parks p JOIN tasks t ON t.company_id=p.company_id AND t.id=p.task_id WHERE p.company_id=? AND p.status='active' AND p.kind='inspect'
 ORDER BY 9 DESC,1`, company, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ActiveAlert{}
	for rows.Next() {
		var a ActiveAlert
		if err = rows.Scan(&a.ID, &a.Kind, &a.TaskID, &a.TaskTitle, &a.ProjectID, &a.Assignee, &a.RefID, &a.Summary, &a.Since); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

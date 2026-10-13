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
	rows, err := tx.QueryContext(ctx, "SELECT id,task_id,ref_id,summary FROM task_alerts WHERE company_id=? AND kind=? AND status='active'", company, kind)
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

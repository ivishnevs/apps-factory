package storage

import (
	"context"
	"strings"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Trigger log and parks (trigger-log-scheduling D1-D2) in shadow mode: every cause records its
// trigger or park in its own transaction, and tasks.Evaluate reads them; the scheduler still
// decides from task status.

func isOwner(actor string) bool { return actor == "" || actor == "owner" }

// addTrigger appends a trigger and raises the task's wake_seq.
func addTrigger(ctx context.Context, tx *Tx, t tasks.Task, kind string, authority bool, actor, ref, body string) error {
	if actor == "" {
		actor = "owner"
	}
	now := tx.now()
	res, err := tx.ExecContext(ctx, "INSERT INTO task_triggers(company_id,task_id,kind,authority,actor,ref_id,body,created_at) VALUES(?,?,?,?,?,?,?,?)", tx.company, t.ID, kind, authority, actor, ref, shortText(body, 4096), now.Unix())
	if err != nil {
		return err
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE tasks SET wake_seq=? WHERE company_id=? AND id=?", seq, tx.company, t.ID); err != nil {
		return err
	}
	return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task.trigger." + kind, EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: now, TaskID: t.ID, Agent: t.Assignee, Actor: actor, Status: t.Status, Summary: body, Ref: ref})
}

// addPark holds the task; an identical active park (task, kind, ref) is kept as is. attention marks
// the feed event of a park that waits on the owner.
func addPark(ctx context.Context, tx *Tx, t tasks.Task, kind, ref, reason string, attention bool) error {
	now := tx.now()
	res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO task_parks(id,company_id,task_id,kind,ref_id,reason,park_seq,status,created_at)
 SELECT ?,?,?,?,?,?,wake_seq,'active',? FROM tasks WHERE company_id=? AND id=?`, protocol.ID(), tx.company, t.ID, kind, ref, reason, now.Unix(), tx.company, t.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task.parked." + kind, EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: now, TaskID: t.ID, Agent: t.Assignee, Status: t.Status, Summary: reason, Ref: ref, Attention: attention})
}

// anyRef matches every park of a kind; anyKind matches every kind.
const anyRef, anyKind = "*", ""

// clearParks ends active parks of the task: cleared when their cause is resolved, overridden when
// the task is closed around them.
func clearParks(ctx context.Context, tx *Tx, t tasks.Task, kind, ref, status string) error {
	now := tx.now()
	res, err := tx.ExecContext(ctx, "UPDATE task_parks SET status=?,cleared_at=? WHERE company_id=? AND task_id=? AND status='active' AND (?='' OR kind=?) AND (?='*' OR ref_id=?)", status, now.Unix(), tx.company, t.ID, kind, kind, ref, ref)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	label := kind
	if label == anyKind {
		label = "all"
	}
	return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task.unparked." + label, EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: now, TaskID: t.ID, Agent: t.Assignee, Status: t.Status, Summary: status, Ref: ref})
}

// closedTask runs what follows a task reaching DONE or CANCELLED by any path: its parks are
// overridden, dependents whose last dependency it was wake up, and a parent whose last open child
// it was wakes up.
func closedTask(ctx context.Context, tx *Tx, t tasks.Task) error {
	if t.Status != "DONE" && t.Status != "CANCELLED" {
		return nil
	}
	if err := clearParks(ctx, tx, t, anyKind, anyRef, "overridden"); err != nil {
		return err
	}
	escalations, err := listEscalations(ctx, tx, "WHERE company_id=? AND task_id=? AND status='open'", tx.company, t.ID)
	if err != nil {
		return err
	}
	for _, e := range escalations {
		if err = closeEscalation(ctx, tx, e, "cleared", "", "system", "task "+t.Status); err != nil {
			return err
		}
	}
	if t.Status == "DONE" {
		if err := dependencyDone(ctx, tx, t); err != nil {
			return err
		}
	}
	if t.ParentTaskID == "" {
		return nil
	}
	parent, err := taskTx(ctx, tx, t.ParentTaskID)
	if err != nil || parent.Status == "DONE" || parent.Status == "CANCELLED" {
		return err
	}
	var open int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM tasks WHERE company_id=? AND json_extract(body,'$.parent_task_id')=? AND status NOT IN ('DONE','CANCELLED')", tx.company, parent.ID).Scan(&open); err != nil || open > 0 {
		return err
	}
	return addTrigger(ctx, tx, parent, "last_child_closed", false, "system", t.ID, "last open child "+t.ID+" is "+t.Status)
}

// dependencyDone fires dependency_done once on each dependent whose last open dependency just
// became DONE.
func dependencyDone(ctx context.Context, tx *Tx, done tasks.Task) error {
	dependents, err := listDocs[tasks.Task](ctx, tx, `SELECT t.body FROM task_dependencies d JOIN tasks t ON t.id=d.task_id
 WHERE d.company_id=? AND d.depends_on=? AND t.status NOT IN ('DONE','CANCELLED')
 AND NOT EXISTS (SELECT 1 FROM task_dependencies o JOIN tasks ot ON ot.id=o.depends_on WHERE o.task_id=t.id AND ot.status!='DONE')`, tx.company, done.ID)
	if err != nil {
		return err
	}
	for _, t := range dependents {
		if err = addTrigger(ctx, tx, t, "dependency_done", false, "system", done.ID, "last open dependency "+done.ID+" is DONE"); err != nil {
			return err
		}
	}
	return nil
}

// artifactApproved fires artifact_approved on open tasks of the project that require the version.
func artifactApproved(ctx context.Context, tx *Tx, project string, a tasks.Artifact, approvalID string) error {
	open, err := listDocs[tasks.Task](ctx, tx, "SELECT body FROM tasks WHERE company_id=? AND project_id=? AND status NOT IN ('DONE','CANCELLED')", tx.company, project)
	if err != nil {
		return err
	}
	for _, t := range open {
		for _, ref := range t.RequiredArtifacts {
			if ref == a {
				if err = addTrigger(ctx, tx, t, "artifact_approved", true, "owner", approvalID, "artifact "+a.ID+"@"+a.Version+" approved"); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

// CommentTask adds a comment. Only a comment by an authority (the owner, or an agent with authority over the
// task, decided by Core) wakes an open task with an authority trigger (explicit-exits-authority D8); other
// comments are stored and listed in the next brief, and an agent's own comment never wakes its task.
func (s *Store) CommentTask(ctx context.Context, company, id string, in tasks.Comment) (tasks.Task, error) {
	return mutate(ctx, s, company, "task.comment:"+id, in.RequestID, in, func(tx *Tx) (tasks.Task, error) {
		t, err := taskTx(ctx, tx, id)
		if err != nil {
			return t, err
		}
		if strings.TrimSpace(in.Text) == "" || len(in.Text) > 16*1024 {
			return t, inputError("comment text up to 16 KiB required")
		}
		actor := in.Actor
		if actor == "" {
			actor = "owner"
		}
		if err = comment(ctx, tx, t.ID, actor, in.Text); err != nil {
			return t, err
		}
		authority := in.Authority || isOwner(actor)
		if t.Status == "DONE" || t.Status == "CANCELLED" || actor == t.Assignee || !authority {
			return t, feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task.commented", EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: tx.now(), TaskID: t.ID, Agent: t.Assignee, Actor: actor, Status: t.Status, Summary: in.Text})
		}
		return t, addTrigger(ctx, tx, t, "owner_comment", true, actor, "", in.Text)
	})
}

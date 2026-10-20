package storage

import (
	"context"
	"errors"
	"slices"
	"strings"

	"appfactory/internal/approvals"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Trigger log and parks (trigger-log-scheduling D1-D2) in shadow mode: every cause records its
// trigger or park in its own transaction, and tasks.Evaluate reads them; the scheduler still
// decides from task status.

func isOwner(actor string) bool { return actor == "" || actor == "owner" }

// addTrigger appends a trigger and raises the task's wake_seq. Authority input also overrides the task's
// interruptible parks and counts as progress (overrideParks); *t is then the saved task.
func addTrigger(ctx context.Context, tx *Tx, t *tasks.Task, kind string, authority bool, actor, ref, body string) error {
	if actor == "" {
		actor = "owner"
	}
	now := tx.now()
	// The addressee is the task's assignee when the trigger is written (the agent a handoff or review goes to).
	var seq int64
	var err error
	if err = tx.QueryRowContext(ctx, "INSERT INTO task_triggers(company_id,task_id,kind,authority,actor,ref_id,body,created_at,addressee) VALUES(?,?,?,?,?,?,?,?,?) RETURNING seq", tx.company, t.ID, kind, authority, actor, ref, shortText(body, 4096), now.Unix(), t.Assignee).Scan(&seq); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE tasks SET wake_seq=? WHERE company_id=? AND id=?", seq, tx.company, t.ID); err != nil {
		return err
	}
	// Authority input ends an active wait early (waits-events-cost-cli D1); so does any new input on a
	// conversation task (a message or a closed delegated task waits for no timer, hierarchy D5).
	if authority || t.Kind == tasks.KindConversation && kind != "wait_ended" {
		if err = interruptWait(ctx, tx, *t, actor, kind); err != nil {
			return err
		}
	}
	if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task.trigger." + kind, EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: now, TaskID: t.ID, Agent: t.Assignee, Actor: actor, Status: t.Status, Summary: body, Ref: ref}); err != nil || !authority {
		return err
	}
	return overrideParks(ctx, tx, t, actor, kind)
}

// overrideParks applies authority input to a parked task in the trigger's transaction
// (explicit-exits-authority D7): an ask's question is superseded, a block's dependencies and a children
// park are overridden, the retries_exhausted, budget_exhausted and no_exit escalations clear (the input
// counts as a retry), and the retry counters, backoff and today's budget reset (progress). addTrigger
// interrupts the wait. inspect, the owner's manual block and system blocks (openspec, a workspace reset)
// keep their parks: they need their own answer.
func overrideParks(ctx context.Context, tx *Tx, t *tasks.Task, actor, kind string) error {
	if t.Status == "DONE" || t.Status == "CANCELLED" {
		return nil
	}
	why := kind + " from " + actor
	if kind != "approval_decided" {
		if err := clearParks(ctx, tx, *t, tasks.ParkChildren, anyRef, "overridden"); err != nil {
			return err
		}
	}
	waiting, err := askPark(ctx, tx, tx.company, t.ID)
	if err != nil {
		return err
	}
	if waiting != "" && t.Status == "READY" { // the question of an ended run; a live run's own question stays
		m, err := messageTx(ctx, tx, waiting)
		if err != nil {
			return err
		}
		if m.Status == "PENDING" || m.Status == "UNREAD" || m.Status == "ESCALATED" {
			m.Status, m.DeliveryStatus, m.EscalationReason = "WITHDRAWN", "NONE", "superseded by "+why
			if err = saveMessage(ctx, tx, &m, "message.withdrawn"); err != nil {
				return err
			}
		}
		if err = clearParks(ctx, tx, *t, tasks.ParkAsk, anyRef, "overridden"); err != nil {
			return err
		}
	}
	if kind == "approval_decided" {
		// An approval decision wakes the requesting task from its wait or ask (addTrigger and above) but redirects
		// nothing else: blocks, escalations and a lost run's retry backoff stay (no progress).
		return nil
	}
	if err = clearRetryEscalations(ctx, tx, *t, why); err != nil {
		return err
	}
	if err = clearOpenEscalation(ctx, tx, *t, "no_exit", why); err != nil {
		return err
	}
	fresh, err := taskTx(ctx, tx, t.ID) // the cleared escalations may have saved the task
	if err != nil {
		return err
	}
	*t = fresh
	changed := t.AttemptsNoProgress > 0 || t.FailStreak > 0 || t.NotBefore > 0
	if len(t.BlockedOn) > 0 {
		// The block's own dependencies go; the task's other dependencies stay.
		t.Dependencies = slices.DeleteFunc(slices.Clone(t.Dependencies), func(d string) bool { return slices.Contains(t.BlockedOn, d) })
		t.BlockedOn = nil
		if err = dependencies(ctx, tx, *t); err != nil {
			return err
		}
		if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task.unparked.block", EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: tx.now(), TaskID: t.ID, Agent: t.Assignee, Actor: actor, Status: t.Status, Summary: "overridden by " + why}); err != nil {
			return err
		}
		changed = true
	}
	if t.Status == "BLOCKED" && strings.HasPrefix(t.BlockReason, "children:") {
		t.Status, t.BlockReason = "READY", ""
		changed = true
	}
	if t.Status == "IN_PROGRESS" && t.CurrentRunID != "" {
		// A run that ended without moving the task (no_exit) left it IN_PROGRESS: the input lets it run again.
		var live bool
		if err = tx.QueryRowContext(ctx, "SELECT status!='ENDED' OR unreleased=1 FROM runs WHERE id=?", t.CurrentRunID).Scan(&live); err != nil {
			return err
		}
		if !live {
			t.Status, changed = "READY", true
		}
	}
	// Progress: the counters, the backoff and today's budget reset. The task is saved when its body changes;
	// otherwise progress_at is stamped in place (no new version for a plain comment on a running task).
	if err = resetRetry(ctx, tx, t); err != nil {
		return err
	}
	if changed {
		return saveTask(ctx, tx, t, "task.updated")
	}
	if t.CurrentRunID == "" {
		return nil // a task that never ran has no progress to record
	}
	_, err = tx.ExecContext(ctx, "UPDATE tasks SET body=(body::jsonb||jsonb_build_object('progress_at',?::bigint))::json WHERE company_id=? AND id=?", t.ProgressAt, tx.company, t.ID)
	return err
}

// addPark holds the task; an identical active park (task, kind, ref) is kept as is. attention marks
// the feed event of a park that waits on the owner.
func addPark(ctx context.Context, tx *Tx, t tasks.Task, kind, ref, reason string, attention bool) error {
	now := tx.now()
	res, err := tx.ExecContext(ctx, `INSERT INTO task_parks(id,company_id,task_id,kind,ref_id,reason,park_seq,status,created_at)
 SELECT ?,?,?,?,?,?,wake_seq,'active',? FROM tasks WHERE company_id=? AND id=? ON CONFLICT DO NOTHING`, protocol.ID(), tx.company, t.ID, kind, ref, reason, now.Unix(), tx.company, t.ID)
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
	if err := endEpisode(ctx, tx, t.ID); err != nil {
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
	// Open questions of a closed task are cancelled (hierarchy D3); approval requests keep their own lifecycle.
	questions, err := listDocs[messaging.Message](ctx, tx, "SELECT body FROM messages WHERE company_id=? AND task_id=? AND message_type='question' AND status IN ('PENDING','UNREAD','ESCALATED') AND id NOT IN (SELECT message_id FROM approvals WHERE company_id=? AND message_id IS NOT NULL) ORDER BY seq", tx.company, t.ID, tx.company)
	if err != nil {
		return err
	}
	for _, m := range questions {
		m.Status, m.DeliveryStatus, m.EscalationReason = "CANCELLED", "NONE", "task "+t.ID+" is "+t.Status
		if err = saveMessage(ctx, tx, &m, "message.cancelled"); err != nil {
			return err
		}
	}
	if err = closedConversationEffects(ctx, tx, t); err != nil {
		return err
	}
	if t.Status == "DONE" {
		if err := dependencyDone(ctx, tx, t); err != nil {
			return err
		}
		if err := archiveChangeTask(ctx, tx, t); err != nil {
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
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM tasks WHERE company_id=? AND (body->>'parent_task_id')=? AND status NOT IN ('DONE','CANCELLED')", tx.company, parent.ID).Scan(&open); err != nil || open > 0 {
		return err
	}
	// A parent that blocked on its children (factory_exit_block on_children) continues.
	if err = clearParks(ctx, tx, parent, tasks.ParkChildren, anyRef, "cleared"); err != nil {
		return err
	}
	if parent.Status == "BLOCKED" && strings.HasPrefix(parent.BlockReason, "children:") {
		parent.Status, parent.BlockReason = "READY", ""
		if err = saveTask(ctx, tx, &parent, "task.updated"); err != nil {
			return err
		}
	}
	return addTrigger(ctx, tx, &parent, "last_child_closed", false, "system", t.ID, "last open child "+t.ID+" is "+t.Status)
}

// dependencyDone fires dependency_done once on each dependent whose last open dependency just
// became DONE.
func dependencyDone(ctx context.Context, tx *Tx, done tasks.Task) error {
	dependents, err := listDocs[tasks.Task](ctx, tx, `SELECT t.body FROM task_dependencies d JOIN tasks t ON t.id=d.task_id
 WHERE d.company_id=? AND d.depends_on=? AND t.status NOT IN ('DONE','CANCELLED')
 AND NOT EXISTS (SELECT 1 FROM task_dependencies o JOIN tasks ot ON ot.id=o.depends_on WHERE o.task_id=t.id AND ot.status!='DONE') ORDER BY t.priority DESC,t.created_at,t.id`, tx.company, done.ID)
	if err != nil {
		return err
	}
	for _, t := range dependents {
		if err = addTrigger(ctx, tx, &t, "dependency_done", false, "system", done.ID, "last open dependency "+done.ID+" is DONE"); err != nil {
			return err
		}
	}
	return nil
}

// artifactApproved fires the authority trigger artifact_approved on open tasks of the project that require
// the version or are pinned to the artifact (they require, and their runs pin, another version of it: a new
// approved version is input for them, explicit-exits-authority D6). urgent stops their live runs like an
// urgent directive. The requesting task (requester) is woken by approvalDecided instead.
func artifactApproved(ctx context.Context, tx *Tx, project string, a tasks.Artifact, approvalID, requester string, urgent bool) error {
	open, err := listDocs[tasks.Task](ctx, tx, "SELECT body FROM tasks WHERE company_id=? AND project_id=? AND status NOT IN ('DONE','CANCELLED') ORDER BY status,priority DESC,created_at,id", tx.company, project)
	if err != nil {
		return err
	}
	for _, t := range open {
		if t.ID == requester {
			continue
		}
		text := ""
		for _, ref := range t.RequiredArtifacts {
			if sameArtifact(a, ref) {
				text = "artifact " + a.ID + "@" + a.Version + " approved"
				break
			}
			if ref.ID == a.ID {
				text = "artifact " + a.ID + "@" + a.Version + " approved (a new version of " + ref.ID + "@" + ref.Version + ", which this task uses)"
			}
		}
		if text == "" {
			continue
		}
		if err = addTrigger(ctx, tx, &t, "artifact_approved", true, "owner", approvalID, text); err != nil {
			return err
		}
		if urgent {
			if err = stopUrgently(ctx, tx, t, "artifact_approved"); err != nil {
				return err
			}
		}
	}
	return nil
}

// approvalDecided fires the authority trigger approval_decided on the open task that requested the approval
// (approval.task_id) when the owner approves or rejects it: the decision is the owner's input on that task, so
// it overrides its wait and ask parks like a directive (explicit-exits-authority D7) and the next brief shows
// it with the reason. An urgent artifact approval stops the task's live run as in artifactApproved.
func approvalDecided(ctx context.Context, tx *Tx, a approvals.Approval, urgent bool) error {
	if a.TaskID == "" {
		return nil
	}
	t, err := taskTx(ctx, tx, a.TaskID)
	if errors.Is(err, ErrNotFound) || err == nil && (t.Status == "DONE" || t.Status == "CANCELLED") {
		return nil
	}
	if err != nil {
		return err
	}
	what := "approval " + a.ID
	switch {
	case a.Kind == "artifact" && a.Artifact != nil:
		what = "artifact approval " + a.ID + " of " + a.Artifact.ID + "@" + a.Artifact.Version
	case a.Kind == "command":
		what = "command approval " + a.ID
	case a.Call.Name != "":
		what = "approval " + a.ID + " of " + a.Call.Name
	}
	text := what + " " + strings.ToLower(a.Status) + " by the owner: " + a.DecisionReason
	if err = addTrigger(ctx, tx, &t, "approval_decided", true, "owner", a.ID, text); err != nil {
		return err
	}
	if urgent && a.Kind == "artifact" && a.Status == "APPROVED" {
		return stopUrgently(ctx, tx, t, "artifact_approved")
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
		return t, addTrigger(ctx, tx, &t, "owner_comment", true, actor, "", in.Text)
	})
}

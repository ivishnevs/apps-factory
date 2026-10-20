package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Authority actions (hierarchy D2): the owner and the managers up an assignee's chain pause, resume, reassign,
// reprioritize and cancel the assignee's tasks; Core checks who may act and passes the actor's rank. Pause,
// resume and reprioritize are actions, not triggers: they never wake a task or override its parks (an active wait
// keeps its timer and episode). A reassignment is an authority trigger (assignment) and ends the wait episode;
// cancel ends the task (its parks and wait episode with it).

// ErrResumeRank refuses a resume by an actor below the pauser.
var ErrResumeRank = fmt.Errorf("%w: only the pauser or a higher authority resumes the task", ErrForbidden)

// taskPause is the authority pause of the task, nil when none.
func taskPause(ctx context.Context, q querier, company, task string) (*tasks.TaskPause, error) {
	var p tasks.TaskPause
	err := q.QueryRowContext(ctx, "SELECT actor,rank,reason FROM task_pauses WHERE company_id=? AND task_id=?", company, task).Scan(&p.Actor, &p.Rank, &p.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &p, err
}

// authorityTask loads an open task at the expected version.
func authorityTask(ctx context.Context, tx *Tx, id string, version int) (tasks.Task, error) {
	t, err := taskTx(ctx, tx, id)
	if err != nil {
		return t, err
	}
	if t.Version != version || t.Status == "DONE" || t.Status == "CANCELLED" {
		return t, ErrConflict
	}
	return t, nil
}

func actorName(actor string) string {
	if actor == "" {
		return "owner"
	}
	return actor
}

// authorityFeed records an authority action for the assignee's chain (every action is an event, D2).
func authorityFeed(ctx context.Context, tx *Tx, t tasks.Task, kind, actor, summary string) error {
	return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task." + kind, EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: tx.now(), TaskID: t.ID, Agent: t.Assignee, Actor: actorName(actor), Status: t.Status, Summary: summary})
}

// PauseTask holds the task from new runs (paused:task); a live run continues. Pausing a paused task again
// keeps the first pause.
func (s *Store) PauseTask(ctx context.Context, company, id string, in tasks.Authority) (tasks.Task, error) {
	return mutate(ctx, s, company, "task.pause:"+id, in.RequestID, in, func(tx *Tx) (tasks.Task, error) {
		t, err := authorityTask(ctx, tx, id, in.Version)
		if err != nil {
			return t, err
		}
		if t.Kind == tasks.KindConversation {
			return t, inputError("a conversation task is not paused; pause the agent")
		}
		res, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO task_pauses(company_id,task_id,actor,rank,reason,created_at) VALUES(?,?,?,?,?,?)", tx.company, t.ID, actorName(in.Actor), in.Rank, strings.TrimSpace(in.Reason), tx.now().Unix())
		if err != nil {
			return t, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return t, nil
		}
		if err = comment(ctx, tx, t.ID, actorName(in.Actor), "paused: "+in.Reason); err != nil {
			return t, err
		}
		if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
			return t, err
		}
		return t, authorityFeed(ctx, tx, t, "paused", in.Actor, strings.TrimSpace("paused by "+actorName(in.Actor)+" "+in.Reason))
	})
}

// ResumeTask removes the task's pause: the pauser or a higher authority (a smaller rank) may. The task runs
// again only with unhandled triggers.
func (s *Store) ResumeTask(ctx context.Context, company, id string, in tasks.Authority) (tasks.Task, error) {
	return mutate(ctx, s, company, "task.resume:"+id, in.RequestID, in, func(tx *Tx) (tasks.Task, error) {
		t, err := authorityTask(ctx, tx, id, in.Version)
		if err != nil {
			return t, err
		}
		p, err := taskPause(ctx, tx, tx.company, t.ID)
		if err != nil || p == nil {
			return t, err
		}
		if p.Actor != actorName(in.Actor) && in.Rank >= p.Rank {
			return t, ErrResumeRank
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM task_pauses WHERE company_id=? AND task_id=?", tx.company, t.ID); err != nil {
			return t, err
		}
		if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
			return t, err
		}
		return t, authorityFeed(ctx, tx, t, "resumed", in.Actor, "resumed by "+actorName(in.Actor)+" (paused by "+p.Actor+")")
	})
}

// ReprioritizeTask changes the task's priority (-1000..1000) in any open state; parks and runs stay as they are.
func (s *Store) ReprioritizeTask(ctx context.Context, company, id string, in tasks.Authority) (tasks.Task, error) {
	return mutate(ctx, s, company, "task.reprioritize:"+id, in.RequestID, in, func(tx *Tx) (tasks.Task, error) {
		t, err := authorityTask(ctx, tx, id, in.Version)
		if err != nil {
			return t, err
		}
		if in.Priority == nil || *in.Priority < -1000 || *in.Priority > 1000 {
			return t, inputError("priority -1000..1000 required")
		}
		old := t.Priority
		t.Priority = *in.Priority
		if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
			return t, err
		}
		return t, authorityFeed(ctx, tx, t, "reprioritized", in.Actor, fmt.Sprintf("priority %d → %d by %s", old, t.Priority, actorName(in.Actor)))
	})
}

// ReassignTask gives the task to another agent (Core checked the new assignee is in the actor's subtree). A
// queued run ends stale and a running one is revoked; the task is READY for the new assignee once its run has
// ended, with an authority assignment trigger.
func (s *Store) ReassignTask(ctx context.Context, company, id string, in tasks.Authority) (tasks.Task, error) {
	return mutate(ctx, s, company, "task.reassign:"+id, in.RequestID, in, func(tx *Tx) (tasks.Task, error) {
		t, err := authorityTask(ctx, tx, id, in.Version)
		if err != nil {
			return t, err
		}
		if t.Kind == tasks.KindConversation {
			return t, inputError("a conversation task belongs to its agent")
		}
		if !entityName.MatchString(in.Assignee) {
			return t, inputError("assignee required")
		}
		if in.Assignee == t.Assignee {
			return t, nil
		}
		return reassignTx(ctx, tx, t, in.Assignee, actorName(in.Actor), in.Reason)
	})
}

// reassignTx gives the open task t to assignee (the checks are the caller's): a queued run ends stale, a running
// one is revoked, counters and retry escalations reset, the wait episode ends, and the new assignee gets an
// authority assignment trigger (which also ends an active wait). Shared by the reassign action and a wait
// escalation's reassign answer.
func reassignTx(ctx context.Context, tx *Tx, t tasks.Task, assignee, actor, reason string) (tasks.Task, error) {
	id := t.ID
	var err error
	if t.CurrentRunID != "" {
		var status string
		var unreleased bool
		if err = tx.QueryRowContext(ctx, "SELECT status,unreleased FROM runs WHERE id=?", t.CurrentRunID).Scan(&status, &unreleased); err != nil {
			return t, err
		}
		switch {
		case unreleased:
			return t, fmt.Errorf("%w: the task's lost run may still run; resolve it first (factory runs resolve)", ErrConflict)
		case status == "QUEUED":
			if _, err = endQueued(ctx, tx, t.CurrentRunID, "stale", "task reassigned to "+assignee); err != nil {
				return t, err
			}
			if t, err = taskTx(ctx, tx, id); err != nil {
				return t, err
			}
		case status == "CLAIMED" || status == "RUNNING":
			// The revoked run returns the task to READY when it ends (a revoked run stops the run, not the task).
			if _, err = tx.ExecContext(ctx, "UPDATE runs SET status='CANCELLING',revoked=1,stop_reason='reassigned' WHERE id=?", t.CurrentRunID); err != nil {
				return t, err
			}
		}
	}
	previous := t.Assignee
	t.Assignee = assignee
	t.AttemptsNoProgress, t.FailStreak, t.NotBefore = 0, 0, 0
	if t.Status == "IN_REVIEW" {
		t.Reviewer = ""
	}
	if err = clearRetryEscalations(ctx, tx, t, "reassigned to "+t.Assignee); err != nil {
		return t, err
	}
	if err = comment(ctx, tx, t.ID, actor, strings.TrimSpace("reassigned from "+previous+" to "+t.Assignee+". "+reason)); err != nil {
		return t, err
	}
	if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
		return t, err
	}
	if err = authorityFeed(ctx, tx, t, "reassigned", actor, "reassigned from "+previous+" to "+t.Assignee+" by "+actor); err != nil {
		return t, err
	}
	// A new assignee starts without the previous one's wait episode (waits-events-cost-cli D1).
	if err = endEpisode(ctx, tx, t.ID); err != nil {
		return t, err
	}
	return t, addTrigger(ctx, tx, t, "assignment", true, actor, "", strings.TrimSpace("assigned to "+t.Assignee+" by "+actor+". "+reason))
}

// openChildren lists the task's open children.
func openChildren(ctx context.Context, tx *Tx, id string) ([]tasks.Task, error) {
	return listDocs[tasks.Task](ctx, tx, "SELECT body FROM tasks WHERE company_id=? AND json_extract(body,'$.parent_task_id')=? AND status NOT IN ('DONE','CANCELLED') ORDER BY created_at,id", tx.company, id)
}

// cancelTx cancels an open task: a QUEUED run is revoked, a claimed or running one asked to stop (the task is
// CANCELLED when it ends); with cascade its open children first, otherwise open children refuse it.
func cancelTx(ctx context.Context, tx *Tx, t tasks.Task, actor, reason string, cascade bool) (tasks.Task, error) {
	if t.Status == "DONE" || t.Status == "CANCELLED" {
		return t, ErrConflict
	}
	if t.Kind == tasks.KindConversation {
		return t, inputError("a conversation task stays open")
	}
	children, err := openChildren(ctx, tx, t.ID)
	if err != nil {
		return t, err
	}
	if len(children) > 0 && !cascade {
		return t, ErrNeedsCascade
	}
	for _, c := range children {
		if c.CancelRequested {
			continue // its run is stopping
		}
		if _, err = cancelTx(ctx, tx, c, actor, "parent "+t.ID+" cancelled", true); err != nil {
			return t, err
		}
	}
	if t.CurrentRunID != "" {
		var status string
		var unreleased bool
		if err = tx.QueryRowContext(ctx, "SELECT status,unreleased FROM runs WHERE id=?", t.CurrentRunID).Scan(&status, &unreleased); err != nil {
			return t, err
		}
		if unreleased {
			return t, ErrConflict
		}
		switch status {
		case "CLAIMED", "RUNNING", "CANCELLING":
			if _, err = tx.ExecContext(ctx, "UPDATE runs SET status='CANCELLING' WHERE id=?", t.CurrentRunID); err != nil {
				return t, err
			}
			t.CancelRequested = true
			if err = comment(ctx, tx, t.ID, actor, reason); err != nil {
				return t, err
			}
			return t, saveTask(ctx, tx, &t, "task.updated")
		case "QUEUED":
			if _, err = tx.ExecContext(ctx, "UPDATE runs SET status='ENDED',outcome='revoked',finished_at=? WHERE id=?", tx.now().Unix(), t.CurrentRunID); err != nil {
				return t, err
			}
			if err = autoRuntimeReport(ctx, tx, t.CurrentRunID); err != nil {
				return t, err
			}
		}
	}
	t.CancelRequested = true
	t.Status = "CANCELLED"
	if err = comment(ctx, tx, t.ID, actor, reason); err != nil {
		return t, err
	}
	if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
		return t, err
	}
	return t, closedTask(ctx, tx, t)
}

// TaskPause returns the authority pause of the task (for Core's resume check and reads).
func (s *Store) TaskPause(ctx context.Context, company, id string) (*tasks.TaskPause, error) {
	return taskPause(ctx, s.DB, company, id)
}

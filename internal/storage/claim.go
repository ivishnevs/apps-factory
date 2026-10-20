package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Claim re-check (trigger-log-scheduling D6): a queued task run is claimed only while its task still wants
// it. The task is evaluated again in the claim's transaction, with the run being claimed as its live run;
// a run the task no longer wants ends stale (no attempt, no backoff) and the worker drops its command.

// staleVerdict reports whether an Evaluate verdict means the task no longer wants a queued run. Pauses,
// capacity and node reasons keep the run QUEUED: the claim's own checks answer retry for them.
func staleVerdict(v tasks.Verdict) bool {
	switch v.Reason {
	case tasks.ReasonTerminal, tasks.ReasonBacklog, tasks.ReasonUnassigned, tasks.ReasonDependencies:
		return true
	}
	return strings.HasPrefix(v.Reason, tasks.ReasonParked)
}

// recheckTask says why the task of run id no longer wants it ("" when it does).
func recheckTask(ctx context.Context, tx *Tx, id string, spec protocol.RunSpec) (string, error) {
	if spec.TaskID == "" {
		return "", nil
	}
	t, err := taskTx(ctx, tx, spec.TaskID)
	if err != nil {
		return "", err
	}
	switch {
	case t.CurrentRunID != id:
		return "run " + id + " is not the task's current run", nil
	case t.Assignee != spec.Agent:
		return "task reassigned to " + t.Assignee, nil
	case t.CancelRequested:
		return "task cancel requested", nil
	}
	snap, err := snapshotTx(ctx, tx, t)
	if err != nil {
		return "", err
	}
	// The run being claimed is the task's live run; the assignee was a configured agent at dispatch.
	snap.CurrentRun, snap.AgentConfigured = nil, true
	if v := tasks.Evaluate(snap); staleVerdict(v) {
		return v.Reason + ": " + v.Detail, nil
	}
	return "", nil
}

// endQueued ends a QUEUED task run that never started with outcome stale or unclaimed (neither counts as an
// attempt nor applies backoff) and puts a task in progress on it back to READY, where Evaluate decides when
// it runs again. It reports false when the run was no longer QUEUED.
func endQueued(ctx context.Context, tx *Tx, id, outcome, why string) (bool, error) {
	now := tx.now()
	res, err := tx.ExecContext(ctx, "UPDATE runs SET status='CANCELLED',outcome=?,result=?,finished_at=? WHERE company_id=? AND id=? AND status='QUEUED'", outcome, why, now.Unix(), tx.company, id)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, nil
	}
	if err = autoRuntimeReport(ctx, tx, id); err != nil {
		return false, err
	}
	spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE id=?", id)
	if err != nil {
		return false, err
	}
	item := FeedItem{EventID: protocol.ID(), Type: "run." + outcome, EntityID: id, ProjectID: spec.Project, CreatedAt: now, TaskID: spec.TaskID, RunID: id, Agent: spec.Agent, Actor: "system", Status: "CANCELLED", Summary: "run " + id + " ended " + outcome + " before it started: " + why}
	if spec.TaskID != "" {
		t, err := taskTx(ctx, tx, spec.TaskID)
		if err != nil {
			return false, err
		}
		if t.CurrentRunID == id && t.Status == "IN_PROGRESS" {
			t.Status = "READY"
			if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
				return false, err
			}
		}
	}
	return true, feed(ctx, tx, item)
}

// claimStale ends run id stale for why, commits, and returns the drop refusal of a claim.
func claimStale(ctx context.Context, tx *Tx, id, why string) error {
	if _, err := endQueued(ctx, tx, id, "stale", why); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return drop("not_runnable")
}

// QueueTTL is how long a task run may stay QUEUED before the reconciler ends it unclaimed (D5); the task is
// dispatched again on the next tick, with its reason showing why nothing claims it.
var QueueTTL = 15 * time.Minute

// ExpireQueued ends task runs QUEUED for longer than QueueTTL with outcome unclaimed. Runs without a task
// (message deliveries, direct runs) keep their command redelivery only.
func (s *Store) ExpireQueued(ctx context.Context, company string) (int, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id FROM runs WHERE company_id=? AND status='QUEUED' AND task_id IS NOT NULL AND created_at<=?", company, tx.now().Add(-QueueTTL).Unix())
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	n := 0
	for _, id := range ids {
		ended, err := endQueued(ctx, tx, id, "unclaimed", fmt.Sprintf("unclaimed: no node claimed the run within %s", QueueTTL))
		if err != nil {
			return 0, err
		}
		if ended {
			n++
		}
	}
	return n, tx.Commit()
}

// ClaimRun is the v1 claim (no node identity): the same re-check as v2, the brief built in the claim's
// transaction, and the answer carrying the RunSpec, so the worker does not read the run separately. A refusal
// is returned as *Refusal.
func (s *Store) ClaimRun(ctx context.Context, company, id string) (Claimed, error) {
	var out Claimed
	tx, err := s.begin(ctx, company)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var status string
	var revoked bool
	if err = tx.QueryRowContext(ctx, "SELECT status,revoked FROM runs WHERE company_id=? AND id=?", company, id).Scan(&status, &revoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrNotFound
		}
		return out, err
	}
	switch {
	case revoked:
		return out, drop("revoked")
	case status == "RUNNING" || status == "CANCELLING":
		return out, drop("duplicate")
	case status != "QUEUED":
		return out, drop("stale")
	}
	spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE company_id=? AND id=?", company, id)
	if err != nil {
		return out, err
	}
	if why, err := recheckTask(ctx, tx, id, spec); err != nil {
		return out, err
	} else if why != "" {
		return out, claimStale(ctx, tx, id, why)
	}
	if spec.OpenSpecChange != "" {
		if _, e := s.OpenSpec.CheckChange(spec.Workspace, spec.OpenSpecChange); e != nil {
			if err = blockOpenSpecRun(ctx, tx, id, spec.TaskID, e.Error()); err != nil {
				return out, err
			}
			if err = tx.Commit(); err != nil {
				return out, err
			}
			return out, drop("not_runnable")
		}
	}
	valid, err := approvedRunArtifacts(ctx, tx, spec)
	if err != nil {
		return out, err
	}
	if !valid {
		if err = reconcileArtifactTasks(ctx, tx); err != nil {
			return out, err
		}
		if err = tx.Commit(); err != nil {
			return out, err
		}
		return out, drop("not_runnable")
	}
	// A v1 worker does not declare worktrees: the run holds the project workspace (one at a time, as before).
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET workspace=? WHERE id=? AND status='QUEUED'", writerSlot(spec, false), id); err != nil {
		if errors.Is(constraint(err), ErrConflict) {
			return out, retry("project_busy")
		}
		return out, err
	}
	if err = briefClaim(ctx, tx, id, &spec); err != nil {
		return out, err
	}
	res, err := tx.ExecContext(ctx, "UPDATE runs SET status='RUNNING',lease=?,lease_until=?,started_at=?,last_activity_at=started_at,brief_from=coalesce((SELECT handled_seq FROM tasks WHERE id=runs.task_id),0),brief_to=coalesce((SELECT wake_seq FROM tasks WHERE id=runs.task_id),0) WHERE id=? AND status='QUEUED'", protocol.ID(), s.Now().Add(LeaseTTL).Unix(), s.Now().Unix(), id)
	if err != nil {
		return out, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return out, drop("stale")
	}
	if out, err = claimedAnswer(ctx, tx, company, id); err != nil {
		return out, err
	}
	out.StartBy = time.Time{}
	return out, tx.Commit()
}

// Claim is ClaimRun for callers that need only the lease; a refusal is ErrConflict.
func (s *Store) Claim(ctx context.Context, company, id string) (string, error) {
	c, err := s.ClaimRun(ctx, company, id)
	var r *Refusal
	if errors.As(err, &r) {
		return "", fmt.Errorf("%w: %s", ErrConflict, r.Error())
	}
	return c.Lease, err
}

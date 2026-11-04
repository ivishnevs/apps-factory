package storage

import (
	"context"
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
	// The end is applied here (no attempt, backoff or budget), so the run counts as recovered.
	res, err := tx.ExecContext(ctx, "UPDATE runs SET status='ENDED',outcome=?,result=?,finished_at=?,recovered=1 WHERE company_id=? AND id=? AND status='QUEUED'", outcome, why, now.Unix(), tx.company, id)
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
	item := FeedItem{EventID: protocol.ID(), Type: "run." + outcome, EntityID: id, ProjectID: spec.Project, CreatedAt: now, TaskID: spec.TaskID, RunID: id, Agent: spec.Agent, Actor: "system", Status: "ENDED", Summary: "run " + id + " ended " + outcome + " before it started: " + why}
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
	rows, err := tx.QueryContext(ctx, "SELECT id FROM runs WHERE company_id=? AND status='QUEUED' AND task_id IS NOT NULL AND created_at<=? ORDER BY seq", company, tx.now().Add(-QueueTTL).Unix())
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

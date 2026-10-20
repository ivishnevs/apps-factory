package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"appfactory/internal/approvals"
	"appfactory/internal/tasks"
)

// Waits are parks and derived reasons, not stored state (trigger-log-scheduling D2): a task waiting for the
// answer to its blocking question has an active ask park, one waiting for artifact approvals has unapproved
// required artifacts, and its stored status stays READY. Reads present the compatibility view: status
// BLOCKED with block_reason, and wait_message_id while the question is open.

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// askPark is the question an open task waits for (the ref of its active ask park), "" when none.
func askPark(ctx context.Context, q querier, company, task string) (string, error) {
	var ref string
	err := q.QueryRowContext(ctx, "SELECT ref_id FROM task_parks WHERE company_id=? AND task_id=? AND kind='ask' AND status='active' ORDER BY created_at,id LIMIT 1", company, task).Scan(&ref)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return ref, err
}

// runAsked reports whether run asked a blocking question (an ask park it created, answered or not).
func runAsked(ctx context.Context, tx *Tx, run string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, "SELECT count(*) FROM task_parks WHERE company_id=? AND kind='ask' AND run_id=?", tx.company, run).Scan(&n)
	return n > 0, err
}

// presentTask derives the compatibility view of a stored task in place.
func presentTask(ctx context.Context, q querier, company string, t *tasks.Task) error {
	if t.Status == "DONE" || t.Status == "CANCELLED" {
		return nil
	}
	ref, err := askPark(ctx, q, company, t.ID)
	if err != nil {
		return err
	}
	t.WaitMessageID = ref
	if t.Status != "READY" {
		return nil
	}
	if ref != "" {
		t.Status, t.BlockReason = "BLOCKED", "message: waiting for answer"
		return nil
	}
	for _, a := range t.RequiredArtifacts {
		ap, err := getDoc[approvals.Approval](ctx, q, artifactApprovalQuery, company, t.ProjectID, a.ID, a.Version)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err != nil || ap.Status != "APPROVED" || ap.Artifact == nil || *ap.Artifact != a {
			t.Status, t.BlockReason = "BLOCKED", fmt.Sprintf("artifact: waiting for owner approval of %s@%s", a.ID, a.Version)
			return nil
		}
	}
	return nil
}

func presentTasks(ctx context.Context, q querier, company string, ts []tasks.Task) error {
	for i := range ts {
		if err := presentTask(ctx, q, company, &ts[i]); err != nil {
			return err
		}
	}
	return nil
}

package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// node-ties: the one notion behind pins (work) and session affinity (session). The placement rules stay where
// they are (ClaimV2, Offers: a work tie outranks a session tie); these helpers only describe the tie.

// workTie is the task's work tie: its unsaved work is only on a node (workspaces.unpushed) — nil when none.
func workTie(ctx context.Context, q affinityQuerier, company, task string) (*tasks.Tie, error) {
	var t tasks.Tie
	err := q.QueryRowContext(ctx, "SELECT node_id,tied_since,wip_unsaved FROM workspaces WHERE company_id=? AND task_id=? AND unpushed=1 AND node_id!=''", company, task).Scan(&t.Node, &t.Since, &t.Unsaved)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.Reason, t.Release = tasks.TieWork, tasks.WorkRelease
	return &t, nil
}

// sessionTie describes Affinity a (the session lock that keeps a queued run on its holder) as a session tie.
func sessionTie(ctx context.Context, q affinityQuerier, company, run, session string, a *Affinity) (*tasks.Tie, error) {
	timeout, err := companyAffinity(ctx, q, company)
	if err != nil {
		return nil, err
	}
	t := &tasks.Tie{Node: a.Holder, Reason: tasks.TieSession, Release: tasks.SessionRelease(timeout), ReleaseAt: a.Release}
	if a.Release > 0 {
		t.Since = a.Release - int64(timeout/time.Second)
	} else if err = q.QueryRowContext(ctx, "SELECT coalesce(max(created_at),0) FROM runs WHERE company_id=? AND session_id=? AND node_id=? AND id!=?", company, session, a.Holder, run).Scan(&t.Since); err != nil {
		return nil, err
	}
	return t, nil
}

// readOnlyQuerier lets sessionAffinity judge a lock without keeping its inactivity timer (a task view must not
// start or clear it; the claim and the queued-run reason do).
type readOnlyQuerier struct{ affinityQuerier }

func (readOnlyQuerier) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, nil
}

// taskTie is a task's tie for its view: the work tie, else the session tie of its queued run.
func taskTie(ctx context.Context, tx *Tx, t tasks.Task, current *tasks.Run) (*tasks.Tie, error) {
	if w, err := workTie(ctx, tx, tx.company, t.ID); err != nil || w != nil {
		return w, err
	}
	if current == nil || current.Status != "QUEUED" {
		return nil, nil
	}
	spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE company_id=? AND id=?", tx.company, current.ID)
	if err != nil || spec.SessionID == "" {
		return nil, err
	}
	a, err := sessionAffinity(ctx, readOnlyQuerier{tx}, tx.company, current.ID, spec, "", tx.now())
	if err != nil || a == nil {
		return nil, err
	}
	return sessionTie(ctx, tx, tx.company, current.ID, spec.SessionID, a)
}

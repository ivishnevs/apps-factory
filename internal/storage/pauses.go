package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"appfactory/internal/protocol"
)

// Pause is an owner hold on new dispatch for the whole company, a project or an agent.
type Pause struct {
	Scope     string `json:"scope"`
	Target    string `json:"target,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Actor     string `json:"actor"`
	CreatedAt int64  `json:"created_at"`
}

func validPause(scope, target string) error {
	switch {
	case scope == "company" && target == "":
	case (scope == "project" || scope == "agent") && entityName.MatchString(target):
	default:
		return inputError("pause scope must be company, project ID or agent name")
	}
	return nil
}

func pauseFeed(ctx context.Context, tx *Tx, kind string, p Pause) error {
	project := ""
	if p.Scope == "project" {
		project = p.Target
	}
	summary := strings.TrimSpace(p.Scope + " " + p.Target)
	if p.Reason != "" {
		summary += ": " + p.Reason
	}
	return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: kind, EntityID: p.Scope + ":" + p.Target, ProjectID: project, CreatedAt: tx.now(), Actor: p.Actor, Summary: summary, Attention: kind == "owner.paused"})
}

// SetPause pauses a scope (idempotent); new dispatch there stops, live runs continue.
func (s *Store) SetPause(ctx context.Context, company string, p Pause) error {
	if err := validPause(p.Scope, p.Target); err != nil {
		return err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if p.Actor == "" {
		p.Actor = "owner"
	}
	res, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO pauses VALUES(?,?,?,?,?,?)", company, p.Scope, p.Target, p.Reason, p.Actor, tx.now().Unix())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		if err = pauseFeed(ctx, tx, "owner.paused", p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearPause resumes a scope; resuming a scope that is not paused is a no-op.
func (s *Store) ClearPause(ctx context.Context, company, scope, target, actor string) error {
	if err := validPause(scope, target); err != nil {
		return err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "DELETE FROM pauses WHERE company_id=? AND scope=? AND target=?", company, scope, target)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		if actor == "" {
			actor = "owner"
		}
		if err = pauseFeed(ctx, tx, "owner.resumed", Pause{Scope: scope, Target: target, Actor: actor}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Pauses(ctx context.Context, company string) ([]Pause, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT scope,target,reason,actor,created_at FROM pauses WHERE company_id=? ORDER BY scope,target", company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pause{}
	for rows.Next() {
		var p Pause
		if err = rows.Scan(&p.Scope, &p.Target, &p.Reason, &p.Actor, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// pausedFor returns the paused scopes covering a project and agent, most general first:
// "company", "project:<id>", "agent:<name>".
func pausedFor(ctx context.Context, tx *Tx, project, agent string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT scope,target FROM pauses WHERE company_id=? AND (scope='company' OR scope='project' AND target=? OR scope='agent' AND target=?) ORDER BY CASE scope WHEN 'company' THEN 0 WHEN 'project' THEN 1 ELSE 2 END", tx.company, project, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var scope, target string
		if err = rows.Scan(&scope, &target); err != nil {
			return nil, err
		}
		if target != "" {
			scope += ":" + target
		}
		out = append(out, scope)
	}
	return out, rows.Err()
}

// RevokeLive is the kill switch's second half: every QUEUED run is revoked at once and every
// RUNNING run is asked to stop; their tasks return to READY instead of being cancelled.
func (s *Store) RevokeLive(ctx context.Context, company, actor string) (int, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id,status FROM runs WHERE company_id=? AND status IN ('QUEUED','RUNNING','CANCELLING') AND revoked=0", company)
	if err != nil {
		return 0, err
	}
	type live struct{ id, status string }
	var runs []live
	for rows.Next() {
		var r live
		if err = rows.Scan(&r.id, &r.status); err != nil {
			rows.Close()
			return 0, err
		}
		runs = append(runs, r)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, r := range runs {
		next := "CANCELLING"
		if r.status == "QUEUED" {
			next = "CANCELLED"
		}
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET status=?,revoked=1,result=CASE WHEN ?='CANCELLED' THEN 'revoked by '||? ELSE result END,finished_at=CASE WHEN ?='CANCELLED' THEN ? ELSE finished_at END WHERE id=?", next, next, actor, next, tx.now().Unix(), r.id); err != nil {
			return 0, err
		}
		if next == "CANCELLED" {
			if err = syncTaskRun(ctx, tx, r.id, false); err != nil {
				return 0, err
			}
		}
	}
	return len(runs), tx.Commit()
}

func runRevoked(ctx context.Context, tx *Tx, id string) (bool, error) {
	var revoked bool
	err := tx.QueryRowContext(ctx, "SELECT revoked FROM runs WHERE id=?", id).Scan(&revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return revoked, err
}

// PendingCounts is what waits on the owner.
type PendingCounts struct {
	Inbox     int `json:"inbox"`
	Approvals int `json:"approvals"`
	Alerts    int `json:"alerts"`
	Inspect   int `json:"inspect"`
	Reviews   int `json:"in_review"`
	// Total counts inbox items, active alerts and runs to inspect; approvals are already in the inbox.
	Total int `json:"total"`
}

func (s *Store) PendingCounts(ctx context.Context, company string) (PendingCounts, error) {
	var p PendingCounts
	inbox, err := s.Inbox(ctx, company, "")
	if err != nil {
		return p, err
	}
	p.Inbox = len(inbox)
	for q, dst := range map[string]*int{
		"SELECT count(*) FROM approvals WHERE company_id=? AND status='PENDING'":                    &p.Approvals,
		"SELECT count(*) FROM task_alerts WHERE company_id=? AND status='active'":                   &p.Alerts,
		"SELECT count(*) FROM task_parks WHERE company_id=? AND status='active' AND kind='inspect'": &p.Inspect,
		"SELECT count(*) FROM tasks WHERE company_id=? AND status='IN_REVIEW'":                      &p.Reviews,
	} {
		if err = s.DB.QueryRowContext(ctx, q, company).Scan(dst); err != nil {
			return p, err
		}
	}
	p.Total = p.Inbox + p.Alerts + p.Inspect
	return p, nil
}

package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// DefaultStallBacklogAfter is how long a planned BACKLOG dependency may stay unchanged before the task waiting on
// it escalates as stalled (dependency-wait-escalations 1.1).
const DefaultStallBacklogAfter = 24 * time.Hour

// StallBacklogAfter is the company's threshold for BACKLOG dependencies.
func (s *Store) StallBacklogAfter(ctx context.Context, company string) (time.Duration, error) {
	var minutes int64
	err := s.DB.QueryRowContext(ctx, "SELECT stall_backlog_after_minutes FROM companies WHERE id=?", company).Scan(&minutes)
	if errors.Is(err, sql.ErrNoRows) || err == nil && minutes <= 0 {
		return DefaultStallBacklogAfter, nil
	}
	return time.Duration(minutes) * time.Minute, err
}

// SetStallBacklogAfter sets the threshold (1 hour to 30 days).
func (s *Store) SetStallBacklogAfter(ctx context.Context, company string, d time.Duration) error {
	if d < time.Hour || d > 30*24*time.Hour {
		return inputError("backlog_after must be between 1h and 720h")
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE companies SET stall_backlog_after_minutes=? WHERE id=?", int64(d/time.Minute), company)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// KeptWaiting is when each (task, dependency) last got the stalled answer keep_waiting, keyed "task\x00ref": the
// sweep opens no new stalled escalation for it until the dependency changes or the threshold passes again.
func (s *Store) KeptWaiting(ctx context.Context, company string) (map[string]time.Time, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT coalesce(task_id,''),ref_id,max(closed_at) FROM escalations WHERE company_id=? AND reason='stalled' AND status='answered' AND answer='keep_waiting' GROUP BY task_id,ref_id", company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var task, ref string
		var at int64
		if err = rows.Scan(&task, &ref, &at); err != nil {
			return nil, err
		}
		out[task+"\x00"+ref] = time.Unix(at, 0)
	}
	return out, rows.Err()
}

// SweepFailure is one task a background sweep could not handle; the sweep went on with the others.
type SweepFailure struct {
	TaskID string
	Err    error
}

// SweepErrors are the per-task failures of one sweep step (dependency-wait-escalations 0.2).
type SweepErrors []SweepFailure

func (e SweepErrors) Error() string {
	parts := make([]string, len(e))
	for i, f := range e {
		parts[i] = f.TaskID + ": " + f.Err.Error()
	}
	return "sweep failed for " + strings.Join(parts, "; ")
}

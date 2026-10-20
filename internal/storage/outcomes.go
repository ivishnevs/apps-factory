package storage

import (
	"context"
	"log/slog"
	"time"
)

// OutcomeRule is one row of the run outcome table (reliable-recovery D1): whether the outcome counts as
// an attempt, applies backoff and counts against the daily budget. Every ENDED run has its outcome
// (runs.outcome, set by the code that ends it; the run_phase trigger refuses an ENDED run without one).
type OutcomeRule struct {
	Attempt, Backoff, Budget bool
}

// Outcomes is the outcome table. `lost` is budget-counted except for new-epoch losses (decided when the
// budget exists).
var Outcomes = map[string]OutcomeRule{
	"succeeded":    {Budget: true},
	"no_exit":      {Attempt: true, Budget: true},
	"failed":       {Attempt: true, Backoff: true, Budget: true},
	"timed_out":    {Attempt: true, Backoff: true, Budget: true},
	"idle_timeout": {Attempt: true, Backoff: true, Budget: true},
	"max_turns":    {Attempt: true, Backoff: true, Budget: true},
	"setup_failed": {Attempt: true, Backoff: true, Budget: true},
	"lost":         {Backoff: true, Budget: true},
	"revoked":      {},
	"paused_out":   {},
	"stale":        {},
	"unclaimed":    {},
}

// IdleTimeout is how long a started run may go without any applied event before Core stops it as
// idle_timeout (reliable-recovery D1, the hang detector); a variable for tests.
var IdleTimeout = 30 * time.Minute

// StopIdle asks the worker to stop every started run without an applied event for IdleTimeout. The
// quiet time counts from the later of the last event and the start of Core's current continuity, so a
// host sleep never makes runs idle. The stop arrives as a heartbeat answer (v1 cancel, v2 stop
// idle_timeout); the run then ends with outcome idle_timeout, and its task is not cancelled.
func (s *Store) StopIdle(ctx context.Context, company string) (int, error) {
	since := s.Now().Add(-IdleTimeout)
	if s.Continuity != nil {
		start := s.Continuity.Start()
		if start.IsZero() || start.After(since) {
			return 0, nil
		}
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id FROM runs WHERE company_id=? AND status='RUNNING' AND stop_reason='' AND last_activity_at>0 AND last_activity_at<?", company, since.Unix())
	if err != nil {
		return 0, err
	}
	var idle []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		idle = append(idle, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, id := range idle {
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET stop_reason='idle_timeout' WHERE id=?", id); err != nil {
			return 0, err
		}
		slog.Warn("run idle: asking the worker to stop it", "run", id, "idle_timeout", IdleTimeout.String())
	}
	return len(idle), tx.Commit()
}

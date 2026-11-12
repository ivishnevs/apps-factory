package storage

import (
	"context"
	"database/sql"
)

// RunSummary is a run as the owner's support tools show it (setup-assistant 1.1, 3.1): no spec, no instructions.
type RunSummary struct {
	ID         string    `json:"id"`
	TaskID     string    `json:"task_id,omitempty"`
	Project    string    `json:"project"`
	Agent      string    `json:"agent"`
	NodeID     string    `json:"node_id,omitempty"`
	Status     string    `json:"status"`
	Outcome    string    `json:"outcome,omitempty"`
	NodeFault  string    `json:"node_fault,omitempty"`
	Result     string    `json:"result,omitempty"`
	Usage      UsageList `json:"usage,omitempty"`
	CreatedAt  int64     `json:"created_at"`
	FinishedAt int64     `json:"finished_at,omitempty"`
}

// RecentRuns is the company's runs created since since (Unix seconds), newest first, at most limit; failed keeps only
// ended runs that did not succeed (and were not stopped by the owner or Core: revoked, stale, unclaimed).
func (s *Store) RecentRuns(ctx context.Context, company string, since int64, limit int, failed bool) ([]RunSummary, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `SELECT id,coalesce(task_id,''),project,agent,node_id,status,outcome,node_fault,result,usage,created_at,finished_at FROM runs
 WHERE company_id=? AND created_at>=?`
	if failed {
		q += ` AND status='ENDED' AND outcome NOT IN ('succeeded','revoked','stale','unclaimed')`
	}
	q += ` ORDER BY created_at DESC, seq DESC LIMIT ?`
	rows, err := s.DB.QueryContext(ctx, q, company, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunSummary{}
	for rows.Next() {
		var r RunSummary
		var finished sql.NullInt64
		if err = rows.Scan(&r.ID, &r.TaskID, &r.Project, &r.Agent, &r.NodeID, &r.Status, &r.Outcome, &r.NodeFault, &r.Result, &r.Usage, &r.CreatedAt, &finished); err != nil {
			return nil, err
		}
		r.FinishedAt = finished.Int64
		out = append(out, r)
	}
	return out, rows.Err()
}

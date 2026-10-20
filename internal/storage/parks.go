package storage

import "context"

// TaskPark is one park of a task as GET /tasks/{id}/parks lists it (owner-ui 6.5): its kind, what it waits
// on (ref_id: a message, a run, a child task), why, who clears it, and when it was set and cleared.
type TaskPark struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	RefID  string `json:"ref_id,omitempty"`
	Reason string `json:"reason"`
	// Who clears it: owner, or agent:<name> for a question to an agent.
	Who       string `json:"who"`
	Status    string `json:"status"` // active, cleared or overridden
	RunID     string `json:"run_id,omitempty"`
	CreatedAt int64  `json:"created_at"`
	ClearedAt int64  `json:"cleared_at,omitempty"`
}

// TaskParks lists a task's parks, oldest first: the active ones, or all of them with all.
func (s *Store) TaskParks(ctx context.Context, company, task string, all bool) ([]TaskPark, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = taskTx(ctx, tx, task); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.id,p.kind,p.ref_id,p.reason,p.status,p.run_id,p.created_at,p.cleared_at,coalesce(m.recipient,'')
 FROM task_parks p LEFT JOIN messages m ON p.kind IN ('ask','escalation') AND m.id=p.ref_id
 WHERE p.company_id=? AND p.task_id=? AND (?=1 OR p.status='active') ORDER BY p.created_at,p.seq`, tx.company, task, map[bool]int{true: 1}[all])
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskPark{}
	for rows.Next() {
		var p TaskPark
		var recipient string
		if err = rows.Scan(&p.ID, &p.Kind, &p.RefID, &p.Reason, &p.Status, &p.RunID, &p.CreatedAt, &p.ClearedAt, &recipient); err != nil {
			return nil, err
		}
		p.Who = "owner"
		if recipient != "" && recipient != "owner" {
			p.Who = "agent:" + recipient
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

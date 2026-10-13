package storage

import "context"

type ToolCall struct {
	ApprovalID string `json:"approval_id,omitempty"`
	ID         string `json:"id"`
	RunID      string `json:"run_id"`
	Agent      string `json:"agent"`
	ProjectID  string `json:"project_id"`
	Tool       string `json:"tool"`
	RequestID  string `json:"request_id"`
	Status     int    `json:"status"`
	CreatedAt  int64  `json:"created_at"`
}

func (s *Store) ToolCalls(ctx context.Context, run string) ([]ToolCall, error) {
	if _, err := s.Get(ctx, run); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT id,run_id,agent,project_id,tool,request_id,status,created_at,COALESCE(approval_id,'') FROM tool_calls WHERE run_id=? ORDER BY created_at,rowid", run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ToolCall{}
	for rows.Next() {
		var v ToolCall
		if err = rows.Scan(&v.ID, &v.RunID, &v.Agent, &v.ProjectID, &v.Tool, &v.RequestID, &v.Status, &v.CreatedAt, &v.ApprovalID); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

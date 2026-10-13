// Package approvals defines durable owner decisions for exact Factory tool calls.
package approvals

import "appfactory/internal/factorytools"
import "appfactory/internal/tasks"

type Approval struct {
	Kind           string            `json:"kind,omitempty"`
	Artifact       *tasks.Artifact   `json:"artifact,omitempty"`
	ID             string            `json:"id"`
	ProjectID      string            `json:"project_id"`
	Agent          string            `json:"agent"`
	TaskID         string            `json:"task_id,omitempty"`
	RunID          string            `json:"run_id"`
	MessageID      string            `json:"message_id"`
	Call           factorytools.Call `json:"call"`
	Reason         string            `json:"reason"`
	Status         string            `json:"status"`
	Version        int               `json:"version"`
	CreatedAt      int64             `json:"created_at"`
	ExpiresAt      int64             `json:"expires_at"`
	DecidedAt      int64             `json:"decided_at,omitempty"`
	DecisionReason string            `json:"decision_reason,omitempty"`
}
type Request struct {
	ProjectID, Agent, TaskID, RunID string
	Call                            factorytools.Call `json:"call"`
	Reason                          string            `json:"reason"`
	Blocking                        bool              `json:"blocking"`
	TimeoutSeconds                  int               `json:"timeout_seconds"`
	RequestID                       string            `json:"request_id"`
}
type Decision struct {
	Version   int    `json:"version"`
	Action    string `json:"action"`
	Reason    string `json:"reason"`
	RequestID string `json:"request_id"`
}

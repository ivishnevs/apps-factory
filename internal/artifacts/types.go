// Package artifacts defines immutable version references and owner approval requests.
package artifacts

import "appfactory/internal/tasks"

type Record struct {
	tasks.Artifact
	ProjectID string `json:"project_id"`
	TaskID    string `json:"task_id"`
	RunID     string `json:"run_id,omitempty"`
	CreatedBy string `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
}
type Register struct {
	Artifact  tasks.Artifact `json:"artifact"`
	TaskID    string         `json:"task_id"`
	RunID     string         `json:"run_id,omitempty"`
	Actor     string         `json:"-"`
	RequestID string         `json:"request_id"`
}
type ApprovalRequest struct {
	ArtifactID      string `json:"artifact_id"`
	ArtifactVersion string `json:"artifact_version"`
	Agent           string `json:"agent,omitempty"`
	TaskID          string `json:"-"`
	RunID           string `json:"-"`
	Reason          string `json:"reason"`
	Blocking        bool   `json:"blocking,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds,omitempty"`
	RequestID       string `json:"request_id"`
}

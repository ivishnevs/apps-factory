// Package reports defines durable significant-action reports and observed runtime outcomes.
package reports

import "appfactory/internal/tasks"

type Report struct {
	ID               string           `json:"id"`
	ProjectID        string           `json:"project_id"`
	TaskID           string           `json:"task_id,omitempty"`
	RunID            string           `json:"run_id,omitempty"`
	CreatedBy        string           `json:"created_by"`
	Source           string           `json:"source"`
	Kind             string           `json:"kind"`
	Summary          string           `json:"summary"`
	Purpose          string           `json:"purpose"`
	Changes          []string         `json:"changes"`
	Result           string           `json:"result"`
	Artifacts        []tasks.Artifact `json:"artifacts,omitempty"`
	NeedsOwnerAction bool             `json:"needs_owner_action"`
	OwnerAction      string           `json:"owner_action,omitempty"`
	MessageID        string           `json:"message_id,omitempty"`
	CreatedAt        int64            `json:"created_at"`
}
type Create struct {
	TaskID           string           `json:"task_id,omitempty"`
	RunID            string           `json:"run_id,omitempty"`
	Kind             string           `json:"kind"`
	Summary          string           `json:"summary"`
	Purpose          string           `json:"purpose"`
	Changes          []string         `json:"changes"`
	Result           string           `json:"result"`
	Artifacts        []tasks.Artifact `json:"artifacts,omitempty"`
	NeedsOwnerAction bool             `json:"needs_owner_action"`
	OwnerAction      string           `json:"owner_action,omitempty"`
	RequestID        string           `json:"request_id"`
	Actor            string           `json:"-"`
}

// Package decisions defines versioned project proposals and explicit owner decisions.
package decisions

import (
	"appfactory/internal/factorytools"
	"appfactory/internal/tasks"
)

type Decision struct {
	CurrentAuthorityRules []string         `json:"current_authority_rules,omitempty"`
	ID                    string           `json:"id"`
	ProjectID             string           `json:"project_id"`
	Category              string           `json:"category,omitempty"`
	Authority             string           `json:"authority,omitempty"`
	AuthorityRules        []string         `json:"authority_rules,omitempty"`
	ConfigHash            string           `json:"config_hash,omitempty"`
	CurrentConfigHash     string           `json:"current_config_hash,omitempty"`
	ReactionMessageID     string           `json:"reaction_message_id,omitempty"`
	Topic                 string           `json:"topic"`
	Status                string           `json:"status"`
	Value                 string           `json:"value"`
	Rationale             string           `json:"rationale"`
	CreatedBy             string           `json:"created_by"`
	ApprovedBy            string           `json:"approved_by,omitempty"`
	RecordedBy            string           `json:"recorded_by,omitempty"`
	ReviewRationale       string           `json:"review_rationale,omitempty"`
	Version               int              `json:"version"`
	TaskID                string           `json:"task_id,omitempty"`
	RunID                 string           `json:"run_id,omitempty"`
	Artifacts             []tasks.Artifact `json:"artifacts,omitempty"`
	MessageID             string           `json:"message_id,omitempty"`
	ApprovalID            string           `json:"approval_id,omitempty"`
	SupersedesID          string           `json:"supersedes_id,omitempty"`
	SupersededBy          string           `json:"superseded_by,omitempty"`
	CreatedAt             int64            `json:"created_at"`
	UpdatedAt             int64            `json:"updated_at"`
}
type Propose struct {
	Category  string           `json:"category,omitempty"`
	Topic     string           `json:"topic"`
	Value     string           `json:"value"`
	Rationale string           `json:"rationale"`
	TaskID    string           `json:"task_id,omitempty"`
	RunID     string           `json:"run_id,omitempty"`
	Artifacts []tasks.Artifact `json:"artifacts,omitempty"`
	RequestID string           `json:"request_id"`
	Actor     string           `json:"-"`
}
type Record struct {
	Version           int                `json:"version"`
	Status            string             `json:"status"`
	Rationale         string             `json:"rationale"`
	SupersedesID      string             `json:"supersedes_id,omitempty"`
	SupersedesVersion int                `json:"supersedes_version,omitempty"`
	RequestID         string             `json:"request_id"`
	Actor             string             `json:"-"`
	ApprovalID        string             `json:"-"`
	ScopeTaskID       string             `json:"-"`
	GrantedCall       *factorytools.Call `json:"-"`
}
type ContextEntry struct {
	Category   string `json:"category,omitempty"`
	Authority  string `json:"authority,omitempty"`
	ID         string `json:"id"`
	Topic      string `json:"topic"`
	Version    int    `json:"version"`
	Value      string `json:"value"`
	Rationale  string `json:"rationale"`
	ApprovedBy string `json:"approved_by"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// Decide is an atomic, policy-delegated decision, distinct from owner recording.
type Decide struct {
	Propose
	SupersedesID      string `json:"supersedes_id,omitempty"`
	SupersedesVersion int    `json:"supersedes_version,omitempty"`
}

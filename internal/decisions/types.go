// Package decisions defines versioned project proposals and explicit owner decisions.
package decisions

import (
	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
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
	Alternatives          []string         `json:"alternatives,omitempty"`
	Cites                 []string         `json:"cites,omitempty"`
	Rationale             string           `json:"rationale"`
	CreatedBy             string           `json:"created_by"`
	ApprovedBy            string           `json:"approved_by,omitempty"`
	RecordedBy            string           `json:"recorded_by,omitempty"`
	RecordedByPerson      string           `json:"recorded_by_person,omitempty"`
	ReviewRationale       string           `json:"review_rationale,omitempty"`
	Version               int              `json:"version"`
	TaskID                string           `json:"task_id,omitempty"`
	RunID                 string           `json:"run_id,omitempty"`
	Artifacts             []tasks.Artifact `json:"artifacts,omitempty"`
	// Attachments and Materials are what the proposal shows (inbox-visual-previews); its message carries them too.
	Attachments  []messaging.Attachment  `json:"attachments,omitempty"`
	Materials    []messaging.MaterialRef `json:"materials,omitempty"`
	MessageID    string                  `json:"message_id,omitempty"`
	ApprovalID   string                  `json:"approval_id,omitempty"`
	SupersedesID string                  `json:"supersedes_id,omitempty"`
	SupersededBy string                  `json:"superseded_by,omitempty"`
	CreatedAt    int64                   `json:"created_at"`
	UpdatedAt    int64                   `json:"updated_at"`
	// Waiting are the open tasks parked on this decision (decision-waits), filled on read; never stored.
	Waiting []Waiter `json:"waiting,omitempty"`
}

// Waiter is a task parked on a decision.
type Waiter struct {
	TaskID string `json:"task_id"`
	Title  string `json:"title"`
}
type Propose struct {
	Category string `json:"category,omitempty"`
	Topic    string `json:"topic"`
	Value    string `json:"value"`
	// Alternatives are the other options the person may choose instead of Value (decision-approval-ux).
	Alternatives []string         `json:"alternatives,omitempty"`
	Rationale    string           `json:"rationale"`
	TaskID       string           `json:"task_id,omitempty"`
	RunID        string           `json:"run_id,omitempty"`
	Artifacts    []tasks.Artifact `json:"artifacts,omitempty"`
	// Attachments (files of the run's worktree, uploaded by the node) and Materials (references) for the owner to
	// see with the proposal (inbox-visual-previews).
	Attachments []messaging.Attachment  `json:"attachments,omitempty"`
	Materials   []messaging.MaterialRef `json:"materials,omitempty"`
	RequestID   string                  `json:"request_id"`
	Actor       string                  `json:"-"`
	// What people read with it (people-topics D3): brief.after is required; topic, value and rationale give the rest.
	Brief       *messaging.Brief `json:"brief,omitempty"`
	PeopleTopic string           `json:"people_topic,omitempty"`
	Strict      bool             `json:"-"`
}
type Record struct {
	// Value is the person's choice when approving (an alternative or their own words); empty keeps the proposal's.
	Value             string             `json:"value,omitempty"`
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
	// Cites are the owner's earlier answers or recorded decisions a delegated decision relies on (approval-delegation).
	Cites []string `json:"cites,omitempty"`
	// Delegation is the approval setting Core allowed the decision under ("" for a policy-delegated category).
	Delegation string `json:"-"`
}

package protocol

import (
	"appfactory/internal/decisions"
	"appfactory/internal/tasks"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

type RuntimeProvider string

const RuntimeCodex RuntimeProvider = "codex"

type RunSpec struct {
	OpenSpecChange    string                   `json:"openspec_change,omitempty"`
	DecisionContext   []decisions.ContextEntry `json:"decision_context,omitempty"`
	MoreDecisions     bool                     `json:"more_decisions,omitempty"`
	ApprovedArtifacts []tasks.ApprovedArtifact `json:"approved_artifacts,omitempty"`
	MessageID         string                   `json:"message_id,omitempty"`
	TaskID            string                   `json:"task_id,omitempty"`
	Attempt           int                      `json:"attempt,omitempty"`
	ConfigHash        string                   `json:"config_hash,omitempty"`
	ConfigSnapshot    json.RawMessage          `json:"config_snapshot,omitempty"`
	Skills            []string                 `json:"skills,omitempty"`

	ID             string          `json:"id"`
	Project        string          `json:"project"`
	Agent          string          `json:"agent"`
	Provider       RuntimeProvider `json:"provider"`
	Workspace      string          `json:"workspace"`
	Prompt         string          `json:"prompt"`
	Instructions   string          `json:"instructions"`
	TimeoutSeconds int             `json:"timeout_seconds"`
	SessionID      string          `json:"session_id,omitempty"`
	RuntimeOptions map[string]any  `json:"runtime_options,omitempty"`
}

type Envelope struct {
	ID            string          `json:"event_id"`
	Version       int             `json:"schema_version"`
	Type          string          `json:"type"`
	CorrelationID string          `json:"correlation_id"`
	CausationID   string          `json:"causation_id,omitempty"`
	At            time.Time       `json:"occurred_at"`
	Payload       json.RawMessage `json:"payload"`
}
type Event struct {
	RunID     string          `json:"run_id"`
	Lease     string          `json:"lease"`
	SessionID string          `json:"session_id,omitempty"`
	Status    string          `json:"status,omitempty"`
	Result    string          `json:"result,omitempty"`
	Method    string          `json:"method,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func Wrap(kind, correlation, cause string, payload any) Envelope {
	b, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return Envelope{ID: ID(), Version: 1, Type: kind, CorrelationID: correlation, CausationID: cause, At: time.Now().UTC(), Payload: b}
}

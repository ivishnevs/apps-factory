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

const (
	RuntimeCodex  RuntimeProvider = "codex"
	RuntimeClaude RuntimeProvider = "claude"
)

// Valid reports whether a native Worker exists for the provider's command subject.
func (p RuntimeProvider) Valid() bool { return p == RuntimeCodex || p == RuntimeClaude }

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

	// Task worktrees (task-worktrees): set by Core from the project and the agent's
	// spec.workspace.mode. Empty WorkspaceMode or "shared" keeps today's project workspace;
	// "worktree" asks the worker for a worktree of RepoURL per task.
	RepoURL       string `json:"repo_url,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	WorkspaceMode string `json:"workspace_mode,omitempty"`
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
	// Usage is the run's cumulative token use so far, one entry per model; Core keeps the last
	// usage it receives, so the terminal event of a stopped or timed-out run should carry it.
	Usage []Usage `json:"usage,omitempty"`
	// Quota reports that the runtime hit its subscription limit; Core pauses the agent's quota key
	// until ResetsAt.
	Quota *Quota `json:"quota,omitempty"`
}

// Usage is cumulative token use of one model in a run (runtime-drivers 2.5).
type Usage struct {
	Model      string `json:"model"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheRead  int64  `json:"cache_read,omitempty"`
	CacheWrite int64  `json:"cache_write,omitempty"`
	// CostUSD is set only when the runtime reports a price (Claude: total_cost_usd);
	// CostSource names it, e.g. "claude_estimate". Without it Core prices usage later.
	CostUSD    float64 `json:"cost_usd,omitempty"`
	CostSource string  `json:"cost_source,omitempty"`
}

// Quota is a subscription limit hit (runtime-drivers 3.2).
type Quota struct {
	// ResetsAt is when the limit lifts; zero means unknown (Core then pauses for a default period).
	ResetsAt time.Time `json:"resets_at,omitzero"`
	// Type is the runtime's limit name, e.g. "five_hour".
	Type string `json:"type,omitempty"`
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

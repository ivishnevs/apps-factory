package protocol

import (
	"appfactory/internal/decisions"
	"appfactory/internal/tasks"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
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
	// "worktree" asks the worker for a worktree of RepoURL per task; "fresh_from_default" for a task
	// worktree reset to the freshly fetched default branch at every run (no task branch).
	RepoURL       string `json:"repo_url,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	WorkspaceMode string `json:"workspace_mode,omitempty"`
	// Conversation marks a run of an agent's conversation task (hierarchy D5): it reads the project workspace
	// but does not hold its writer slot.
	Conversation bool `json:"conversation,omitempty"`
	// ProjectSetup asks the node to provision or onboard the project's repository before the run starts
	// (project-creation D2); Workspace, RepoURL and WorkspaceMode stay empty until the node reported the setup.
	ProjectSetup *ProjectSetup `json:"project_setup,omitempty"`

	// Company is set in the v2 claim answer (runtime protocol v2 §5); Core does not store it in the spec.
	Company string `json:"company,omitempty"`
	// Materialize is set in the claim answer, not stored (core-owned-project-data D3): the files the node
	// places in the worktree before the runtime starts.
	Materialize *Manifest `json:"materialize,omitempty"`
}

// ProjectSetup is what the node sets up for a project (project-creation D2): Mode local or github creates a
// repository under the node's projects_root (directory = the project ID); existing uses Workspace (an
// absolute path on the node) or clones RepoURL there. OpenSpecDir (empty when the project disabled OpenSpec)
// is imported into Core and untracked when the repository tracks it.
type ProjectSetup struct {
	Mode          string `json:"mode"`
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	RepoURL       string `json:"repo_url,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	OpenSpecDir   string `json:"openspec_dir,omitempty"`
}

// ProjectSetupReport is the node's report (POST /runs/{id}/project-setup): the repository it set up, or Error.
type ProjectSetupReport struct {
	Lease         string `json:"lease"`
	NodeID        string `json:"node_id,omitempty"`
	Error         string `json:"error,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	RepoURL       string `json:"repo_url,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	// OpenSpec are the documents of the repository's OpenSpec tree (paths relative to it) to import.
	OpenSpec []SetupDocument `json:"openspec,omitempty"`
	// Untracked: "pushed", "task_branch", "local" or "" (tasks.ProjectSetup.OpenSpecUntracked).
	Untracked string `json:"openspec_untracked,omitempty"`
}

// SetupDocument is one imported OpenSpec document.
type SetupDocument struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// ProjectSetupAnswer is Core's answer to a successful report: the run continues in this workspace with this
// manifest (the imported tree).
type ProjectSetupAnswer struct {
	Workspace     string    `json:"workspace"`
	RepoURL       string    `json:"repo_url,omitempty"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	WorkspaceMode string    `json:"workspace_mode,omitempty"`
	Materialize   *Manifest `json:"materialize,omitempty"`
}

// Manifest lists the project data a run gets as files: the project's current OpenSpec tree under OpenSpecDir
// (empty when the project disabled OpenSpec) and the run's pinned approved artifact inputs under
// .factory/inputs/<id>/<version>/. Paths are relative to the worktree. The node keeps it in
// .factory/manifest.json; the MCP adapter syncs the OpenSpec tree back against its digests.
type Manifest struct {
	OpenSpecDir string         `json:"openspec_dir,omitempty"`
	Files       []ManifestFile `json:"files"`
}

type ManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// ManifestPath is where the node keeps a run's manifest, relative to the worktree.
const ManifestPath = ".factory/manifest.json"

// InputsDir holds the materialized artifact inputs, relative to the worktree.
const InputsDir = ".factory/inputs"

// InputPath is where an uploaded artifact input is materialized: .factory/inputs/<id>/<version>/<name>, each
// segment escaped so it stays one path element.
func InputPath(a tasks.Artifact) string {
	segment := func(s, def string) string {
		if s == "" {
			s = def
		}
		s = url.PathEscape(s)
		if strings.Trim(s, ".") == "" {
			s = strings.ReplaceAll(s, ".", "%2E")
		}
		return s
	}
	return InputsDir + "/" + segment(a.ID, "artifact") + "/" + segment(a.Version, "version") + "/" + segment(a.Name, "content")
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
	RunID     string `json:"run_id"`
	Lease     string `json:"lease"`
	SessionID string `json:"session_id,omitempty"`
	// Status is ENDED on the terminal event (the run's phase), empty otherwise.
	Status string          `json:"status,omitempty"`
	Result string          `json:"result,omitempty"`
	Method string          `json:"method,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
	// Usage is the run's cumulative token use so far, one entry per model; Core keeps the last
	// usage it receives, so the terminal event of a stopped or timed-out run should carry it.
	Usage []Usage `json:"usage,omitempty"`
	// Quota reports that the runtime hit its subscription limit; Core pauses the agent's quota key
	// until ResetsAt.
	Quota *Quota `json:"quota,omitempty"`
	// Outcome, required on the terminal event and only there, is how the run ended (reliable-recovery D1):
	// succeeded, failed, timed_out, idle_timeout, max_turns, setup_failed, paused_out, revoked, or lost when
	// terminal evidence never arrived (docs/runtime-protocol-v2.md §8).
	Outcome string `json:"outcome,omitempty"`
	// ExitState, on a terminal event, is the MCP adapter's exit state (docs/explicit-exits-contract.md §5):
	// accepted, pending (an exit was queued but not delivered), refused or none.
	ExitState string `json:"exit_state,omitempty"`
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

// NATS subjects carry the company as their second token (postgres-tenancy D3): factory.<company>.<kind>....
// Company IDs contain no dots, so the company is always one token.

// CommandSubject is the run command subject of a runtime provider (stream FACTORY_COMMANDS).
func CommandSubject(company string, provider RuntimeProvider) string {
	return "factory." + company + ".commands.agent.run." + string(provider)
}

// EventSubject is the subject of an event of kind, e.g. "agent.completed" or "task.updated" (stream
// FACTORY_EVENTS).
func EventSubject(company, kind string) string { return "factory." + company + ".events." + kind }

// FeedSubject is the subject of a feed row of project (or "_company") and kind (stream FACTORY_EVENTS).
func FeedSubject(company, project, kind string) string {
	return "factory." + company + ".feed." + project + "." + kind
}

// NodeControlSubject is the control subject of a node: a plain fan-out NATS subject outside the JetStream
// streams, so a stop never waits behind queued run commands (explicit-exits-authority D7, node-agent D2).
// Core publishes RunStop there; a node's credentials may subscribe to its own control subject only.
func NodeControlSubject(company, nodeID string) string {
	return "factory." + company + ".nodes." + nodeID + ".control"
}

// IsNodeControl reports whether subject is a node's control subject.
func IsNodeControl(subject string) bool {
	parts := strings.Split(subject, ".")
	return len(parts) == 5 && parts[0] == "factory" && parts[2] == "nodes" && parts[4] == "control"
}

// SubjectCompany is the company of a factory subject, empty for any other subject.
func SubjectCompany(subject string) string {
	parts := strings.SplitN(subject, ".", 3)
	if len(parts) < 3 || parts[0] != "factory" {
		return ""
	}
	return parts[1]
}

// RunStop is the payload of a "run.stop" control message: the node stops the run if it still has it.
type RunStop struct {
	RunID  string `json:"run_id"`
	Reason string `json:"reason"`
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

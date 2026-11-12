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

// FreshSessionNote starts the prompt of a run that does not resume its task's earlier session.
const FreshSessionNote = "[App Factory] This run starts a new runtime session: the previous session of this task is not available here (it ran on another node, under another account, or the workspace was reset). Take the task's context, history and decisions from Core: this prompt and the Factory tools.\n\n"

type RunSpec struct {
	// FreshSession: Core started this run without the task's earlier session on purpose (new_session,
	// reset_workspace); the brief starts with FreshSessionNote.
	FreshSession      bool                     `json:"fresh_session,omitempty"`
	OpenSpecChange    string                   `json:"openspec_change,omitempty"`
	DecisionContext   []decisions.ContextEntry `json:"decision_context,omitempty"`
	MoreDecisions     bool                     `json:"more_decisions,omitempty"`
	ApprovedArtifacts []tasks.ApprovedArtifact `json:"approved_artifacts,omitempty"`
	MessageID         string                   `json:"message_id,omitempty"`
	TaskID            string                   `json:"task_id,omitempty"`
	Attempt           int                      `json:"attempt,omitempty"`
	ConfigHash        string                   `json:"config_hash,omitempty"`
	ConfigSnapshot    json.RawMessage          `json:"config_snapshot,omitempty"`
	// Resources are the task's node resources (throughput-signals), needed on top of the agent's spec.resources.
	Resources map[string]int `json:"resources,omitempty"`
	Skills    []string       `json:"skills,omitempty"`

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
	// ReviewCopy (node-ties): the run's agent is not the task's implementer (reviewer, QA, accepting manager); a
	// worktree run works in a disposable copy of the pushed task branch, never sealed, reported or pushed.
	// ReviewCarry are the project's workspace.review_carry directories the node copies into it.
	ReviewCopy  bool     `json:"review_copy,omitempty"`
	ReviewCarry []string `json:"review_carry,omitempty"`
	// RestoreWIP is set in the claim answer, not stored (node-ties): the task's work saved to
	// refs/factory/wip/<task>, whose files the node restores when its worktree does not hold them.
	RestoreWIP string `json:"restore_wip,omitempty"`
	// WorkspaceNode, also in the claim answer only, is the node of the task's last workspace report: when it is
	// another node, this node's leftovers it saved remotely are not continued (node-ties).
	WorkspaceNode string `json:"workspace_node,omitempty"`
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
	// Requires is set in the claim answer from the project (node-readiness): the node checks the tools before the
	// runtime starts, and a missing one ends the run setup_failed with a node fault.
	Requires []tasks.Requirement `json:"requires,omitempty"`
}

// ProjectSetup is what the node sets up for a project (project-creation D2): Mode local or github creates a
// repository under the node's projects_root (directory = the project ID); existing uses Workspace (an
// absolute path on the node) or clones RepoURL there, leaving it as it is.
type ProjectSetup struct {
	Mode          string `json:"mode"`
	Name          string `json:"name,omitempty"`
	Description   string `json:"description,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	RepoURL       string `json:"repo_url,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
}

// ProjectSetupReport is the node's report (POST /runs/{id}/project-setup): the repository it set up, or Error.
type ProjectSetupReport struct {
	Lease         string `json:"lease"`
	NodeID        string `json:"node_id,omitempty"`
	Error         string `json:"error,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	RepoURL       string `json:"repo_url,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
}

// ProjectSetupAnswer is Core's answer to a successful report: the run continues in this workspace with this
// manifest.
type ProjectSetupAnswer struct {
	Workspace     string    `json:"workspace"`
	RepoURL       string    `json:"repo_url,omitempty"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	WorkspaceMode string    `json:"workspace_mode,omitempty"`
	Materialize   *Manifest `json:"materialize,omitempty"`
}

// Manifest lists the project data a run gets as files: the run's pinned approved artifact inputs under
// .factory/inputs/<id>/<version>/. Paths are relative to the worktree. The node keeps it in
// .factory/manifest.json. OpenSpec is not part of it: it is in the checkout (openspec-in-git).
type Manifest struct {
	Files []ManifestFile `json:"files"`
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
	// succeeded, failed, timed_out, idle_timeout, max_turns, setup_failed, change_missing, paused_out, revoked, or lost when
	// terminal evidence never arrived (docs/runtime-protocol-v2.md §8).
	Outcome string `json:"outcome,omitempty"`
	// ExitState, on a terminal event, is the MCP adapter's exit state (docs/explicit-exits-contract.md §5):
	// accepted, pending (an exit was queued but not delivered), refused or none.
	ExitState string `json:"exit_state,omitempty"`
	// Fault, on a terminal setup_failed event, says the node's environment is the cause (node-readiness): Core
	// does not count the run against its task and keeps the project's runs off this node until it passes its
	// check again.
	Fault *NodeFault `json:"fault,omitempty"`
}

// Node fault kinds (node-readiness): what in the node's environment kept a run from starting.
const (
	FaultRepoAccess = "repo_access" // the node cannot clone or fetch the project's repository
	FaultWorktree   = "worktree"    // the node cannot create the task's worktree or workspace
	FaultRuntime    = "runtime"     // the runtime binary is missing or refuses to start (root, packaging)
	FaultSandbox    = "sandbox"     // the runtime's sandbox cannot start (bwrap, socat, user namespaces)
	FaultTool       = "tool"        // a tool the project requires is missing or too old
)

// NodeFault is a node-side cause of a failed run or readiness check: Detail says what failed, Fix how to fix it.
type NodeFault struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// ProjectCheck is a project's readiness check Core asks a node to run (node-readiness): clone access to Repo
// (forge.repo) and the Requires tools. The node runs it again when Generation or the check's contents change.
type ProjectCheck struct {
	Project    string              `json:"project"`
	Repo       string              `json:"repo,omitempty"`
	Requires   []tasks.Requirement `json:"requires,omitempty"`
	Generation int64               `json:"generation"`
}

// ProjectReadiness is a node's result of a ProjectCheck: Faults lists what failed (none: ready).
type ProjectReadiness struct {
	Project    string      `json:"project"`
	Generation int64       `json:"generation"`
	Faults     []NodeFault `json:"faults,omitempty"`
	CheckedAt  int64       `json:"checked_at"`
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

// SealedSecret is a company secret in a claim answer, sealed to the claiming node (core-secret-store D4): it opens
// with the node's encryption key and the info company|run|node|name|version only.
type SealedSecret struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Version int64  `json:"version"`
	Sealed  []byte `json:"sealed"`
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

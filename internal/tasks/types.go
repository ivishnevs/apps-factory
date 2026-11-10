// Package tasks defines the runtime-independent task lifecycle and API contracts.
package tasks

type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Team is the team of the company's catalog the project belongs to (company-teams D4): its tasks go to
	// agents of that team only.
	Team        string `json:"team,omitempty"`
	Description string `json:"description"`
	Workspace   string `json:"workspace"`
	Version     int    `json:"version"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
	// RepoURL enables worktrees for agents in workspace mode worktree; DefaultBranch empty is the
	// remote HEAD (task-worktrees 1.1).
	RepoURL       string `json:"repo_url,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
	// ReviewAfterConflicts (task-worktrees D7): "required" makes done refuse a pull request whose head moved
	// after the approval (a new submit and approval first); "agent_decides" (default) leaves it to the agent.
	ReviewAfterConflicts string `json:"review_after_conflicts,omitempty"`
	// Rules are the project's standing conventions, facts and fixed decisions, carried by every brief.
	Rules string `json:"rules,omitempty"`
	// Direction is the project's goal and direction (lead-brainstorm-mode): what the lead decides against without
	// asking; carried by every brief next to the rules.
	Direction string `json:"direction,omitempty"`
	// Archived hides the project from the lists (projects-page); its history stays.
	Archived bool `json:"archived,omitempty"`
	// Approvals overrides its team's approval modes (approval-delegation); empty modes inherit.
	Approvals ApprovalModes `json:"approvals,omitzero"`
	// RepoMode is how the project's repository came to be (project-creation D1): "local" (a new git repository
	// the node creates under its projects_root), "github" (a new private GitHub repository created through the
	// node's gh, cloned there), "existing" (a repository that exists: workspace path and/or repo_url, onboarded
	// by the node). Empty for projects created with a workspace and no repo_mode (no onboarding, as before).
	RepoMode string `json:"repo_mode,omitempty"`
	// CreatedBy is "owner" or the agent whose approved factory_project_create created the project;
	// ApprovalID is that approval.
	CreatedBy  string `json:"created_by,omitempty"`
	ApprovalID string `json:"approval_id,omitempty"`
	// Setup is the repository provisioning / onboarding state (project-creation D2); nil when none is needed.
	Setup *ProjectSetup `json:"setup,omitempty"`
	// Requires are the tools the project's runs need on a node (node-readiness); nodes probe them with the
	// repository access and Core offers the project's runs only to nodes that have them.
	Requires []Requirement `json:"requires,omitempty"`
	// ReviewCarry is the project's workspace.review_carry (node-ties): directories ignored by git (build or
	// import caches) the node copies into a review copy from the task's worktree on that node. Empty by default;
	// nothing else ignored by git is ever carried.
	ReviewCarry []string `json:"review_carry,omitempty"`
}

// Ready: the project's repository needs no setup or it finished.
func (p Project) Ready() bool { return p.Setup == nil || p.Setup.State == SetupReady }

// Project setup states (project-creation D2). The node that claims the project's first task run performs the
// setup before that run starts; until it is ready no other task of the project starts.
const (
	SetupPending = "pending" // waiting for the project's first task run
	SetupRunning = "running" // a run was dispatched to perform it (RunID, TaskID)
	SetupReady   = "ready"
	SetupFailed  = "failed" // Reason says why; the task's next attempt retries it
)

// ProjectSetup is the provisioning / onboarding state of a project's repository.
type ProjectSetup struct {
	State  string `json:"state"`
	TaskID string `json:"task_id,omitempty"`
	RunID  string `json:"run_id,omitempty"`
	NodeID string `json:"node_id,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Audit asked for an onboarding audit; AuditTaskID is the task Core created for it.
	Audit       bool   `json:"audit,omitempty"`
	AuditTaskID string `json:"audit_task_id,omitempty"`
	UpdatedAt   int64  `json:"updated_at,omitempty"`
}

// Repository modes of POST /projects (project-creation D1).
const (
	RepoLocal    = "local"
	RepoGitHub   = "github"
	RepoExisting = "existing"
)

type Artifact struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	URI     string `json:"uri"`
	// SHA256 and Size are the content of an uploaded version (URI factory://artifacts/<id>/<version>);
	// approval binds to the digest. Name is the uploaded file's name. External URIs carry none of them.
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Name   string `json:"name,omitempty"`
}
type ApprovedArtifact struct {
	Artifact        Artifact `json:"artifact"`
	ApprovalID      string   `json:"approval_id"`
	ApprovalVersion int      `json:"approval_version"`
}
type Submission struct {
	Summary   string     `json:"summary"`
	Commit    string     `json:"commit,omitempty"`
	Checks    []string   `json:"checks,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}
type Task struct {
	RequiredArtifacts []Artifact  `json:"required_artifacts,omitempty"`
	WaitMessageID     string      `json:"wait_message_id,omitempty"`
	ID                string      `json:"id"`
	ProjectID         string      `json:"project_id"`
	Title             string      `json:"title"`
	Description       string      `json:"description"`
	Status            string      `json:"status"`
	Priority          int         `json:"priority"`
	Assignee          string      `json:"assignee"`
	CreatedBy         string      `json:"created_by"`
	ParentTaskID      string      `json:"parent_task_id,omitempty"`
	OpenSpecChange    string      `json:"openspec_change,omitempty"`
	Dependencies      []string    `json:"dependencies"`
	CreatedAt         int64       `json:"created_at"`
	UpdatedAt         int64       `json:"updated_at"`
	Version           int         `json:"version"`
	CurrentRunID      string      `json:"current_run_id,omitempty"`
	Attempt           int         `json:"attempt"`
	Result            string      `json:"result,omitempty"`
	Submission        *Submission `json:"submission,omitempty"`
	ReviewFeedback    string      `json:"review_feedback,omitempty"`
	BlockReason       string      `json:"block_reason,omitempty"`
	CancelRequested   bool        `json:"cancel_requested"`
	// OriginTask and OriginRun record the run that created this task through Factory MCP.
	OriginTask string `json:"origin_task,omitempty"`
	OriginRun  string `json:"origin_run,omitempty"`
	// Deduplicated is set only on a create response that returned an existing open task.
	Deduplicated bool `json:"deduplicated,omitempty"`
	// Automatic retry (reliable-recovery D2): attempt outcomes in a row without progress for the
	// current assignee, backoff outcomes in a row, and the earliest time the next run may start.
	AttemptsNoProgress int   `json:"attempts_no_progress,omitempty"`
	FailStreak         int   `json:"fail_streak,omitempty"`
	NotBefore          int64 `json:"not_before,omitempty"`
	// ProgressAt is the last progress (D2: a successful run with progress, or authority input); runs
	// without progress since then for no_progress_alert_after raise an alert (D8).
	ProgressAt int64 `json:"progress_at,omitempty"`
	// SideEffects marks a task whose runs act outside the workspace (publish, deploy): an unclear
	// outcome parks it for inspection instead of retrying.
	SideEffects bool `json:"side_effects,omitempty"`
	// Implementer submitted the task (factory_exit_submit); Reviewer is the agent asked to review it and is
	// its assignee while the task is IN_REVIEW.
	// CreatedByPerson is the person behind created_by owner (people-attribution).
	CreatedByPerson string `json:"created_by_person,omitempty"`
	Implementer     string `json:"implementer,omitempty"`
	Reviewer        string `json:"reviewer,omitempty"`
	// ReviewBy is who reviews the task's work (review-coverage): an agent, "owner" or "member:USER". Required on
	// every task outside the backlog (Core refuses ready without it); a submit naming no reviewer goes to it.
	ReviewBy string `json:"review_by,omitempty"`
	// FreshSession: the next run starts a new runtime session (reset_workspace moved the task off its node).
	FreshSession bool `json:"fresh_session,omitempty"`
	// BlockedOn are the dependencies a factory_exit_block added (explicit-exits-authority D1): authority input
	// removes them again (the block is overridden); the task's next exit forgets them.
	BlockedOn []string `json:"blocked_on,omitempty"`
	// Model overrides the assignee's spec.runtime.model for this task's runs (runtime-drivers D1): set by an
	// authority and validated against the assignee's provider then; a run whose assignee's provider does not
	// fit it (after a reassignment) uses the agent's model and records a task.model_ignored event.
	Model string `json:"model,omitempty"`
	// Kind "conversation" marks an agent's long-lived conversation task with its boss (hierarchy D5): messages,
	// questions and escalations addressed to the agent are its triggers; done marks them handled, it stays open.
	Kind string `json:"kind,omitempty"`
	// Counterpart is a conversation task's other side: the agent's reports_to (an agent or owner).
	Counterpart string `json:"counterpart,omitempty"`
	// PausedBy is the authority whose pause holds the task (hierarchy D2); set on reads.
	PausedBy string `json:"paused_by,omitempty"`
}

// KindConversation is the kind of an agent's conversation task.
const KindConversation = "conversation"

type ProjectCreate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Team is the project's team; Core checks it against the catalog (the creating agent's team for
	// factory_project_create, the only team when the catalog has one).
	Team        string `json:"team,omitempty"`
	Description string `json:"description"`
	// Workspace is an absolute path on the node; optional (project-creation D1).
	Workspace            string `json:"workspace,omitempty"`
	RequestID            string `json:"request_id,omitempty"`
	RepoURL              string `json:"repo_url,omitempty"`
	DefaultBranch        string `json:"default_branch,omitempty"`
	ReviewAfterConflicts string `json:"review_after_conflicts,omitempty"`
	Rules                string `json:"rules,omitempty"`
	// RepoMode: local (the default without workspace and repo_url), github or existing (Project.RepoMode).
	RepoMode string `json:"repo_mode,omitempty"`
	// Audit (an existing repository) makes Core create an onboarding audit task for the configured agent.
	Audit bool `json:"audit,omitempty"`
	// Requires: Project.Requires.
	Requires []Requirement `json:"requires,omitempty"`
	// CreatedBy and ApprovalID are set by Core (an agent's approved factory_project_create), never decoded.
	CreatedBy  string `json:"-"`
	ApprovalID string `json:"-"`
}
type ProjectUpdate struct {
	Version     int    `json:"version"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RequestID   string `json:"request_id,omitempty"`
	// Nil keeps the current value; "" clears it.
	RepoURL              *string        `json:"repo_url,omitempty"`
	DefaultBranch        *string        `json:"default_branch,omitempty"`
	ReviewAfterConflicts *string        `json:"review_after_conflicts,omitempty"`
	Rules                *string        `json:"rules,omitempty"`
	Direction            *string        `json:"direction,omitempty"`
	Archived             *bool          `json:"archived,omitempty"`
	Approvals            *ApprovalModes `json:"approvals,omitempty"`
	// Team moves the project to another team, only while it has no live tasks (company-teams D4).
	Team *string `json:"team,omitempty"`
	// Requires replaces the project's requirements (node-readiness); nil keeps them, [] clears them.
	Requires *[]Requirement `json:"requires,omitempty"`
	// ReviewCarry replaces the project's workspace.review_carry (node-ties); [] clears it.
	ReviewCarry *[]string `json:"review_carry,omitempty"`
}

// Workspace is a task's workspace as its worker last reported it (task-worktrees D1); files stay on the node.
type Workspace struct {
	TaskID     string `json:"task_id"`
	Mode       string `json:"mode"`
	Branch     string `json:"branch,omitempty"`
	NodeID     string `json:"node_id,omitempty"`
	ExitSHA    string `json:"exit_sha,omitempty"`
	SealedHead string `json:"sealed_head,omitempty"`
	SealedAt   int64  `json:"sealed_at,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	UpdatedAt  int64  `json:"updated_at"`
	// Unpushed: the node holds work of the task that is not on the remote and could not be saved remotely
	// either; the task is tied to NodeID by its work (task-worktrees D8, node-ties).
	Unpushed bool `json:"unpushed,omitempty"`
	// WIPSHA is the task's work saved to refs/factory/wip/<task> (node-ties): the next run restores its files
	// on any node. WIPUnsaved says why local work could not be saved; TiedSince when the work tie began.
	WIPSHA     string `json:"wip_sha,omitempty"`
	WIPUnsaved string `json:"wip_unsaved,omitempty"`
	TiedSince  int64  `json:"tied_since,omitempty"`
}
type Create struct {
	RequiredArtifacts []Artifact `json:"required_artifacts,omitempty"`
	ID                string     `json:"id,omitempty"`
	ProjectID         string     `json:"project_id"`
	Title             string     `json:"title"`
	Description       string     `json:"description"`
	Priority          int        `json:"priority"`
	Assignee          string     `json:"assignee"`
	CreatedBy         string     `json:"created_by,omitempty"`
	ParentTaskID      string     `json:"parent_task_id,omitempty"`
	OpenSpecChange    string     `json:"openspec_change,omitempty"`
	Dependencies      []string   `json:"dependencies"`
	RequestID         string     `json:"request_id,omitempty"`
	Actor             string     `json:"actor,omitempty"`
	// Ready creates the task released for scheduling (READY, or waiting for its artifacts).
	Ready bool `json:"ready,omitempty"`
	// ReviewBy is the task's reviewer (Task.ReviewBy); Core requires it with Ready.
	ReviewBy string `json:"review_by,omitempty"`
	// SideEffects: the task's runs act outside the workspace; failures are inspected, not retried.
	SideEffects bool `json:"side_effects,omitempty"`
	// Trusted metadata set by Core, never decoded from a request body.
	OriginTask string            `json:"-"`
	OriginRun  string            `json:"-"`
	Roles      map[string]string `json:"-"` // agent name → role, from the Core catalog
}
type Update struct {
	RequiredArtifacts *[]Artifact `json:"required_artifacts,omitempty"`
	Version           int         `json:"version"`
	Title             *string     `json:"title,omitempty"`
	Description       *string     `json:"description,omitempty"`
	Priority          *int        `json:"priority,omitempty"`
	Assignee          *string     `json:"assignee,omitempty"`
	OpenSpecChange    *string     `json:"openspec_change,omitempty"`
	Dependencies      *[]string   `json:"dependencies,omitempty"`
	SideEffects       *bool       `json:"side_effects,omitempty"`
	// ReviewBy sets the task's reviewer; Core refuses clearing it outside the backlog.
	ReviewBy *string `json:"review_by,omitempty"`
	// Model sets the task's model override ("" clears it); only an authority over the task may set it.
	Model     *string `json:"model,omitempty"`
	RequestID string  `json:"request_id,omitempty"`
	Actor     string  `json:"actor,omitempty"`
}
type Action struct {
	Version   int    `json:"version"`
	Action    string `json:"action"`
	Reason    string `json:"reason,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Actor     string `json:"actor,omitempty"`
	// Cascade cancels the task's open children with it; cancelling a task with open children needs it.
	Cascade bool `json:"cascade,omitempty"`
}

// Authority is an authority action on a task (hierarchy D2): pause, resume, reassign or reprioritize. Actor is
// the agent acting ("" for the owner); Core checks the authority and sets Rank.
type Authority struct {
	Version   int    `json:"version"`
	Reason    string `json:"reason,omitempty"`
	Assignee  string `json:"assignee,omitempty"`
	Priority  *int   `json:"priority,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Actor     string `json:"actor,omitempty"`
	// Rank of the actor: 0 the owner, otherwise its depth in the tree (1 reports to the owner).
	Rank int `json:"-"`
}
type Submit struct {
	Version    int        `json:"version"`
	RunID      string     `json:"run_id"`
	Submission Submission `json:"submission"`
	RequestID  string     `json:"request_id,omitempty"`
	Actor      string     `json:"actor,omitempty"`
}

// Comment adds a comment; a comment by the owner or another agent wakes the task (owner_comment).
type Comment struct {
	Text      string `json:"text"`
	RequestID string `json:"request_id,omitempty"`
	Actor     string `json:"actor,omitempty"`
	// Authority is set by Core (never decoded): the owner or an agent with authority over the task.
	Authority bool `json:"-"`
}
type Review struct {
	Version   int    `json:"version"`
	Accept    bool   `json:"accept"`
	Feedback  string `json:"feedback,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Actor     string `json:"actor,omitempty"`
}

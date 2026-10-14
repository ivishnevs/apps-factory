// Package tasks defines the runtime-independent task lifecycle and API contracts.
package tasks

type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
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
}
type Artifact struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	URI     string `json:"uri"`
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
	RetryEligible     bool        `json:"retry_eligible"`
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
	// SideEffects marks a task whose runs act outside the workspace (publish, deploy): an unclear
	// outcome parks it for inspection instead of retrying.
	SideEffects bool `json:"side_effects,omitempty"`
	// Implementer submitted the task (factory_exit_submit); Reviewer is the agent asked to review it and is
	// its assignee while the task is IN_REVIEW.
	Implementer string `json:"implementer,omitempty"`
	Reviewer    string `json:"reviewer,omitempty"`
}
type ProjectCreate struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Description          string `json:"description"`
	Workspace            string `json:"workspace"`
	RequestID            string `json:"request_id,omitempty"`
	RepoURL              string `json:"repo_url,omitempty"`
	DefaultBranch        string `json:"default_branch,omitempty"`
	ReviewAfterConflicts string `json:"review_after_conflicts,omitempty"`
}
type ProjectUpdate struct {
	Version     int    `json:"version"`
	Name        string `json:"name"`
	Description string `json:"description"`
	RequestID   string `json:"request_id,omitempty"`
	// Nil keeps the current value; "" clears it.
	RepoURL              *string `json:"repo_url,omitempty"`
	DefaultBranch        *string `json:"default_branch,omitempty"`
	ReviewAfterConflicts *string `json:"review_after_conflicts,omitempty"`
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
	// Unpushed: the node holds work of the task that is not on the remote (unpushed commits, or a WIP ref
	// newer than the remote branch); the task is pinned to NodeID (task-worktrees D8).
	Unpushed bool `json:"unpushed,omitempty"`
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
	RequestID         string      `json:"request_id,omitempty"`
	Actor             string      `json:"actor,omitempty"`
}
type Action struct {
	Version   int    `json:"version"`
	Action    string `json:"action"`
	Reason    string `json:"reason,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Actor     string `json:"actor,omitempty"`
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
}
type Review struct {
	Version   int    `json:"version"`
	Accept    bool   `json:"accept"`
	Feedback  string `json:"feedback,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Actor     string `json:"actor,omitempty"`
}

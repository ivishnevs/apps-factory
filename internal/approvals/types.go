// Package approvals defines durable owner decisions for exact Factory tool calls.
package approvals

import "appfactory/internal/factorytools"
import "appfactory/internal/tasks"
import "appfactory/internal/messaging"

type Approval struct {
	Kind           string            `json:"kind,omitempty"`
	Artifact       *tasks.Artifact   `json:"artifact,omitempty"`
	ID             string            `json:"id"`
	ProjectID      string            `json:"project_id"`
	Agent          string            `json:"agent"`
	TaskID         string            `json:"task_id,omitempty"`
	RunID          string            `json:"run_id"`
	MessageID      string            `json:"message_id"`
	Call           factorytools.Call `json:"call"`
	Reason         string            `json:"reason"`
	Status         string            `json:"status"`
	Version        int               `json:"version"`
	CreatedAt      int64             `json:"created_at"`
	ExpiresAt      int64             `json:"expires_at"`
	DecidedAt      int64             `json:"decided_at,omitempty"`
	DecisionReason string            `json:"decision_reason,omitempty"`
	// Delegation (approval-delegation): Audience internal|external of an artifact; Delegate the agent that decides
	// under Setting (e.g. "team game: internal_artifacts agent game-designer") until it escalates to people;
	// DecidedBy the agent that decided and Cites what it relied on.
	Audience  string `json:"audience,omitempty"`
	Delegate  string `json:"delegate,omitempty"`
	Setting   string `json:"setting,omitempty"`
	Escalated bool   `json:"escalated,omitempty"`
	DecidedBy string `json:"decided_by,omitempty"`
	// DecidedByPerson is the person who decided (people-attribution); empty for agents and the CLI.
	DecidedByPerson string   `json:"decided_by_person,omitempty"`
	Cites           []string `json:"cites,omitempty"`
	// CommentID is the board comment the owner's decision reason became on the approval's task
	// (approval-comments-handled): every reason but the default one is the board's word on the work.
	CommentID string `json:"comment_id,omitempty"`
	// Comment is that comment's handling, filled on read (never stored).
	Comment *CommentState `json:"comment,omitempty"`
}

// CommentState is what became of a decision's comment (approval-comments-handled).
type CommentState struct {
	ID string `json:"id"`
	// Status is open (not handled yet), done, forwarded or declined.
	Status string `json:"status"`
	// Outcome is the handling in words ("done by launch-creative: frame 1 without the window", "not handled yet").
	Outcome string `json:"outcome"`
}
type Request struct {
	ProjectID, Agent, TaskID, RunID string
	Call                            factorytools.Call `json:"call"`
	Reason                          string            `json:"reason"`
	Blocking                        bool              `json:"blocking"`
	TimeoutSeconds                  int               `json:"timeout_seconds"`
	RequestID                       string            `json:"request_id"`
	// Kind "command" makes a single-use grant for one exact shell argv (command guards); set by Core.
	Kind string `json:"-"`
	// What people read (people-topics D3): the requester's brief, note and an explicit topic.
	Brief  *messaging.Brief    `json:"brief,omitempty"`
	Note   *messaging.ItemNote `json:"note,omitempty"`
	Topic  string              `json:"topic,omitempty"`
	Strict bool                `json:"-"`
}
type Decision struct {
	// DecidedBy and Cites: an agent's decision under delegation (set by Core); empty for people.
	DecidedBy string   `json:"-"`
	Cites     []string `json:"-"`
	Version   int      `json:"version"`
	Action    string   `json:"action"`
	Reason    string   `json:"reason"`
	RequestID string   `json:"request_id"`
	// Urgent delivers an approved artifact version like an urgent directive (explicit-exits-authority D6):
	// live runs of the tasks it wakes are stopped and resume with it.
	Urgent bool `json:"urgent,omitempty"`
}

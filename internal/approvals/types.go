// Package approvals defines durable owner decisions for exact Factory tool calls.
package approvals

import (
	"fmt"
	"strconv"
	"strings"

	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

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
	// A choice approval (artifact-choice-approvals) is one approval over 2–6 Options, numbered from 1; Artifact is
	// empty until one is chosen, then it is the chosen option's version (the approved one). Recommended is the
	// requester's recommendation, Chosen the chosen option (0: none) and ChangesOn the options a request for changes
	// names (status CHANGES_REQUESTED).
	Options     []Option `json:"options,omitempty"`
	Recommended int      `json:"recommended,omitempty"`
	Chosen      int      `json:"chosen,omitempty"`
	ChangesOn   []int    `json:"changes_on,omitempty"`
}

// Choice approval decisions and statuses (artifact-choice-approvals).
const (
	ActionChoose  = "choose"  // choose one option (Decision.Option); the comment is optional
	ActionChanges = "changes" // ask for changes (Decision.Reason is what to change; Decision.Options which options)
	ActionReject  = "reject"  // reject all (Decision.Reason)
	// StatusChanges: people (or the delegate) asked for changes; no option is approved.
	StatusChanges = "CHANGES_REQUESTED"
	// NotChosen is the state of an option of an approved choice that was not chosen: not approved, not rejected.
	NotChosen = "NOT_CHOSEN"
	// MinOptions and MaxOptions bound a choice approval.
	MinOptions = 2
	MaxOptions = 6
)

// Option is one artifact version of a choice approval.
type Option struct {
	Artifact tasks.Artifact `json:"artifact"`
	Label    string         `json:"label"`
	Note     string         `json:"note,omitempty"`
	// Dropped is the reason the delegate (DroppedBy) gave when it dropped the option before escalating: people see it
	// collapsed and may still choose it.
	Dropped   string `json:"dropped,omitempty"`
	DroppedBy string `json:"dropped_by,omitempty"`
}

// Choice tells whether a is a choice approval.
func (a Approval) Choice() bool { return len(a.Options) > 0 }

// OptionOf is the number (from 1) of the option that is artifact id at version, 0 when none is.
func (a Approval) OptionOf(id, version string) int {
	for i, o := range a.Options {
		if o.Artifact.ID == id && o.Artifact.Version == version {
			return i + 1
		}
	}
	return 0
}

// StatusOf is a's status for the artifact version id@version: an approved choice's other options are NOT_CHOSEN.
func (a Approval) StatusOf(id, version string) string {
	if a.Choice() && a.Status == "APPROVED" && a.OptionOf(id, version) != a.Chosen {
		return NotChosen
	}
	return a.Status
}

// OptionName names option n (from 1) for people and agents: its label and version ("V2 (lens@2)").
func (a Approval) OptionName(n int) string {
	if n < 1 || n > len(a.Options) {
		return fmt.Sprintf("option %d", n)
	}
	o := a.Options[n-1]
	return o.Label + " (" + o.Artifact.ID + "@" + o.Artifact.Version + ")"
}

// ResolveOption finds the option ref names: its number (from 1), its label, its "id@version", or its artifact id
// when only one option has it. 0 and an error when none or several match.
func (a Approval) ResolveOption(ref any) (int, error) {
	n := len(a.Options)
	switch v := ref.(type) {
	case float64:
		if v == float64(int(v)) && int(v) >= 1 && int(v) <= n {
			return int(v), nil
		}
		return 0, fmt.Errorf("option %v: options are numbered 1 to %d", v, n)
	case int:
		if v >= 1 && v <= n {
			return v, nil
		}
		return 0, fmt.Errorf("option %d: options are numbered 1 to %d", v, n)
	case string:
		s := strings.TrimSpace(v)
		if i, err := strconv.Atoi(s); err == nil {
			return a.ResolveOption(i)
		}
		for i, o := range a.Options {
			if strings.EqualFold(o.Label, s) || o.Artifact.ID+"@"+o.Artifact.Version == s {
				return i + 1, nil
			}
		}
		found := 0
		for i, o := range a.Options {
			if o.Artifact.ID == s {
				if found != 0 {
					return 0, fmt.Errorf("option %q: several options are versions of %s, name one as id@version", s, s)
				}
				found = i + 1
			}
		}
		if found != 0 {
			return found, nil
		}
		return 0, fmt.Errorf("option %q is none of %s", s, a.optionList())
	}
	return 0, fmt.Errorf("option: a number from 1 to %d, a label or id@version", n)
}

func (a Approval) optionList() string {
	names := make([]string, len(a.Options))
	for i := range a.Options {
		names[i] = fmt.Sprintf("%d %s", i+1, a.OptionName(i+1))
	}
	return strings.Join(names, ", ")
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
	// Option is the option (from 1) action choose chooses; Options the options action changes names (optional).
	Option  int   `json:"option,omitempty"`
	Options []int `json:"options,omitempty"`
}

// Drop is an option a delegate drops before escalating a choice approval, with its reason.
type Drop struct {
	Option int
	Reason string
}

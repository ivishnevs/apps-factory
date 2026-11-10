package tasks

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
)

// node-ties: every reason a task or a queued run may run only on one node is a tie with that node. The old names
// (verdict and escalation pinned_node_unavailable, queued reasons pinned and affinity, Snapshot.PinnedNode) stay
// as aliases for one release; the tie is what the web, the TUI, escalations and docs show.

// Tie reasons.
const (
	TieWork    = "work"    // the task's unsaved work is only on that node
	TieSession = "session" // the agent's session is on that node
)

// Tie is why a task or a queued run waits for one node and how that ends.
type Tie struct {
	Node     string `json:"node"`
	NodeName string `json:"node_name,omitempty"`
	Reason   string `json:"reason"`
	// Since is when the tie began (the work tie's seal, the session holder's inactivity or its last run).
	Since int64 `json:"since,omitempty"`
	// Release says how the tie ends; ReleaseAt is when a session tie is released automatically (0: not timed).
	Release   string `json:"release"`
	ReleaseAt int64  `json:"release_at,omitempty"`
	// Unsaved is why the work could not be saved remotely (a work tie only).
	Unsaved string `json:"unsaved,omitempty"`
}

// WorkRelease is how a work tie ends.
const WorkRelease = "when the work is saved remotely or after the owner resets the workspace"

// SessionRelease is how a session tie ends with the company's timeout.
func SessionRelease(timeout time.Duration) string {
	return fmt.Sprintf("%s after the node goes inactive", minutes(timeout))
}

func minutes(d time.Duration) string {
	if d%time.Hour == 0 && d >= 2*time.Hour {
		return fmt.Sprintf("%d h", d/time.Hour)
	}
	return fmt.Sprintf("%d min", d/time.Minute)
}

// Label is the tie's node as people read it: "name (id)" or the ID.
func (t Tie) Label() string {
	if t.NodeName != "" && t.NodeName != t.Node {
		return t.NodeName + " (" + t.Node + ")"
	}
	return t.Node
}

// Say is the one wording: "tied to <node>: its unsaved work is there" or "…: its agent session is there".
func (t Tie) Say() string {
	if t.Reason == TieSession {
		return "tied to " + t.Label() + ": its agent session is there"
	}
	s := "tied to " + t.Label() + ": its unsaved work is there"
	if t.Unsaved != "" {
		s += " (it could not be saved: " + t.Unsaved + ")"
	}
	return s
}

// MaxReviewCarry bounds a project's workspace.review_carry list.
const MaxReviewCarry = 16

// ValidReviewCarry checks one workspace.review_carry entry (node-ties): a relative directory inside the
// repository, without "..", not .git or .factory.
func ValidReviewCarry(p string) error {
	c := path.Clean(p)
	if p == "" || len(p) > 256 || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || c == "." || strings.ContainsAny(p, "\\\x00\n") ||
		slices.Contains(strings.Split(p, "/"), "..") || c == ".git" || strings.HasPrefix(c, ".git/") || c == ".factory" || strings.HasPrefix(c, ".factory/") {
		return fmt.Errorf("review_carry %q must be a relative directory inside the repository, without ..", p)
	}
	return nil
}

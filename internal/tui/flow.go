package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"appfactory/internal/storage"
)

// Interaction is one step of work passing between parties: the owner, agents and Core ("factory").
type Interaction struct {
	Seq       int64
	At        time.Time
	Project   string
	Task      string
	From, To  string
	Verb      string // create, handoff, submit, approved, changes_requested, question, answer, directive, comment, ...
	Text      string
	Attention bool
}

// interactionOf reads who passed what to whom from a feed row; ok is false for rows that are not an
// interaction (run starts, retries, node events, ...).
func interactionOf(item storage.FeedItem) (Interaction, bool) {
	in := Interaction{Seq: item.Sequence, At: item.CreatedAt, Project: item.ProjectID, Task: item.TaskID, Text: item.Summary, Attention: item.Attention}
	actor := item.Actor
	switch t := item.Type; {
	case t == "task.created":
		in.From, in.To, in.Verb = orOwner(actor), item.Agent, "create"
	case strings.HasPrefix(t, "task.exit."), strings.HasPrefix(t, "message."):
		from, to, verb, text, ok := parseRoute(item.Summary)
		if !ok {
			// "agent done: ...", "agent asked: ...", "agent blocked: ...", "agent waiting until ...".
			in.From, in.Verb = item.Agent, strings.TrimPrefix(t, "task.exit.")
			if name, rest, found := strings.Cut(item.Summary, ": "); found && strings.HasPrefix(name, item.Agent+" ") {
				in.Text = rest
			}
			if in.From == "" {
				return in, false
			}
			return in, true
		}
		in.From, in.To, in.Verb, in.Text = from, to, verb, text
		switch t {
		case "message.escalated":
			in.Verb = "escalated"
		case "message.forwarded", "message.auto_forwarded":
			in.Verb = "forwarded"
		case "message.created":
		default:
			if strings.HasPrefix(t, "message.") {
				return in, false // read, withdrawn, delivered: not a new step
			}
		}
	case strings.HasPrefix(t, "task.trigger."):
		kind := strings.TrimPrefix(t, "task.trigger.")
		switch kind {
		case "directive", "answer", "review_result", "comment":
		default:
			return in, false
		}
		in.From, in.To, in.Verb = orOwner(actor), item.Agent, kind
	case t == "task.commented":
		in.From, in.To, in.Verb = orOwner(actor), item.Agent, "comment"
	case t == "task.reassigned", t == "task.paused", t == "task.resumed", t == "task.reprioritized":
		in.From, in.To, in.Verb = orOwner(actor), item.Agent, strings.TrimPrefix(t, "task.")
	case strings.HasPrefix(t, "task.escalation."):
		in.From, in.Verb = item.Agent, "escalate"
		if i := strings.LastIndex(item.Summary, " → "); i >= 0 {
			in.To, in.Text = item.Summary[i+len(" → "):], item.Summary[:i]
		}
	case t == "approval.requested":
		in.From, in.To, in.Verb = item.Agent, "owner", "approval"
	default:
		return in, false
	}
	if in.From == "" && in.To == "" {
		return in, false
	}
	return in, true
}

func orOwner(s string) string {
	if s == "" {
		return "owner"
	}
	return s
}

// parseRoute reads "from → to (verb): text".
func parseRoute(s string) (from, to, verb, text string, ok bool) {
	head, text, found := strings.Cut(s, ": ")
	if !found {
		head = s
	}
	from, rest, found := strings.Cut(head, " → ")
	if !found || strings.Contains(from, " ") {
		return "", "", "", "", false
	}
	to, verb, _ = strings.Cut(rest, " (")
	return from, to, strings.TrimSuffix(verb, ")"), text, true
}

// FlowFilter narrows the flow to a project, a task and a party.
type FlowFilter struct {
	Project, Task, Agent string
}

func (f FlowFilter) matches(in Interaction) bool {
	return (f.Project == "" || in.Project == f.Project) && (f.Task == "" || in.Task == f.Task) &&
		(f.Agent == "" || in.From == f.Agent || in.To == f.Agent)
}

// interactions are the feed's interactions matching f, oldest first.
func interactions(feed []storage.FeedItem, f FlowFilter) []Interaction {
	out := []Interaction{}
	for _, item := range feed {
		if in, ok := interactionOf(item); ok && f.matches(in) {
			out = append(out, in)
		}
	}
	return out
}

// Edge is all interactions from one party to another.
type Edge struct {
	From, To string
	Verbs    map[string]int
	Total    int
	Last     time.Time
}

// edges aggregates interactions into the graph of who passes work to whom, busiest first.
func edges(ins []Interaction) []Edge {
	idx := map[string]*Edge{}
	for _, in := range ins {
		to := in.To
		if to == "" {
			to = "·"
		}
		k := in.From + "\x00" + to
		e, ok := idx[k]
		if !ok {
			e = &Edge{From: in.From, To: to, Verbs: map[string]int{}}
			idx[k] = e
		}
		e.Verbs[in.Verb]++
		e.Total++
		if in.At.After(e.Last) {
			e.Last = in.At
		}
	}
	out := make([]Edge, 0, len(idx))
	for _, e := range idx {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

func (e Edge) verbsText() string {
	keys := make([]string, 0, len(e.Verbs))
	for k := range e.Verbs {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if e.Verbs[keys[i]] != e.Verbs[keys[j]] {
			return e.Verbs[keys[i]] > e.Verbs[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k
		if e.Verbs[k] > 1 {
			parts[i] = fmt.Sprintf("%s×%d", k, e.Verbs[k])
		}
	}
	return strings.Join(parts, " ")
}

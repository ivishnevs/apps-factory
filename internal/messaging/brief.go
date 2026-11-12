package messaging

import (
	"strings"
	"unicode/utf8"
)

// People topics (people-topics): everything that reaches people goes through one gate in Core, which puts it into a
// topic and checks its brief. The brief is written by whoever brings the item to people (the author, the lead that
// escalates or forwards it) or by Core from a template; agent-to-agent items carry none.

// Brief is what people read instead of the item's technical body.
type Brief struct {
	Title          string `json:"title,omitempty"`          // one line: what this is about
	Ask            string `json:"ask,omitempty"`            // what people decide, naming exactly what is approved
	After          string `json:"after,omitempty"`          // what happens after (per answer or overall)
	Recommendation string `json:"recommendation,omitempty"` // the bringer's advice and why
	Changes        string `json:"changes,omitempty"`        // since the version people saw
	Scope          string `json:"scope,omitempty"`          // what people do not decide
	Context        string `json:"context,omitempty"`        // background, markdown
}

// ItemNote is an item's own note when it joins an open topic.
type ItemNote struct {
	What    string `json:"what,omitempty"`
	Changes string `json:"changes,omitempty"`
}

// Brief field names, as the tools and refusals name them.
const (
	FieldTitle          = "title"
	FieldAsk            = "ask"
	FieldAfter          = "after"
	FieldRecommendation = "recommendation"
	FieldChanges        = "changes"
	FieldContext        = "context"
	FieldWhat           = "note.what"
	FieldNoteChanges    = "note.changes"
)

// Text limits of a brief (characters).
const (
	MaxTitle = 120
	MaxAsk   = 300
	MaxField = 4000
)

// Empty reports whether nothing is written.
func (b *Brief) Empty() bool {
	return b == nil || strings.TrimSpace(b.Title+b.Ask+b.After+b.Recommendation+b.Changes+b.Scope+b.Context) == ""
}

// Get returns a field by name ("" for a nil brief).
func (b *Brief) Get(field string) string {
	if b == nil {
		return ""
	}
	switch field {
	case FieldTitle:
		return b.Title
	case FieldAsk:
		return b.Ask
	case FieldAfter:
		return b.After
	case FieldRecommendation:
		return b.Recommendation
	case FieldChanges:
		return b.Changes
	case FieldContext:
		return b.Context
	}
	return ""
}

// Missing lists the fields of want that are empty.
func (b *Brief) Missing(want ...string) []string {
	var out []string
	for _, f := range want {
		if strings.TrimSpace(b.Get(f)) == "" {
			out = append(out, f)
		}
	}
	return out
}

// TooLong names the first field over its limit ("" when none is).
func (b *Brief) TooLong() string {
	if b == nil {
		return ""
	}
	if utf8.RuneCountInString(b.Title) > MaxTitle {
		return FieldTitle
	}
	if utf8.RuneCountInString(b.Ask) > MaxAsk {
		return FieldAsk
	}
	for _, f := range []struct{ name, v string }{{FieldAfter, b.After}, {FieldRecommendation, b.Recommendation}, {FieldChanges, b.Changes}, {"scope", b.Scope}, {FieldContext, b.Context}} {
		if utf8.RuneCountInString(f.v) > MaxField {
			return f.name
		}
	}
	return ""
}

// Trimmed is the brief with surrounding spaces removed; nil stays nil.
func (b *Brief) Trimmed() *Brief {
	if b == nil {
		return nil
	}
	return &Brief{Title: strings.TrimSpace(b.Title), Ask: strings.TrimSpace(b.Ask), After: strings.TrimSpace(b.After), Recommendation: strings.TrimSpace(b.Recommendation),
		Changes: strings.TrimSpace(b.Changes), Scope: strings.TrimSpace(b.Scope), Context: strings.TrimSpace(b.Context)}
}

// Empty reports whether the note says nothing.
func (n *ItemNote) Empty() bool { return n == nil || strings.TrimSpace(n.What+n.Changes) == "" }

// BriefHelp is the sentence every people-facing tool carries.
const BriefHelp = "When this reaches people (the owner), brief is required: what people read instead of your text. brief.title: one line, what this is about; brief.ask: what they decide, naming exactly what is approved (the whole mockup, one element, one state); brief.after: what happens after each answer; brief.recommendation: your advice and why; brief.changes: what changed since the version they saw (required when they saw one); brief.scope (optional): what they do not decide; brief.context (optional): background. When the item joins a topic people already have open, note {what, changes} is enough. Agents deciding among themselves need no brief, and Core refuses with brief_required naming the missing fields when one is needed. Write it in the company's language for people."

// People paths: how an item reaches people, which decides what its bringer must write (people-topics D3).
const (
	PathUpdate   = "update"   // a notification or report: no brief, joins the task's topic when one is open
	PathQuestion = "question" // an agent's question to people (owner_ask, exit_ask, message_ask to owner)
	PathForward  = "forward"  // a question an agent forwards to people
	PathTimeout  = "timeout"  // a question Core moved to people after nobody answered
	PathCall     = "call"     // approval of an exact call or command
	PathArtifact = "artifact" // approval of an artifact version (asked directly or escalated by the delegate)
	PathDecision = "decision" // a decision proposal
	PathReview   = "review"   // work submitted to a person reviewer
	PathCore     = "core"     // Core's own escalation or alert, briefed from a template
)

// PeopleRef says what a message to people is about; the gate uses it to find the topic and check the brief.
type PeopleRef struct {
	Path       string // one of the Path constants ("" derives it from the message)
	Kind       string // item kind: question, approval, escalation, decision, review, update
	Ref        string // id of the approval, escalation, decision or task the item stands for ("" = the message)
	Lineage    string // artifact id of a version line
	Version    string // artifact version
	Key        string // explicit topic key for Core's items (agent:<name>:<reason>)
	HasOptions bool   // the question offers options: a recommendation is required
	Composed   bool   // Core wrote it on the agent's behalf (a block that fell back to a question): not checked
	Skip       bool   // not for people yet (an approval its delegate decides): no topic until it is escalated
}

// PeopleArgs reads brief, note and topic from a tool call's arguments (people-topics D2).
func PeopleArgs(args map[string]any) (*Brief, *ItemNote, string) {
	str := func(m map[string]any, k string) string { v, _ := m[k].(string); return v }
	var brief *Brief
	if m, ok := args["brief"].(map[string]any); ok {
		brief = &Brief{Title: str(m, "title"), Ask: str(m, "ask"), After: str(m, "after"), Recommendation: str(m, "recommendation"),
			Changes: str(m, "changes"), Scope: str(m, "scope"), Context: str(m, "context")}
		if brief.Empty() {
			brief = nil
		}
	}
	var note *ItemNote
	if m, ok := args["note"].(map[string]any); ok {
		note = &ItemNote{What: str(m, "what"), Changes: str(m, "changes")}
		if note.Empty() {
			note = nil
		}
	}
	return brief, note, str(args, "topic")
}

// Package closedset holds the closed sets of values Core accepts for tool arguments (contracts-at-the-boundary §3):
// one source for both the Factory MCP tool schemas (their enums) and Core's validators, so an agent reads the valid
// values where it reads the tool, and every refusal of a value outside a set lists the set.
package closedset

import (
	"fmt"
	"slices"
	"strings"
)

// Set is a closed set of string values in the order they are shown.
type Set []string

// Has reports whether v is in the set.
func (s Set) Has(v string) bool { return slices.Contains(s, v) }

// Enum is the set as a JSON Schema enum.
func (s Set) Enum() []string { return slices.Clone(s) }

// Refusal is the error text for a value outside the set: what was refused, the value and every allowed value, e.g.
// `unsupported report kind "audit"; one of: dependency_installation, environment_change, …`.
func (s Set) Refusal(what, v string) string {
	shown := make([]string, len(s))
	for i, x := range s {
		if x == "" {
			x = `"" (all)`
		}
		shown[i] = x
	}
	return fmt.Sprintf("unsupported %s %q; one of: %s", what, v, strings.Join(shown, ", "))
}

var (
	// ReportKinds are the kinds an agent's factory_report_create accepts.
	ReportKinds = Set{"dependency_installation", "environment_change", "plan_deviation", "problem", "stage_completed"}
	// ReportListKinds filter factory_report_list: the agents' kinds, Core's runtime reports, or "" for all.
	ReportListKinds = Set{"", "dependency_installation", "environment_change", "plan_deviation", "problem", "stage_completed", "runtime"}
	// MessageTypes are the message types a sent message may have (answers go through reply).
	MessageTypes = Set{"question", "notification", "review_request", "review_result", "escalation"}
	// QuestionTypes are the types of a structured question.
	QuestionTypes = Set{"text", "confirmation", "single_choice", "multiple_choice"}
	// DecisionListStatuses filter factory_decision_list ("" lists every status).
	DecisionListStatuses = Set{"", "proposed", "under_discussion", "approved", "superseded", "rejected"}
	// DecisionRecordStatuses are what factory_decision_record records on a proposal.
	DecisionRecordStatuses = Set{"under_discussion", "approved", "rejected"}
	// ArtifactAudiences: internal for the team's own material, external (the default) for what leaves the company.
	ArtifactAudiences = Set{"internal", "external"}
	// TaskStatuses filter factory_task_list (BLOCKED includes READY tasks held by a question, an escalation or artifacts).
	TaskStatuses = Set{"BACKLOG", "READY", "IN_PROGRESS", "IN_REVIEW", "REWORK", "BLOCKED", "DONE", "CANCELLED"}
)

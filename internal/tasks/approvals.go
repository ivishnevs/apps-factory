package tasks

import (
	"fmt"
	"strings"
)

// Who approves (approval-delegation): per team, overridden per project, for each kind of thing — internal artifacts
// (concepts, references, textures), external artifacts (store, publication) and decisions.
const (
	ApproveOwner = "owner" // people only: a hard gate no agent can pass
	ApproveAgent = "agent" // a named agent approves; it escalates to people when unsure
	ApproveLead  = "lead"  // the team's lead approves what the owner's earlier answers and recorded decisions cover
)

// Approval kinds.
const (
	KindInternalArtifacts = "internal_artifacts"
	KindExternalArtifacts = "external_artifacts"
	KindDecisions         = "decisions"
)

// ApprovalMode is who approves one kind.
type ApprovalMode struct {
	Mode  string `yaml:"mode" json:"mode"`
	Agent string `yaml:"agent,omitempty" json:"agent,omitempty"`
}

// ApprovalModes are a team's or a project's modes; an empty mode inherits (project → team → default).
type ApprovalModes struct {
	InternalArtifacts ApprovalMode `yaml:"internal_artifacts,omitempty" json:"internal_artifacts,omitzero"`
	ExternalArtifacts ApprovalMode `yaml:"external_artifacts,omitempty" json:"external_artifacts,omitzero"`
	Decisions         ApprovalMode `yaml:"decisions,omitempty" json:"decisions,omitzero"`
}

// Of is the mode of kind.
func (m ApprovalModes) Of(kind string) ApprovalMode {
	switch kind {
	case KindInternalArtifacts:
		return m.InternalArtifacts
	case KindExternalArtifacts:
		return m.ExternalArtifacts
	case KindDecisions:
		return m.Decisions
	}
	return ApprovalMode{}
}

// DefaultApproval is the mode when neither the project nor the team sets one (owner 2026-11-06): the lead decides
// internal work from earlier answers, people approve what leaves the company.
func DefaultApproval(kind string) ApprovalMode {
	if kind == KindExternalArtifacts {
		return ApprovalMode{Mode: ApproveOwner}
	}
	return ApprovalMode{Mode: ApproveLead}
}

// Validate checks every set mode.
func (m ApprovalModes) Validate() error {
	for _, k := range []string{KindInternalArtifacts, KindExternalArtifacts, KindDecisions} {
		x := m.Of(k)
		switch x.Mode {
		case "", ApproveOwner, ApproveLead:
			if x.Agent != "" {
				return fmt.Errorf("approvals.%s: agent applies to mode agent only", k)
			}
		case ApproveAgent:
			if strings.TrimSpace(x.Agent) == "" {
				return fmt.Errorf("approvals.%s: mode agent needs the agent", k)
			}
		default:
			return fmt.Errorf("approvals.%s: mode must be owner, agent or lead", k)
		}
	}
	return nil
}

// PeopleOnlyCategory: decisions about money and destructive changes always go to people, whatever the mode.
func PeopleOnlyCategory(category string) bool {
	c := strings.ToLower(category)
	for _, w := range []string{"spend", "budget", "purchase", "payment", "money", "cost", "price", "destructive", "delete", "legal"} {
		if strings.Contains(c, w) {
			return true
		}
	}
	return false
}

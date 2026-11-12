package core

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"appfactory/internal/approvals"
	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// Approval delegation (approval-delegation): who approves a project's artifacts and decisions — people only, a named
// agent, or the team's lead deciding from the owner's earlier answers — set on the team, overridden on the project.
// Spending and destructive decisions always go to people.

// approvalMode is the mode of kind in project, the agent that decides under it ("" for people) and the setting in
// words for the record.
func (c *Core) approvalMode(ctx context.Context, company, project, kind string) (tasks.ApprovalMode, string, string) {
	p, err := c.Store.Project(ctx, company, project)
	if err != nil {
		return tasks.ApprovalMode{Mode: tasks.ApproveOwner}, "", "people (project unknown)"
	}
	cat := c.cat(company)
	mode, where := p.Approvals.Of(kind), "project "+p.ID
	if mode.Mode == "" && p.Team != "" {
		mode, where = cat.Teams[p.Team].Spec.Approvals.Of(kind), "team "+p.Team
	}
	if mode.Mode == "" {
		mode, where = tasks.DefaultApproval(kind), "default"
	}
	delegate := ""
	switch mode.Mode {
	case tasks.ApproveAgent:
		if a, ok := cat.Agents[mode.Agent]; ok && (p.Team == "" || a.Spec.Team == p.Team) {
			delegate = mode.Agent
		}
	case tasks.ApproveLead:
		if p.Team != "" {
			delegate = cat.Lead(p.Team)
		}
	}
	setting := fmt.Sprintf("%s: %s %s", where, strings.ReplaceAll(kind, "_", " "), mode.Mode)
	if mode.Agent != "" {
		setting += " " + mode.Agent
	}
	return mode, delegate, setting
}

// artifactDelegate fills an artifact approval request's delegate from the modes; the requester never decides its own.
func (c *Core) artifactDelegate(ctx context.Context, company, project string, in *requestDelegation) {
	kind := tasks.KindExternalArtifacts
	if in.audience == "internal" {
		kind = tasks.KindInternalArtifacts
	}
	_, delegate, setting := c.approvalMode(ctx, company, project, kind)
	if delegate == in.requester {
		delegate = ""
	}
	in.delegate, in.setting = delegate, setting
}

type requestDelegation struct{ audience, requester, delegate, setting string }

// approvalDecideTool is factory_approval_decide: the delegate of a pending approval approves, rejects or escalates it.
func (c *Core) approvalDecideTool(w http.ResponseWriter, r *http.Request, p factorytools.Call, run storage.Run) {
	company := c.companyFrom(r)
	id, _ := p.Arguments["approval_id"].(string)
	action, _ := p.Arguments["action"].(string)
	reason, _ := p.Arguments["reason"].(string)
	brief, note, topic := messaging.PeopleArgs(p.Arguments)
	var ids []string
	if list, ok := p.Arguments["approval_ids"].([]any); ok {
		for _, x := range list {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				ids = append(ids, s)
			}
		}
	}
	if len(ids) > 0 {
		// Several approvals escalated with one brief (people-topics D12).
		if action != "escalate" || id != "" {
			http.Error(w, "approval_ids is for escalate only, instead of approval_id", 400)
			return
		}
		for _, x := range ids {
			a, err := c.Store.Approval(r.Context(), company, x)
			if err != nil {
				reply(w, nil, err)
				return
			}
			if a.Delegate != run.Spec.Agent || a.Escalated {
				http.Error(w, "approval "+x+" is not yours to decide: it waits for people", 403)
				return
			}
		}
		if strings.TrimSpace(reason) == "" {
			http.Error(w, "reason required", 400)
			return
		}
		if ag, ok := c.cat(company).Agents[run.Spec.Agent]; ok && !ownerAllowed(ag) {
			http.Error(w, "you may not bring items to people (owner is not in your can_message): decide them or escalate them one by one, which hands each to your manager", 403)
			return
		}
		key, _ := p.Arguments["request_id"].(string)
		v, err := c.Store.EscalateApprovals(r.Context(), company, ids, run.Spec.Agent, reason, key, storage.EscalatePeople{Brief: brief, Note: note, Topic: topic, Strict: true}, nil)
		reply(w, v, err)
		return
	}
	var cites []string
	if list, ok := p.Arguments["cites"].([]any); ok {
		for _, x := range list {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				cites = append(cites, s)
			}
		}
	}
	a, err := c.Store.Approval(r.Context(), company, id)
	if err != nil {
		reply(w, nil, err)
		return
	}
	if a.Delegate != run.Spec.Agent || a.Escalated {
		http.Error(w, "this approval is not yours to decide: it waits for people", 403)
		return
	}
	kind := tasks.KindExternalArtifacts
	if a.Audience == "internal" {
		kind = tasks.KindInternalArtifacts
	}
	mode, delegate, _ := c.approvalMode(r.Context(), company, a.ProjectID, kind)
	if delegate != run.Spec.Agent {
		http.Error(w, "the approval setting changed: it waits for people now", 403)
		return
	}
	if strings.TrimSpace(reason) == "" {
		http.Error(w, "reason required", 400)
		return
	}
	if action == "escalate" {
		// Strict delegation (people-topics D17): an agent that may not bring items to people hands it one level up.
		if ag, ok := c.cat(company).Agents[run.Spec.Agent]; ok && !ownerAllowed(ag) {
			m := c.escalationTargetIn(company, run.Spec.Agent)
			if m == "" || m == "owner" {
				http.Error(w, "you may not bring items to people (owner is not in your can_message): decide it", 403)
				return
			}
			v, err := c.Store.HandUpApproval(r.Context(), company, id, run.Spec.Agent, m, reason)
			reply(w, v, err)
			return
		}
	}
	switch action {
	case "escalate":
		v, err := c.Store.EscalateApproval(r.Context(), company, id, run.Spec.Agent, reason, storage.EscalatePeople{Brief: brief, Note: note, Topic: topic, Strict: true})
		reply(w, v, err)
		return
	case "approve", "reject":
	default:
		http.Error(w, "action must be approve, reject or escalate", 400)
		return
	}
	if mode.Mode == tasks.ApproveLead && action == "approve" && len(cites) == 0 {
		http.Error(w, "as the lead you approve only what the owner's earlier answers or recorded decisions cover: name them in cites, or escalate", 400)
		return
	}
	key, _ := p.Arguments["request_id"].(string)
	// /agent/call already holds approvalMu (a second Lock here deadlocked every agent call: security-review-2026-11 F3).
	v, err := c.Store.DecideApproval(r.Context(), company, id, approvals.Decision{Version: a.Version, Action: action, Reason: reason, RequestID: run.Spec.Agent + ":" + key, DecidedBy: run.Spec.Agent, Cites: cites})
	reply(w, v, err)
}

// decisionDelegation says whether agent may record a decision of category in project without a policy rule: the
// project's decision mode names it (agent) or makes the team's lead decide (lead, with cites). owner refuses even a
// policy-delegated category.
func (c *Core) decisionDelegation(ctx context.Context, company, project, agent, category string, cites int) (allowed bool, setting string, err error) {
	if tasks.PeopleOnlyCategory(category) {
		return false, "", fmt.Errorf("decisions about money and destructive changes (%s) always go to people: propose it", category)
	}
	mode, delegate, setting := c.approvalMode(ctx, company, project, tasks.KindDecisions)
	switch {
	case mode.Mode == tasks.ApproveOwner:
		return false, setting, fmt.Errorf("decisions of this project are approved by people only (%s): propose it", setting)
	case delegate != agent:
		return false, setting, nil
	case mode.Mode == tasks.ApproveLead && cites == 0:
		return false, setting, fmt.Errorf("as the lead you decide only what the owner's earlier answers or recorded decisions cover: name them in cites, or propose it")
	}
	return true, setting, nil
}

// delegationKey carries the approval setting a decision is recorded under (factory_decision_decide by delegation).
type delegationKey struct{}

// ownerAllowed: both the captured and the current agent may bring items to people (owner in can_message; people-topics
// D17).
func ownerAllowed(agents ...config.Agent) bool {
	for _, a := range agents {
		if !slices.Contains(a.Spec.Communication.CanMessage, "owner") {
			return false
		}
	}
	return true
}

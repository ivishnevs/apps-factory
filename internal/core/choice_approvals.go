package core

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"appfactory/internal/approvals"
	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
)

// Choice approvals under delegation (artifact-choice-approvals): a delegate decides a choice by choosing (only with
// cites of what people said that covers it, each checked here), asking for changes or rejecting all (its own
// judgement, with a reason), or escalating, optionally dropping options not good enough. It never approves a choice
// without choosing.

// choiceDecideTool is factory_approval_decide on a choice approval delegated to the run's agent.
func (c *Core) choiceDecideTool(w http.ResponseWriter, r *http.Request, p factorytools.Call, run storage.Run, a approvals.Approval, action, reason string, cites []string) {
	company := c.companyFrom(r)
	agent := run.Spec.Agent
	if _, ok := p.Arguments["drop"]; ok && action != "escalate" {
		http.Error(w, "drop leaves options out when you escalate: use it with action escalate", 400)
		return
	}
	if _, ok := p.Arguments["choice"]; ok && action != approvals.ActionChoose {
		http.Error(w, "choice is for action choose", 400)
		return
	}
	key, _ := p.Arguments["request_id"].(string)
	decide := func(in approvals.Decision) {
		in.Version, in.Reason, in.RequestID, in.DecidedBy = a.Version, reason, agent+":"+key, agent
		v, err := c.Store.DecideApproval(r.Context(), company, a.ID, in)
		reply(w, v, err)
	}
	switch action {
	case "escalate":
		drops, err := choiceDrops(a, p.Arguments["drop"])
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if ag, ok := c.cat(company).Agents[agent]; ok && !ownerAllowed(ag) {
			// Strict delegation (people-topics D17): an agent that may not bring items to people hands it one level up.
			m := c.escalationTargetIn(company, agent)
			if m == "" || m == "owner" {
				http.Error(w, "you may not bring items to people (owner is not in your can_message): choose it with cites, ask for changes or reject it", 403)
				return
			}
			v, err := c.Store.HandUpApproval(r.Context(), company, a.ID, agent, m, reason, drops...)
			reply(w, v, err)
			return
		}
		brief, note, topic := messaging.PeopleArgs(p.Arguments)
		v, err := c.Store.EscalateApproval(r.Context(), company, a.ID, agent, reason, storage.EscalatePeople{Brief: brief, Note: note, Topic: topic, Strict: true, Drops: drops})
		reply(w, v, err)
	case approvals.ActionChoose:
		ref, ok := p.Arguments["choice"]
		if !ok {
			http.Error(w, "choice: the option you choose (its number, label or artifact_id@version)", 400)
			return
		}
		n, err := a.ResolveOption(ref)
		if err != nil {
			http.Error(w, "choice: "+err.Error(), 400)
			return
		}
		if len(cites) == 0 {
			http.Error(w, "you choose only what people said covers: cite their earlier answers (message ids), recorded decisions (decision ids) or the company's or project's rules (\"project rules: <quote>\") in cites; otherwise escalate, ask for changes or reject", 400)
			return
		}
		if err = c.checkCites(r.Context(), company, a.ProjectID, cites); err != nil {
			http.Error(w, err.Error()+"; without such a basis escalate to people", 400)
			return
		}
		decide(approvals.Decision{Action: approvals.ActionChoose, Option: n, Cites: cites})
	case approvals.ActionChanges:
		var on []int
		if list, ok := p.Arguments["options"].([]any); ok {
			for _, x := range list {
				n, err := a.ResolveOption(x)
				if err != nil {
					http.Error(w, "options: "+err.Error(), 400)
					return
				}
				on = append(on, n)
			}
		}
		decide(approvals.Decision{Action: approvals.ActionChanges, Options: on})
	case approvals.ActionReject:
		decide(approvals.Decision{Action: approvals.ActionReject})
	case "approve":
		http.Error(w, fmt.Sprintf("this approval is a choice among %d options and is never approved without choosing: choose one (choice, citing what people said that covers it), ask for changes or reject all with a reason, or escalate it to people", len(a.Options)), 400)
	default:
		http.Error(w, "action must be choose, changes, reject or escalate for a choice approval", 400)
	}
}

// choiceDrops reads escalate's drop: [{option, reason}].
func choiceDrops(a approvals.Approval, arg any) ([]approvals.Drop, error) {
	if arg == nil {
		return nil, nil
	}
	list, ok := arg.([]any)
	if !ok {
		return nil, fmt.Errorf("drop: a list of {option, reason}")
	}
	var out []approvals.Drop
	for _, x := range list {
		m, _ := x.(map[string]any)
		n, err := a.ResolveOption(m["option"])
		if err != nil {
			return nil, fmt.Errorf("drop: %w", err)
		}
		reason, _ := m["reason"].(string)
		out = append(out, approvals.Drop{Option: n, Reason: reason})
	}
	return out, nil
}

// rulesCite is a cite of the company's or project's rules: "project rules: <quote>".
var rulesCite = regexp.MustCompile(`(?is)^\s*(company|project)(?:'s)?\s+rules?\b\s*[:\-–—]?\s*(.*)$`)

// checkCites checks that each cite of a delegate's choice names what people said (artifact-choice-approvals): an
// answer people wrote (the id of their message, or of a question people answered), a recorded decision (the id of
// an approved decision), or a quote of the company's or project's rules, which only people write.
func (c *Core) checkCites(ctx context.Context, company, project string, cites []string) error {
	for _, cite := range cites {
		if m := rulesCite.FindStringSubmatch(cite); m != nil {
			quote := strings.Trim(strings.TrimSpace(m[2]), "\"'“”«»„`")
			if quote == "" {
				return fmt.Errorf("cite %q: quote the rule you rely on (\"%s rules: <quote>\")", cite, strings.ToLower(m[1]))
			}
			var rules string
			if strings.EqualFold(m[1], "company") {
				rules, _ = c.Store.CompanyRules(ctx, company)
			} else if p, err := c.Store.Project(ctx, company, project); err == nil {
				rules = p.Rules
			}
			if !strings.Contains(foldSpace(rules), foldSpace(quote)) {
				return fmt.Errorf("cite %q: the %s rules do not say that (quote them word for word)", cite, strings.ToLower(m[1]))
			}
			continue
		}
		if !c.peopleCited(ctx, company, cite) {
			return fmt.Errorf("cite %q names no answer people wrote (a message id), no recorded decision (a decision id) and no quote of the company's or project's rules", cite)
		}
	}
	return nil
}

// peopleCited tells whether cite names a message people wrote, a question people answered or an approved decision.
func (c *Core) peopleCited(ctx context.Context, company, cite string) bool {
	for _, id := range slices.Compact(citedID.FindAllString(strings.ToLower(cite), 8)) {
		if m, err := c.Store.Message(ctx, company, id); err == nil {
			if m.Sender == "owner" {
				return true
			}
			if ans, err := c.Store.AnswerFor(ctx, company, id); err == nil && ans.Sender == "owner" {
				return true
			}
		}
		if d, err := c.Store.Decision(ctx, company, id); err == nil && d.Status == "approved" {
			return true
		}
	}
	return false
}

// foldSpace is s lower-cased with its whitespace collapsed, for comparing quotes.
func foldSpace(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

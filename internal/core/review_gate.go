package core

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Review coverage (review-coverage): every task outside the backlog names its reviewer — an agent, the owner or a
// member — and Core refuses to release a task without one. The review policy only suggests the reviewer.

// ErrReviewerRequired is the people-facing refusal text; the web shows it as is.
const ErrReviewerRequired = "Reviewer is required"

// suggestReviewer is the default reviewer of assignee's work (approval-gates-merge, config.Catalog.DefaultReviewer):
// its review policy's reviewer, else the team's default reviewer, else its manager, else the team's lead, else the
// owner; never the assignee.
func (c *Core) suggestReviewer(company, assignee string) string {
	return c.cat(company).DefaultReviewer(assignee)
}

// mayChangeReviewer tells whether actor may change t's reviewer (approval-gates-merge): people, the task's creator,
// or a manager up the chain of the agent whose work it reviews (the implementer while it is in review).
func (c *Core) mayChangeReviewer(company, actor string, t tasks.Task) bool {
	if actor == "" || actor == config.Owner || !c.isAgent(company, actor) || actor == t.CreatedBy {
		return true
	}
	worker := t.Assignee
	if t.Status == "IN_REVIEW" && t.Implementer != "" {
		worker = t.Implementer
	}
	return slices.Contains(config.Chain(c.cat(company).Agents, worker), actor)
}

// checkReviewBy validates a reviewer value: another agent of the catalog, "owner", or "member:USER" of the
// company. "" is valid here (the gate decides whether one is required).
func (c *Core) checkReviewBy(ctx context.Context, company, assignee, by string) error {
	switch {
	case by == "", by == "owner":
		return nil
	case strings.HasPrefix(by, "member:"):
		user := strings.TrimPrefix(by, "member:")
		members, err := c.Store.Members(ctx, company)
		if err != nil {
			return err
		}
		for _, m := range members {
			if m.User == user {
				if by == assignee {
					return fmt.Errorf("review_by %s is the assignee; a task is reviewed by someone else", by)
				}
				return nil
			}
		}
		return fmt.Errorf("review_by %s is not a member of the company", by)
	}
	if _, ok := c.cat(company).Agents[by]; !ok {
		return fmt.Errorf("review_by %s is not an agent, owner or member:USER", by)
	}
	if by == assignee {
		return fmt.Errorf("review_by %s is the assignee; a task is reviewed by someone else", by)
	}
	return nil
}

// reviewerRequired writes the refusal for a release without a reviewer: agents get the field and the policy's
// suggestion, people the plain "Reviewer is required" (with the suggestion after it).
func (c *Core) reviewerRequired(w http.ResponseWriter, company, assignee string) {
	s := c.suggestReviewer(company, assignee)
	http.Error(w, fmt.Sprintf("%s: set review_by (an agent, owner or member:USER) before the task leaves the backlog; the review policy suggests review_by %q", ErrReviewerRequired, s), 400)
}

// reviewGateCreate checks a task create: the reviewer is valid, and set when the task is created ready.
func (c *Core) reviewGateCreate(w http.ResponseWriter, r *http.Request, p *tasks.Create) bool {
	company := c.companyFrom(r)
	p.ReviewBy = strings.TrimSpace(p.ReviewBy)
	if err := c.checkReviewBy(r.Context(), company, p.Assignee, p.ReviewBy); err != nil {
		http.Error(w, err.Error(), 400)
		return false
	}
	if p.Ready && p.ReviewBy == "" {
		c.reviewerRequired(w, company, p.Assignee)
		return false
	}
	return true
}

// reviewGateUpdate checks a task update: a new reviewer is valid, and a task outside the backlog keeps one.
func (c *Core) reviewGateUpdate(w http.ResponseWriter, r *http.Request, id string, p *tasks.Update) bool {
	if p.ReviewBy == nil && p.Assignee == nil {
		return true
	}
	company := c.companyFrom(r)
	t, err := c.Store.Task(r.Context(), company, id)
	if err != nil {
		reply(w, nil, err)
		return false
	}
	by, assignee := t.ReviewBy, t.Assignee
	if p.ReviewBy != nil {
		by = strings.TrimSpace(*p.ReviewBy)
	}
	if p.Assignee != nil {
		assignee = *p.Assignee
	}
	if p.ReviewBy != nil && by != t.ReviewBy && !c.mayChangeReviewer(company, p.Actor, t) {
		// The task's reviewer is binding: changing it is an authority action, like reassigning.
		http.Error(w, fmt.Sprintf("changing the reviewer of %s is for people, the task's creator or a manager of its assignee: its reviewer stays %s", t.ID, firstNonEmpty(t.ReviewBy, "unset")), 403)
		return false
	}
	if p.ReviewBy != nil {
		if err := c.checkReviewBy(r.Context(), company, assignee, by); err != nil {
			http.Error(w, err.Error(), 400)
			return false
		}
		if by == "" && t.Status != "BACKLOG" {
			c.reviewerRequired(w, company, assignee)
			return false
		}
	} else if by != "" && by == assignee {
		http.Error(w, fmt.Sprintf("the new assignee %s is the task's reviewer; change review_by too", assignee), 400)
		return false
	}
	return true
}

// reviewGateReady checks a release (tasks ready): the task names its reviewer. Conversations are not reviewed.
func (c *Core) reviewGateReady(w http.ResponseWriter, r *http.Request, t tasks.Task) bool {
	if t.ReviewBy != "" || t.Kind == tasks.KindConversation {
		return true
	}
	c.reviewerRequired(w, c.companyFrom(r), t.Assignee)
	return false
}

// reviewFill is the one-time migration of review-coverage 3.4: every open task outside the backlog without a
// reviewer gets the policy's suggestion (dry_run lists them only). Owner route; conversations are skipped.
func (c *Core) reviewFill(w http.ResponseWriter, r *http.Request) {
	if _, agent := r.Context().Value(runKey{}).(protocol.RunSpec); agent {
		http.Error(w, "owner only", 403)
		return
	}
	var p struct {
		DryRun bool `json:"dry_run"`
	}
	if !decode(w, r, &p) {
		return
	}
	company := c.companyFrom(r)
	all, err := c.Store.Tasks(r.Context(), company, "", "")
	if err != nil {
		reply(w, nil, err)
		return
	}
	type filled struct {
		Task     string `json:"task_id"`
		Title    string `json:"title"`
		Assignee string `json:"assignee"`
		ReviewBy string `json:"review_by"`
		Error    string `json:"error,omitempty"`
	}
	out := []filled{}
	for _, t := range all {
		if t.ReviewBy != "" || t.Kind == tasks.KindConversation || t.Status == "BACKLOG" || t.Status == "DONE" || t.Status == "CANCELLED" {
			continue
		}
		f := filled{Task: t.ID, Title: t.Title, Assignee: t.Assignee, ReviewBy: c.suggestReviewer(company, t.Assignee)}
		if f.ReviewBy == t.Assignee {
			f.ReviewBy = "owner"
		}
		if !p.DryRun {
			if err := c.Store.SetReviewBy(r.Context(), company, t.ID, f.ReviewBy); err != nil {
				f.Error = err.Error()
			}
		}
		out = append(out, f)
	}
	write(w, map[string]any{"dry_run": p.DryRun, "tasks": out})
}

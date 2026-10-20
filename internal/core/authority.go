package core

import (
	"context"
	"net/http"
	"slices"

	"appfactory/internal/config"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// Authority actions (hierarchy D2) for the owner (API/CLI) and for agents with authority over the task (Factory
// MCP forwards with actor): pause, resume, reassign within the actor's subtree, reprioritize; cancel goes through
// /tasks/{id}/actions with the same authority check. Agents are refused with 403 outside their authority.

// rank is the actor's authority rank: 0 the owner, otherwise its depth in the tree (1 reports to the owner).
func (c *Core) rank(actor string) int {
	if actor == "" || actor == config.Owner {
		return 0
	}
	return len(config.Chain(c.Agents, actor)) + 1
}

// authorityOver loads the task and checks actor's authority over it (owner always); it answers the refusal.
func (c *Core) authorityOver(w http.ResponseWriter, r *http.Request, id, actor, operation string) (tasks.Task, bool) {
	if !c.taskPermit(w, r, actor, operation, false, false) {
		return tasks.Task{}, false
	}
	t, err := c.Store.Task(r.Context(), c.companyFrom(r), id)
	if err != nil {
		reply(w, nil, err)
		return t, false
	}
	if !c.hasAuthority(r.Context(), c.companyFrom(r), actor, t) {
		http.Error(w, "authority over the task required: its assignee's chain, its delegator or the owner", 403)
		return t, false
	}
	return t, true
}

func (c *Core) authorityRoutes(m *http.ServeMux) {
	actions := map[string]func(*storage.Store, context.Context, string, string, tasks.Authority) (tasks.Task, error){
		"pause": (*storage.Store).PauseTask, "resume": (*storage.Store).ResumeTask,
		"reassign": (*storage.Store).ReassignTask, "reprioritize": (*storage.Store).ReprioritizeTask,
	}
	for name, apply := range actions {
		m.HandleFunc("POST /tasks/{id}/"+name, func(w http.ResponseWriter, r *http.Request) {
			var p tasks.Authority
			if !decode(w, r, &p) {
				return
			}
			if _, ok := c.authorityOver(w, r, r.PathValue("id"), p.Actor, "factory.task."+name); !ok {
				return
			}
			if name == "reassign" {
				if !c.agentExists(w, p.Assignee) {
					return
				}
				// An agent reassigns within its subtree (or to itself); the owner to anyone.
				if p.Actor != "" && p.Actor != config.Owner && p.Assignee != p.Actor && !slices.Contains(config.Subtree(c.Agents, p.Actor), p.Assignee) {
					http.Error(w, "the new assignee must be in your subtree", 403)
					return
				}
			}
			p.Rank = c.rank(p.Actor)
			v, err := apply(c.Store, r.Context(), c.companyFrom(r), r.PathValue("id"), p)
			reply(w, v, err)
		})
	}
	// Agent scope: a manager pauses an agent of its subtree (no new runs of it); resume by the pauser or a
	// higher authority.
	for _, op := range []string{"pause", "resume"} {
		m.HandleFunc("POST /agents/{name}/"+op, func(w http.ResponseWriter, r *http.Request) {
			name := r.PathValue("name")
			var p struct {
				Actor  string `json:"actor,omitempty"`
				Reason string `json:"reason,omitempty"`
			}
			if !decode(w, r, &p) {
				return
			}
			if !c.agentExists(w, name) || !c.taskPermit(w, r, p.Actor, "factory.task."+op, false, false) {
				return
			}
			actor := p.Actor
			if actor == "" {
				actor = config.Owner
			}
			if actor != config.Owner && !slices.Contains(config.Subtree(c.Agents, actor), name) {
				http.Error(w, "agents pause only agents of their subtree", 403)
				return
			}
			ctx, company := r.Context(), c.companyFrom(r)
			if op == "pause" {
				err := c.Store.SetPause(ctx, company, storage.Pause{Scope: "agent", Target: name, Reason: p.Reason, Actor: actor})
				reply(w, map[string]string{"paused": "agent:" + name}, err)
				return
			}
			pauses, err := c.Store.Pauses(ctx, company)
			if err != nil {
				reply(w, nil, err)
				return
			}
			for _, existing := range pauses {
				if existing.Scope == "agent" && existing.Target == name && existing.Actor != actor && c.rank(actor) >= c.rank(existing.Actor) {
					http.Error(w, "only the pauser ("+existing.Actor+") or a higher authority resumes the agent", 403)
					return
				}
			}
			reply(w, map[string]string{"resumed": "agent:" + name}, c.Store.ClearPause(ctx, company, "agent", name, actor))
		})
	}
}

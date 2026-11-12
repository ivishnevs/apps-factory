package core

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"time"

	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// People as assignees (people-assignees): a task's assignee is an agent of the catalog or a person of the company
// (member:USER). Core dispatches no run for a person's task; the person and the owner move it through the same
// statuses (start, submit with the pull request, done after the approval and the merge). Agents may read, comment on
// and depend on it, and give a task to a person only on that person's word; they may not change its status,
// assignee or description, nor cancel it.

// peopleSettingsRoutes serve the company's people.remind_after.
func (c *Core) peopleSettingsRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /settings/company/people", func(w http.ResponseWriter, r *http.Request) {
		d, err := c.Store.PeopleRemindAfter(r.Context(), c.companyFrom(r))
		reply(w, map[string]any{"remind_after_minutes": int64(d / time.Minute)}, err)
	})
	m.HandleFunc("PUT /settings/company/people", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			RemindAfterMinutes int64 `json:"remind_after_minutes"`
		}
		if !decode(w, r, &p) {
			return
		}
		err := c.Store.SetPeopleRemindAfter(r.Context(), c.companyFrom(r), time.Duration(p.RemindAfterMinutes)*time.Minute)
		reply(w, map[string]any{"remind_after_minutes": p.RemindAfterMinutes}, err)
	})
}

// member is the company's member user, if any.
func (c *Core) member(ctx context.Context, company, user string) (storage.Member, bool) {
	members, err := c.Store.Members(ctx, company)
	if err != nil {
		return storage.Member{}, false
	}
	for _, m := range members {
		if m.User == user {
			return m, true
		}
	}
	return storage.Member{}, false
}

// assigneeExists answers 400 unless assignee is a configured agent or a person of the company (member:USER).
func (c *Core) assigneeExists(w http.ResponseWriter, r *http.Request, assignee string) bool {
	if user, ok := tasks.PersonUser(assignee); ok {
		if _, ok := c.member(r.Context(), c.companyFrom(r), user); ok {
			return true
		}
		http.Error(w, "valid assignee required: "+assignee+" is not a person of the company", 400)
		return false
	}
	return c.agentExists(w, c.companyFrom(r), assignee)
}

// isAgent tells whether actor is an agent of the company's catalog (people act without an actor).
func (c *Core) isAgent(company, actor string) bool {
	_, ok := c.cat(company).Agents[actor]
	return actor != "" && ok
}

var citedID = regexp.MustCompile(`[0-9a-f]{32}`)

// personCited tells whether reason cites a message of the person behind assignee: a people message (sender owner)
// written by that user, or by the owner's CLI (no person) when the user is an owner of the company.
func (c *Core) personCited(ctx context.Context, company, assignee, reason string) bool {
	user, _ := tasks.PersonUser(assignee)
	member, ok := c.member(ctx, company, user)
	if !ok {
		return false
	}
	for _, id := range slices.Compact(citedID.FindAllString(reason, 8)) {
		m, err := c.Store.Message(ctx, company, id)
		if err != nil || m.Sender != "owner" {
			continue
		}
		if m.PersonID == user || m.PersonID == "" && member.Role == storage.RoleOwner {
			return true
		}
	}
	return false
}

// agentAssignsPerson refuses an agent's assignment of a task to a person without that person's word (403); it
// reports whether the request may go on.
func (c *Core) agentAssignsPerson(w http.ResponseWriter, r *http.Request, actor, assignee, reason string) bool {
	company := c.companyFrom(r)
	if !tasks.IsPerson(assignee) || !c.isAgent(company, actor) || c.personCited(r.Context(), company, assignee, reason) {
		return true
	}
	http.Error(w, "a task goes to a person only on that person's word: set assign_reason citing the id of their message (\"I'll do it myself\"), or leave the task to an agent", 403)
	return false
}

// agentHandsOff refuses an agent's change of a person's task (403 with the hint to comment); it reports whether
// it answered the request.
func (c *Core) agentHandsOff(w http.ResponseWriter, r *http.Request, actor, id, what string) bool {
	company := c.companyFrom(r)
	if !c.isAgent(company, actor) {
		return false
	}
	t, err := c.Store.Task(r.Context(), company, id)
	if err != nil || !tasks.IsPerson(t.Assignee) {
		return false
	}
	http.Error(w, "task "+t.ID+" is assigned to a person ("+t.Assignee+"): agents do not "+what+" it; comment on it instead (factory_message_send to the owner with its task_id), or depend on it", 403)
	return true
}

// personAction serves start, submit and done of a person's task: only people (no agent actor), and among them
// the assigned person and the company's owners (the owner token and the operator act as the owner).
func (c *Core) personAction(w http.ResponseWriter, r *http.Request, p tasks.Action) {
	if p.Actor != "" {
		http.Error(w, "start, submit and done move a person's task; agents end their own runs with factory_exit_*", 403)
		return
	}
	company := c.companyFrom(r)
	t, err := c.Store.Task(r.Context(), company, r.PathValue("id"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	if pr := principalOf(r); pr.Kind == "session" && pr.Role != storage.RoleOwner && t.Assignee != tasks.PersonPrefix+pr.User {
		http.Error(w, "only "+t.Assignee+" or an owner moves this task", 403)
		return
	}
	v, err := c.Store.PersonAction(r.Context(), company, t.ID, p)
	reply(w, v, err)
}

// assigneeFilter resolves GET /tasks?assignee=: "me" is the signed-in person (member:USER); for the owner's CLI
// token and the operator, who carry no person, it is the company's owners. Anything else is matched as given.
func (c *Core) assigneeFilter(r *http.Request, assignee string) []string {
	if assignee != "me" {
		return []string{assignee}
	}
	if pr := principalOf(r); pr.Kind == "session" {
		return []string{tasks.PersonPrefix + pr.User}
	}
	members, _ := c.Store.Members(r.Context(), c.companyFrom(r))
	out := []string{}
	for _, m := range members {
		if m.Role == storage.RoleOwner {
			out = append(out, tasks.PersonPrefix+m.User)
		}
	}
	return out
}

// remindPeople sends each person whose task has not changed for the company's people.remind_after while other
// tasks wait on it one reminder per episode (people-assignees 3.1).
func (c *Core) remindPeople(ctx context.Context, company string, now time.Time, snaps []tasks.Snapshot) error {
	after, err := c.Store.PeopleRemindAfter(ctx, company)
	if err != nil {
		return err
	}
	waiting := map[string][]tasks.Task{}
	open := map[string]tasks.Task{}
	for _, s := range snaps {
		open[s.Task.ID] = s.Task
	}
	for _, s := range snaps {
		if s.Task.Status == "BACKLOG" {
			continue
		}
		for _, d := range s.Dependencies {
			if d.Status != "DONE" && tasks.IsPerson(open[d.ID].Assignee) {
				waiting[d.ID] = append(waiting[d.ID], s.Task)
			}
		}
	}
	for id, ws := range waiting {
		t := open[id]
		unchanged := now.Sub(time.Unix(t.UpdatedAt, 0))
		if unchanged < after {
			continue
		}
		if _, err = c.Store.RemindPerson(ctx, company, t, ws, unchanged); err != nil {
			return err
		}
	}
	return nil
}

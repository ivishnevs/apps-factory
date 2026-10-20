package core

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

func (c *Core) taskRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /projects", func(w http.ResponseWriter, r *http.Request) {
		var p tasks.ProjectCreate
		if !decode(w, r, &p) {
			return
		}
		v, err := c.Store.CreateProject(r.Context(), c.companyFrom(r), p)
		reply(w, v, err)
	})
	m.HandleFunc("GET /projects", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Projects(r.Context(), c.companyFrom(r))
		reply(w, v, err)
	})
	m.HandleFunc("GET /projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Project(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("PATCH /projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		var p tasks.ProjectUpdate
		if !decode(w, r, &p) {
			return
		}
		v, err := c.Store.UpdateProject(r.Context(), c.companyFrom(r), r.PathValue("id"), p)
		reply(w, v, err)
	})
	m.HandleFunc("GET /settings/company", func(w http.ResponseWriter, r *http.Request) {
		rules, err := c.Store.CompanyRules(r.Context(), c.companyFrom(r))
		reply(w, map[string]string{"scope": "company", "rules": rules}, err)
	})
	m.HandleFunc("PUT /settings/company", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Rules string `json:"rules"`
		}
		if !decode(w, r, &p) {
			return
		}
		err := c.Store.SetCompanyRules(r.Context(), c.companyFrom(r), p.Rules)
		reply(w, map[string]string{"scope": "company", "rules": p.Rules}, err)
	})
	// The company's time zone sets the day of the daily retry budget (reliable-recovery D2).
	m.HandleFunc("GET /settings/company/time_zone", func(w http.ResponseWriter, r *http.Request) {
		zone, err := c.Store.CompanyTimeZone(r.Context(), c.companyFrom(r))
		reply(w, map[string]string{"time_zone": zone}, err)
	})
	m.HandleFunc("PUT /settings/company/time_zone", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			TimeZone string `json:"time_zone"`
		}
		if !decode(w, r, &p) {
			return
		}
		err := c.Store.SetCompanyTimeZone(r.Context(), c.companyFrom(r), p.TimeZone)
		reply(w, map[string]string{"time_zone": p.TimeZone}, err)
	})
	m.HandleFunc("GET /projects/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.EntityEvents(r.Context(), c.companyFrom(r), r.PathValue("id"), "")
		reply(w, v, err)
	})
	m.HandleFunc("POST /tasks", func(w http.ResponseWriter, r *http.Request) {
		var p tasks.Create
		if !decode(w, r, &p) {
			return
		}
		if !c.taskPermit(w, r, p.Actor, "factory.task.create", true, false) {
			return
		}
		if p.Assignee != "" && !c.agentExists(w, p.Assignee) {
			return
		}
		if p.Actor != "" && p.Assignee != "" && p.Assignee != p.Actor && !c.taskPermit(w, r, p.Actor, "factory.task.assign", false, true) {
			return
		}
		if p.Ready && !c.taskPermit(w, r, p.Actor, "factory.task.ready", false, false) {
			return
		}
		p.CreatedBy = "owner"
		if p.Actor != "" {
			p.CreatedBy = p.Actor
		}
		// Only Factory MCP calls carry an origin; agent-created tasks are deduplicated by it.
		if run, ok := r.Context().Value(runKey{}).(protocol.RunSpec); ok {
			p.OriginRun, p.OriginTask = run.ID, run.TaskID
			p.Roles = map[string]string{}
			for name, a := range c.Agents {
				p.Roles[name] = a.Spec.Role
			}
		}
		v, err := c.Store.CreateTask(r.Context(), c.companyFrom(r), p)
		reply(w, v, err)
	})
	m.HandleFunc("GET /tasks", func(w http.ResponseWriter, r *http.Request) {
		if !c.taskPermit(w, r, r.URL.Query().Get("actor"), "factory.task.list", false, false) {
			return
		}
		ctx, company, project := r.Context(), c.companyFrom(r), r.URL.Query().Get("project")
		list, err := c.Store.Tasks(ctx, company, project, r.URL.Query().Get("status"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		snaps, err := c.Store.TaskSnapshots(ctx, company, project)
		if err != nil {
			reply(w, nil, err)
			return
		}
		verdicts := map[string]tasks.Verdict{}
		for _, snap := range snaps {
			verdicts[snap.Task.ID] = c.evaluate(snap)
		}
		out := make([]taskView, 0, len(list))
		for _, t := range list {
			v, ok := verdicts[t.ID]
			if !ok { // terminal
				v = tasks.Evaluate(tasks.Snapshot{Task: t})
			}
			out = append(out, taskView{Task: t, Reason: v.Reason, Clearer: v.Clearer})
		}
		write(w, out)
	})
	m.HandleFunc("GET /tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !c.taskPermit(w, r, r.URL.Query().Get("actor"), "factory.task.get", false, false) {
			return
		}
		snap, err := c.Store.TaskSnapshot(r.Context(), c.companyFrom(r), r.PathValue("id"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		v := c.evaluate(snap)
		write(w, taskView{Task: snap.Task, Reason: v.Reason, Clearer: v.Clearer})
	})
	// Diagnostics only: why a task does not run now and who clears it (trigger-log-scheduling D3).
	m.HandleFunc("GET /tasks/{id}/why", func(w http.ResponseWriter, r *http.Request) {
		if !c.taskPermit(w, r, r.URL.Query().Get("actor"), "factory.task.get", false, false) {
			return
		}
		snap, err := c.Store.TaskSnapshot(r.Context(), c.companyFrom(r), r.PathValue("id"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		write(w, c.evaluate(snap))
	})
	m.HandleFunc("GET /projects/{id}/why", func(w http.ResponseWriter, r *http.Request) {
		if !c.taskPermit(w, r, r.URL.Query().Get("actor"), "factory.task.list", false, false) {
			return
		}
		if _, err := c.Store.Project(r.Context(), c.companyFrom(r), r.PathValue("id")); err != nil {
			reply(w, nil, err)
			return
		}
		snaps, err := c.Store.TaskSnapshots(r.Context(), c.companyFrom(r), r.PathValue("id"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		out := make([]tasks.Verdict, 0, len(snaps))
		for _, snap := range snaps {
			out = append(out, c.evaluate(snap))
		}
		write(w, out)
	})
	m.HandleFunc("GET /tasks/{id}/workspace", func(w http.ResponseWriter, r *http.Request) {
		if !c.taskPermit(w, r, r.URL.Query().Get("actor"), "factory.task.get", false, false) {
			return
		}
		v, err := c.Store.TaskWorkspace(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("GET /tasks/{id}/attempts", func(w http.ResponseWriter, r *http.Request) {
		if !c.taskPermit(w, r, r.URL.Query().Get("actor"), "factory.task.get", false, false) {
			return
		}
		v, err := c.Store.Attempts(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("GET /tasks/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		if !c.taskPermit(w, r, r.URL.Query().Get("actor"), "factory.task.get", false, false) {
			return
		}
		v, err := c.Store.EntityEvents(r.Context(), c.companyFrom(r), "", r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("GET /tasks/{id}/comments", func(w http.ResponseWriter, r *http.Request) {
		if !c.taskPermit(w, r, r.URL.Query().Get("actor"), "factory.task.get", false, false) {
			return
		}
		v, err := c.Store.Comments(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("PATCH /tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		var p tasks.Update
		if !decode(w, r, &p) {
			return
		}
		operation := "factory.task.update"
		if p.Assignee != nil && p.Title == nil && p.Description == nil && p.Priority == nil && p.OpenSpecChange == nil && p.Dependencies == nil && p.RequiredArtifacts == nil && p.Model == nil {
			operation = "factory.task.assign"
		}
		if !c.taskPermit(w, r, p.Actor, operation, false, false) {
			return
		}
		if p.Assignee != nil {
			if *p.Assignee != "" && !c.agentExists(w, *p.Assignee) {
				return
			}
			if !c.taskPermit(w, r, p.Actor, "factory.task.assign", false, true) {
				return
			}
		}
		if p.Model != nil && !c.modelOverride(w, r, p) {
			return
		}
		v, err := c.Store.UpdateTask(r.Context(), c.companyFrom(r), r.PathValue("id"), p)
		reply(w, v, err)
	})
	m.HandleFunc("POST /tasks/{id}/comments", func(w http.ResponseWriter, r *http.Request) {
		var p tasks.Comment
		if !decode(w, r, &p) {
			return
		}
		if !c.taskPermit(w, r, p.Actor, "factory.task.update", false, false) {
			return
		}
		if t, err := c.Store.Task(r.Context(), c.companyFrom(r), r.PathValue("id")); err == nil {
			p.Authority = c.hasAuthority(r.Context(), c.companyFrom(r), p.Actor, t)
		}
		v, err := c.Store.CommentTask(r.Context(), c.companyFrom(r), r.PathValue("id"), p)
		reply(w, v, err)
	})
	m.HandleFunc("POST /tasks/{id}/actions", func(w http.ResponseWriter, r *http.Request) {
		var p tasks.Action
		if !decode(w, r, &p) {
			return
		}
		if !c.taskPermit(w, r, p.Actor, "factory.task."+p.Action, false, false) {
			return
		}
		if p.Action == "cancel" && p.Actor != "" {
			// An agent cancels a task it has authority over (hierarchy D2) or one it created.
			t, err := c.Store.Task(r.Context(), c.companyFrom(r), r.PathValue("id"))
			if err != nil {
				reply(w, nil, err)
				return
			}
			if t.CreatedBy != p.Actor && !c.hasAuthority(r.Context(), c.companyFrom(r), p.Actor, t) {
				http.Error(w, "authority over the task required to cancel it", 403)
				return
			}
		}
		if p.Action == "ready" {
			t, err := c.Store.Task(r.Context(), c.companyFrom(r), r.PathValue("id"))
			if err != nil {
				reply(w, nil, err)
				return
			}
			if !c.agentExists(w, t.Assignee) {
				return
			}
		}
		v, err := c.Store.TaskAction(r.Context(), c.companyFrom(r), r.PathValue("id"), p)
		reply(w, v, err)
	})
	m.HandleFunc("POST /tasks/{id}/submit", func(w http.ResponseWriter, r *http.Request) {
		var p tasks.Submit
		if !decode(w, r, &p) {
			return
		}
		if len(p.Submission.Artifacts) > 0 && !c.taskPermit(w, r, p.Actor, "factory.artifact.register", false, false) {
			return
		}
		if !c.taskPermit(w, r, p.Actor, "factory.task.submit", false, false) {
			return
		}
		if p.Actor != "" {
			t, err := c.Store.Task(r.Context(), c.companyFrom(r), r.PathValue("id"))
			if err != nil {
				reply(w, nil, err)
				return
			}
			if t.Assignee != p.Actor {
				http.Error(w, "only assignee can submit", 403)
				return
			}
		}
		v, err := c.Store.SubmitTask(r.Context(), c.companyFrom(r), r.PathValue("id"), p)
		reply(w, v, err)
	})
	m.HandleFunc("POST /tasks/{id}/review", func(w http.ResponseWriter, r *http.Request) {
		var p tasks.Review
		if !decode(w, r, &p) {
			return
		}
		if !c.taskPermit(w, r, p.Actor, "factory.task.review", false, false) {
			return
		}
		if p.Actor != "" {
			t, err := c.Store.Task(r.Context(), c.companyFrom(r), r.PathValue("id"))
			if err != nil {
				reply(w, nil, err)
				return
			}
			if t.Assignee == p.Actor {
				http.Error(w, "assignee cannot accept own work", 403)
				return
			}
		}
		v, err := c.Store.ReviewTask(r.Context(), c.companyFrom(r), r.PathValue("id"), p)
		reply(w, v, err)
	})
}

// taskView is a task with why it does not run now (trigger-log-scheduling D3); the fields are added
// to every task read so clients and agents see the reason without a separate call.
type taskView struct {
	tasks.Task
	Reason  string        `json:"reason"`
	Clearer tasks.Clearer `json:"clearer"`
}

// needs fills what the assignee's runs need from a node (node-agent D3) from the agent catalog.
func (c *Core) needs(snap *tasks.Snapshot) {
	if a, ok := c.Agents[snap.Task.Assignee]; ok {
		snap.Needs = tasks.NodeNeeds{Provider: a.Spec.Runtime.Provider, Labels: a.Spec.Labels, Resources: a.Spec.Resources}
	}
}

func (c *Core) evaluate(snap tasks.Snapshot) tasks.Verdict {
	var a config.Agent
	a, snap.AgentConfigured = c.Agents[snap.Task.Assignee]
	c.needs(&snap)
	if snap.AgentConfigured {
		b, _ := json.Marshal(a)
		snap.AgentCap, snap.AgentProjectCap = storage.AgentCaps(b)
	}
	if snap.AgentConfigured && len(snap.QuotaPaused) > 0 {
		b, _ := json.Marshal(a)
		key := storage.QuotaKey(b, snap.Task.Assignee)
		if slices.Contains(snap.QuotaPaused, key) {
			snap.Paused = append(snap.Paused, "quota_key:"+key)
		}
	}
	return tasks.Evaluate(snap)
}
func (c *Core) agentExists(w http.ResponseWriter, name string) bool {
	if _, ok := c.Agents[name]; !ok {
		http.Error(w, "valid assignee required", 400)
		return false
	}
	return true
}
func (c *Core) taskPermit(w http.ResponseWriter, r *http.Request, actor, operation string, create, assign bool) bool {
	if actor == "" {
		return true
	} // Existing shared service token authorizes owner requests; no agent identity isolation.
	a, ok := c.Agents[actor]
	if !ok {
		http.Error(w, "actor agent not found", 404)
		return false
	}
	d := a.Authorize(operation)
	if d.Effect != "allow" && !(d.Effect == "approval" && approvedOperation(r.Context(), operation)) {
		w.Header().Set("Content-Type", "application/json")
		status := 403
		if d.Effect == "approval" {
			status = 423
		}
		w.WriteHeader(status)
		write(w, d)
		return false
	}
	if create && !a.Spec.Communication.CanCreateTasks || assign && !a.Spec.Communication.CanAssignTasks {
		http.Error(w, "agent communication permissions deny operation", 403)
		return false
	}
	return true
}
func (c *Core) Schedule(ctx context.Context) error {
	company := c.company()
	if err := c.Store.ReconcileArtifactTasks(ctx, company); err != nil {
		return err
	}
	ready, err := c.Store.Tasks(ctx, company, "", "READY")
	if err != nil {
		return err
	}
	// The dispatcher and why share one decision (trigger-log-scheduling D3): only a runnable verdict
	// dispatches (backoff, pauses, parks, dependencies, capacity and an idle task without new input wait).
	var runnable []tasks.Snapshot
	for _, t := range ready {
		snap, err := c.Store.TaskSnapshot(ctx, company, t.ID)
		if err != nil {
			return err
		}
		if v := c.evaluate(snap); v.Reason == tasks.ReasonRunnable {
			runnable = append(runnable, snap)
		}
	}
	// D4 order: priority, then the oldest unhandled trigger (the work waiting longest), then id. Dispatch
	// re-checks capacity, so a later task of a busy agent waits for the next tick.
	slices.SortStableFunc(runnable, func(a, b tasks.Snapshot) int {
		return cmp.Or(cmp.Compare(b.Task.Priority, a.Task.Priority), cmp.Compare(oldestTrigger(a), oldestTrigger(b)), strings.Compare(a.Task.ID, b.Task.ID))
	})
	for _, snap := range runnable {
		if err = c.dispatchTask(ctx, company, snap.Task, false); err != nil {
			return err
		}
	}
	now := c.Store.Now().Unix()
	if !c.ExplicitExits {
		return nil
	}
	// Review runs: the reviewer named by factory_exit_submit reviews the task while it stays IN_REVIEW.
	review, err := c.Store.Tasks(ctx, company, "", "IN_REVIEW")
	if err != nil {
		return err
	}
	for _, t := range review {
		if t.Reviewer == "" || t.Assignee != t.Reviewer || t.NotBefore > now {
			continue
		}
		snap, err := c.Store.TaskSnapshot(ctx, company, t.ID)
		if err != nil {
			return err
		}
		if snap.WakeSeq <= snap.HandledSeq || snap.CurrentRun != nil && snap.CurrentRun.Live() {
			continue
		}
		if err = c.dispatchTask(ctx, company, t, true); err != nil {
			return err
		}
	}
	return nil
}

// oldestTrigger is the sequence of the task's oldest unhandled trigger (sequences grow with time).
func oldestTrigger(s tasks.Snapshot) int64 {
	if len(s.Pending) == 0 {
		return s.WakeSeq
	}
	return s.Pending[0].Seq
}

// dispatchTask dispatches the run of a READY task (or a review run of a task in review). Its brief is built
// at claim (trigger-log-scheduling D8), so the prompt stays empty here.
func (c *Core) dispatchTask(ctx context.Context, company string, t tasks.Task, review bool) error {
	a, ok := c.Agents[t.Assignee]
	if !ok {
		return nil
	}
	p, err := c.Store.Project(ctx, company, t.ProjectID)
	if err != nil {
		return err
	}
	snapshot, hash, err := c.RunSnapshot(a)
	if err != nil {
		return err
	}
	provider := protocol.RuntimeProvider(a.Spec.Runtime.Provider)
	// A task's model override (set by an authority, validated against the assignee's provider then) applies
	// while it fits the current assignee's provider; one left by a reassignment is ignored with a feed event.
	model, ignoredModel := a.Spec.Runtime.Model, ""
	if t.Model != "" {
		if config.ModelFits(provider, t.Model) {
			model = t.Model
		} else {
			ignoredModel = t.Model
		}
	}
	options := a.RuntimeOptions()
	options["model"] = model
	spec := protocol.RunSpec{ID: protocol.ID(), Project: t.ProjectID, Agent: t.Assignee, Provider: provider, Workspace: p.Workspace, Instructions: a.Instructions, TimeoutSeconds: int(a.Timeout.Seconds()), RuntimeOptions: options, ConfigHash: hash, ConfigSnapshot: snapshot, Skills: append([]string{}, a.AvailableSkills...)}
	if _, err = c.Store.Dispatch(ctx, company, t.ID, t.Version, spec); err == nil && ignoredModel != "" {
		return c.Store.AddFeed(ctx, company, storage.FeedItem{EventID: protocol.ID(), Type: "task.model_ignored", EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: c.Store.Now(), TaskID: t.ID, RunID: spec.ID, Agent: t.Assignee, Actor: "system",
			Summary: fmt.Sprintf("model override %s does not fit %s's provider %s; run %s uses %s", ignoredModel, t.Assignee, provider, spec.ID, a.Spec.Runtime.Model), Ref: ignoredModel})
	}
	if err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return nil
		}
		if errors.Is(err, storage.ErrInput) {
			_, blockErr := c.Store.TaskAction(ctx, company, t.ID, tasks.Action{Version: t.Version, Action: "block", Reason: err.Error(), Actor: "system"})
			if blockErr != nil && !errors.Is(blockErr, storage.ErrConflict) {
				return blockErr
			}
			return nil
		}
		return err
	}
	return nil
}

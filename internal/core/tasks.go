package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
		v, err := c.Store.Tasks(r.Context(), c.companyFrom(r), r.URL.Query().Get("project"), r.URL.Query().Get("status"))
		reply(w, v, err)
	})
	m.HandleFunc("GET /tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !c.taskPermit(w, r, r.URL.Query().Get("actor"), "factory.task.get", false, false) {
			return
		}
		v, err := c.Store.Task(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
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
		if p.Assignee != nil && p.Title == nil && p.Description == nil && p.Priority == nil && p.OpenSpecChange == nil && p.Dependencies == nil && p.RequiredArtifacts == nil {
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
		v, err := c.Store.UpdateTask(r.Context(), c.companyFrom(r), r.PathValue("id"), p)
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
func (c *Core) evaluate(snap tasks.Snapshot) tasks.Verdict {
	_, snap.AgentConfigured = c.Agents[snap.Task.Assignee]
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
	failed, err := c.Store.Tasks(ctx, company, "", "FAILED")
	if err != nil {
		return err
	}
	for _, t := range failed {
		if !t.RetryEligible {
			continue
		}
		run, err := c.Store.Get(ctx, company, t.CurrentRunID)
		if err != nil {
			return err
		}
		var snapshot config.Agent
		if json.Unmarshal(run.Spec.ConfigSnapshot, &snapshot) != nil {
			continue
		}
		if t.Attempt > snapshot.Spec.Execution.MaxRetries {
			continue
		}
		if _, err = c.Store.TaskAction(ctx, company, t.ID, tasks.Action{Version: t.Version, Action: "ready", Reason: "Bounded retry after known failed turn; inspect previous attempt and workspace", Actor: "system", RequestID: "retry-" + t.CurrentRunID}); err != nil && !errors.Is(err, storage.ErrConflict) {
			if errors.Is(err, storage.ErrInput) {
				if _, e := c.Store.TaskAction(ctx, company, t.ID, tasks.Action{Version: t.Version, Action: "block", Reason: err.Error(), Actor: "system"}); e != nil && !errors.Is(e, storage.ErrConflict) {
					return e
				}
				continue
			}
			return err
		}
	}
	ready, err := c.Store.Tasks(ctx, company, "", "READY")
	if err != nil {
		return err
	}
	for _, t := range ready {
		a, ok := c.Agents[t.Assignee]
		if !ok {
			continue
		}
		p, err := c.Store.Project(ctx, company, t.ProjectID)
		if err != nil {
			return err
		}
		snapshot, err := json.Marshal(a)
		if err != nil {
			return err
		}
		hash := sha256.Sum256(snapshot)
		var prompt strings.Builder
		fmt.Fprintf(&prompt, "Project: %s (%s)\nTask: %s — %s\n\n%s\n", p.Name, p.ID, t.ID, t.Title, t.Description)
		if t.OpenSpecChange != "" {
			fmt.Fprintf(&prompt, "OpenSpec change: %s (openspec/changes/%s). Read proposal and relevant specs with factory_openspec_read before implementation; list current openspec/specs as needed. Documents remain current in files; this is a reference, not a copied snapshot.\n", t.OpenSpecChange, t.OpenSpecChange)
		}
		if t.Attempt > 0 {
			fmt.Fprintf(&prompt, "Previous attempt result: %s\nInspect the workspace and previous changes before continuing. Do not repeat completed side effects.\n", contextExcerpt(t.Result, 16*1024))
		}
		if t.WaitMessageID != "" {
			answer, err := c.Store.AnswerFor(ctx, company, t.WaitMessageID)
			if err != nil {
				return err
			}
			b, err := json.Marshal(answer)
			if err != nil {
				return err
			}
			fmt.Fprintf(&prompt, "Answer to your blocking question %s: %s\nContinue the original task using this explicit answer. Do not ask the same question again.\n", t.WaitMessageID, b)
		}
		if t.ReviewFeedback != "" {
			fmt.Fprintf(&prompt, "Previous review feedback: %s\n", t.ReviewFeedback)
		}
		for _, dep := range t.Dependencies {
			d, err := c.Store.Task(ctx, company, dep)
			if err != nil {
				return err
			}
			fmt.Fprintf(&prompt, "Dependency %s [%s]: %s\n", d.ID, d.Status, contextExcerpt(d.Result, 4*1024))
		}
		prompt.WriteString("Finish with a concise submission summary, changed files and checks. Your completed turn submits this task for review; it does not accept the task.\n")
		spec := protocol.RunSpec{ID: protocol.ID(), Project: t.ProjectID, Agent: t.Assignee, Provider: protocol.RuntimeProvider(a.Spec.Runtime.Provider), Workspace: p.Workspace, Prompt: prompt.String(), Instructions: a.Instructions, TimeoutSeconds: int(a.Timeout.Seconds()), RuntimeOptions: map[string]any{"model": a.Spec.Runtime.Model}, ConfigHash: hex.EncodeToString(hash[:]), ConfigSnapshot: snapshot, Skills: append([]string{}, a.AvailableSkills...)}
		if _, err = c.Store.Dispatch(ctx, company, t.ID, t.Version, spec); err != nil {
			if errors.Is(err, storage.ErrConflict) {
				continue
			}
			if errors.Is(err, storage.ErrInput) {
				_, blockErr := c.Store.TaskAction(ctx, company, t.ID, tasks.Action{Version: t.Version, Action: "block", Reason: err.Error(), Actor: "system"})
				if blockErr != nil && !errors.Is(blockErr, storage.ErrConflict) {
					return blockErr
				}
				continue
			}
			return err
		}
	}
	return nil
}

func contextExcerpt(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "\n[truncated; read the full task/attempt through Factory CLI when needed]"
}

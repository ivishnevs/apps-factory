package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"

	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// Project creation (project-creation): the owner creates projects with POST /projects, an agent allowed to
// (config.Agent.CreatesProjects) with factory_project_create behind the owner's approval of that exact call.
// Both end here; the node sets the repository up when the project's first task runs (storage.Dispatch).

// auditAssignee is the agent of an existing repository's onboarding audit.
func (c *Core) auditAssignee() string { return cmp.Or(c.AuditAssignee, config.DefaultAuditAssignee) }

// createProject creates the project and, when asked, its onboarding audit task (ready, assigned to the
// configured agent): the audit is the project's first task, so its run onboards the repository first. A
// repeated request (same request_id) answers the same project and task.
func (c *Core) createProject(ctx context.Context, company string, in tasks.ProjectCreate) (tasks.Project, error) {
	if err := storage.CheckProjectRepo(in); err != nil {
		return tasks.Project{}, err
	}
	if in.Audit {
		if _, ok := c.Agents[c.auditAssignee()]; !ok {
			return tasks.Project{}, fmt.Errorf("%w: the onboarding audit needs agent %s (configs/company.yaml projects.audit_assignee), which is not configured", storage.ErrInput, c.auditAssignee())
		}
	}
	p, err := c.Store.CreateProject(ctx, company, in)
	if err != nil || p.Setup == nil || !p.Setup.Audit {
		return p, err
	}
	t, err := c.Store.CreateTask(ctx, company, tasks.Create{ProjectID: p.ID, Title: "Audit the existing project " + p.Name, Description: auditBrief(p), Assignee: c.auditAssignee(), CreatedBy: p.CreatedBy, Ready: true, RequestID: "project-audit:" + p.ID})
	if err != nil {
		return p, fmt.Errorf("project %s created; its audit task failed: %w", p.ID, err)
	}
	return c.Store.SetProjectAuditTask(ctx, company, p.ID, t.ID)
}

// auditBrief is the onboarding audit task's description (the project-audit skill).
func auditBrief(p tasks.Project) string {
	return "Onboard project " + p.ID + " (an existing repository) before any new work, with the project-audit skill: " +
		"a read-only audit of the code, build, product state and (if it is live) store presence; OpenSpec baseline specs " +
		"describing the current behaviour, written to the project's OpenSpec tree; a report with options for what to do " +
		"next (factory_report_create, needs_owner_action when the owner must choose). Do not change product code."
}

func (c *Core) projectRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /projects", func(w http.ResponseWriter, r *http.Request) {
		var p tasks.ProjectCreate
		if !decode(w, r, &p) {
			return
		}
		v, err := c.createProject(r.Context(), c.companyFrom(r), p)
		reply(w, v, err)
	})
	// The node reports the repository setup of a run's project (project-creation D2), with the run's lease;
	// the body carries an existing repository's OpenSpec tree.
	m.HandleFunc("POST /runs/{id}/project-setup", func(w http.ResponseWriter, r *http.Request) {
		var p protocol.ProjectSetupReport
		if !decodeMax(w, r, &p, maxSetupBody) {
			return
		}
		v, err := c.Store.CompleteProjectSetup(r.Context(), c.companyFrom(r), r.PathValue("id"), p)
		reply(w, v, err)
	})
}

// maxSetupBody bounds a setup report (an existing repository's OpenSpec tree).
const maxSetupBody = 64 << 20

// projectCreateTool runs an approved factory_project_create (the gateway checked the approval of this exact
// call): one project per request_id of the agent.
func (c *Core) projectCreateTool(w http.ResponseWriter, r *http.Request, args map[string]any, run storage.Run, approval string) {
	str := func(k string) string { s, _ := args[k].(string); return s }
	in := tasks.ProjectCreate{ID: str("project_id"), Name: str("name"), Description: str("description"), RepoMode: str("repo_mode"), Workspace: str("workspace"), RepoURL: str("repo_url"), DefaultBranch: str("default_branch"),
		RequestID: "agent:" + run.Spec.Agent + ":" + str("request_id"), CreatedBy: run.Spec.Agent, ApprovalID: approval}
	if b, ok := args["audit"].(bool); ok {
		in.Audit = b
	}
	if b, ok := args["openspec"].(bool); ok {
		in.OpenSpec = &tasks.ProjectOpenSpec{Enabled: &b}
	}
	v, err := c.createProject(r.Context(), c.companyFrom(r), in)
	reply(w, v, err)
}

// taskProject is the project of an agent's factory_task_create: its run's project, or a project the agent
// created (project-creation D4: after approval it starts the new project's work).
func (c *Core) taskProject(r *http.Request, args map[string]any, run storage.Run) (string, error) {
	id, _ := args["project_id"].(string)
	if id == "" || id == run.Spec.Project {
		return run.Spec.Project, nil
	}
	p, err := c.Store.Project(r.Context(), c.companyFrom(r), id)
	if errors.Is(err, storage.ErrNotFound) || err == nil && p.CreatedBy != run.Spec.Agent {
		return "", fmt.Errorf("%w: tasks go to your run's project or to a project you created", storage.ErrForbidden)
	}
	return id, err
}

// projectTool reports whether the tool needs the project-creation permission.
func projectTool(t factorytools.Tool) bool { return t.Name == "factory_project_create" }

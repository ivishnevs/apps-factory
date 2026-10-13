package core

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"appfactory/internal/approvals"
	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/storage"
)

type approvalContextKey struct{}
type approvalAuditKey struct{}

func approvedOperation(ctx context.Context, op string) bool {
	ops, _ := ctx.Value(approvalContextKey{}).(map[string]bool)
	return ops[op]
}
func approvalMeta(name string) bool {
	return name == "factory_owner_request_approval" || name == "factory_approval_get" || name == "factory_artifact_request_approval"
}
func (c *Core) approvalRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /approvals", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Approvals(r.Context(), c.companyFrom(r), r.URL.Query().Get("project"))
		reply(w, v, err)
	})
	m.HandleFunc("GET /approvals/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Approval(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("POST /approvals/{id}/decision", func(w http.ResponseWriter, r *http.Request) {
		var p approvals.Decision
		if !decode(w, r, &p) {
			return
		}
		c.approvalMu.Lock()
		defer c.approvalMu.Unlock()
		v, err := c.Store.DecideApproval(r.Context(), c.companyFrom(r), r.PathValue("id"), p)
		reply(w, v, err)
	})
}
func (c *Core) approvalTool(w http.ResponseWriter, r *http.Request, p factorytools.Call, run storage.Run, snapshot, current config.Agent, allowed func(factorytools.Tool) bool) {
	if p.Name == "factory_approval_get" {
		a, err := c.Store.Approval(r.Context(), c.companyFrom(r), p.Arguments["approval_id"].(string))
		if err != nil {
			reply(w, nil, err)
			return
		}
		if a.ProjectID != run.Spec.Project || a.Kind != "artifact" && (a.Agent != run.Spec.Agent || a.TaskID != run.Spec.TaskID) {
			http.Error(w, "approval outside run scope", 403)
			return
		}
		write(w, a)
		return
	}
	var call factorytools.Call
	b, _ := json.Marshal(p.Arguments["call"])
	if err := json.Unmarshal(b, &call); err != nil {
		http.Error(w, "invalid call", 400)
		return
	}
	var target factorytools.Tool
	for _, t := range factorytools.Definitions() {
		if t.Name == call.Name {
			target = t
			break
		}
	}
	if target.Name == "" || approvalMeta(call.Name) {
		http.Error(w, "approval requires a supported action tool", 400)
		return
	}
	if err := factorytools.Validate(target, call.Arguments); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	// Require mutation IDs: an approval is a single logical intent, never a blanket read permission.
	key, _ := call.Arguments["request_id"].(string)
	if strings.TrimSpace(key) == "" || len(key) > 128 {
		http.Error(w, "approved action requires stable request_id", 400)
		return
	}
	if !allowed(target) {
		http.Error(w, "policy denies target action", 403)
		return
	}
	for _, a := range []config.Agent{snapshot, current} {
		owner := false
		for _, name := range a.Spec.Communication.CanMessage {
			if name == "owner" {
				owner = true
			}
		}
		if !owner {
			http.Error(w, "owner communication permission required", 403)
			return
		}
		if assignee, _ := call.Arguments["assignee"].(string); assignee != "" && (!a.Spec.Communication.CanAssignTasks || a.Authorize("factory.task.assign").Effect == "deny") {
			http.Error(w, "assignment denied", 403)
			return
		}
	}
	if id, _ := call.Arguments["id"].(string); id != "" {
		t, err := c.Store.Task(r.Context(), c.companyFrom(r), id)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if t.ProjectID != run.Spec.Project {
			http.Error(w, "target outside project", 403)
			return
		}
	}

	if id, _ := call.Arguments["decision_id"].(string); id != "" {
		d, err := c.Store.Decision(r.Context(), c.companyFrom(r), id)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if d.ProjectID != run.Spec.Project {
			http.Error(w, "decision outside project", 403)
			return
		}
	}
	var in approvals.Request
	b, _ = json.Marshal(p.Arguments)
	if err := json.Unmarshal(b, &in); err != nil {
		http.Error(w, "invalid approval request", 400)
		return
	}
	if _, present := p.Arguments["blocking"]; !present {
		in.Blocking = run.Spec.TaskID != ""
	}
	if id, _ := call.Arguments["id"].(string); in.Blocking && id != "" && id == run.Spec.TaskID {
		http.Error(w, "blocking approval changes current task version; use a separate execution task or blocking=false", 400)
		return
	}
	in.ProjectID = run.Spec.Project
	in.Agent = run.Spec.Agent
	in.TaskID = run.Spec.TaskID
	in.RunID = run.Spec.ID
	in.Call = call
	v, err := c.Store.RequestApproval(r.Context(), c.companyFrom(r), in)
	reply(w, v, err)
}

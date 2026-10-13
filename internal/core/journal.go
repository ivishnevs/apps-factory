package core

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"appfactory/internal/config"
	"appfactory/internal/decisions"
	"appfactory/internal/factorytools"
	"appfactory/internal/reports"
	"appfactory/internal/storage"
)

type approvalIntentKey struct{}

func httpPage(w http.ResponseWriter, r *http.Request) (int64, int, bool) {
	after, limit := int64(0), 20
	var err error
	if s := r.URL.Query().Get("after"); s != "" {
		after, err = strconv.ParseInt(s, 10, 64)
		if err != nil {
			http.Error(w, "invalid after", 400)
			return 0, 0, false
		}
	}
	if s := r.URL.Query().Get("limit"); s != "" {
		limit, err = strconv.Atoi(s)
		if err != nil {
			http.Error(w, "invalid limit", 400)
			return 0, 0, false
		}
	}
	if after < 0 || limit < 1 || limit > 100 {
		http.Error(w, "after >= 0 and limit 1..100 required", 400)
		return 0, 0, false
	}
	return after, limit, true
}
func (c *Core) journalRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /projects/{project}/reports", func(w http.ResponseWriter, r *http.Request) {
		var p reports.Create
		if !decode(w, r, &p) {
			return
		}
		p.Actor = "owner"
		v, err := c.Store.CreateReport(r.Context(), c.companyFrom(r), r.PathValue("project"), p)
		reply(w, v, err)
	})
	m.HandleFunc("GET /projects/{project}/reports", func(w http.ResponseWriter, r *http.Request) {
		a, l, ok := httpPage(w, r)
		if !ok {
			return
		}
		v, err := c.Store.Reports(r.Context(), c.companyFrom(r), r.PathValue("project"), r.URL.Query().Get("kind"), r.URL.Query().Get("task_id"), r.URL.Query().Get("run_id"), a, l)
		reply(w, v, err)
	})
	m.HandleFunc("GET /reports/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Report(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("POST /projects/{project}/decisions", func(w http.ResponseWriter, r *http.Request) {
		var p decisions.Propose
		if !decode(w, r, &p) {
			return
		}
		p.Actor = "owner"
		v, err := c.Store.ProposeDecision(r.Context(), c.companyFrom(r), r.PathValue("project"), p)
		reply(w, v, err)
	})
	m.HandleFunc("GET /projects/{project}/decisions", func(w http.ResponseWriter, r *http.Request) {
		a, l, ok := httpPage(w, r)
		if !ok {
			return
		}
		v, err := c.Store.Decisions(r.Context(), c.companyFrom(r), r.PathValue("project"), r.URL.Query().Get("status"), r.URL.Query().Get("topic"), a, l)
		reply(w, v, err)
	})
	m.HandleFunc("GET /decisions/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Decision(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("POST /decisions/{id}/record", func(w http.ResponseWriter, r *http.Request) {
		var p decisions.Record
		if !decode(w, r, &p) {
			return
		}
		p.Actor = "owner"
		v, err := c.Store.RecordDecision(r.Context(), c.companyFrom(r), r.PathValue("id"), p)
		reply(w, v, err)
	})
	m.HandleFunc("GET /projects/{project}/context", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.ProjectContext(r.Context(), c.companyFrom(r), r.PathValue("project"))
		reply(w, v, err)
	})
	m.HandleFunc("GET /projects/{project}/feed", func(w http.ResponseWriter, r *http.Request) {
		a, l, ok := httpPage(w, r)
		if !ok {
			return
		}
		v, err := c.Store.ProjectFeed(r.Context(), c.companyFrom(r), r.PathValue("project"), a, l)
		reply(w, v, err)
	})
}
func ownerNoticeAllowed(w http.ResponseWriter, agents ...config.Agent) bool {
	for _, a := range agents {
		ok := false
		for _, name := range a.Spec.Communication.CanMessage {
			if name == "owner" {
				ok = true
			}
		}
		if !ok {
			http.Error(w, "owner communication permission required", 403)
			return false
		}
	}
	return true
}
func toolPage(w http.ResponseWriter, args map[string]any) (int64, int, bool) {
	after, limit := int64(0), 10
	if v, ok := args["after"].(float64); ok {
		if v < 0 || v > 9007199254740991 {
			http.Error(w, "invalid after", 400)
			return 0, 0, false
		}
		after = int64(v)
	}
	if v, ok := args["limit"].(float64); ok {
		limit = int(v)
	}
	if limit < 1 || limit > 20 {
		http.Error(w, "MCP limit must be 1..20", 400)
		return 0, 0, false
	}
	return after, limit, true
}
func stringArg(args map[string]any, key string) string { v, _ := args[key].(string); return v }
func decodeTool(args map[string]any, out any) error {
	b, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
func brief(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	s = s[:max]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + " [truncated]", true
}
func (c *Core) journalTool(w http.ResponseWriter, r *http.Request, p factorytools.Call, run storage.Run, snapshot, current config.Agent) {
	args := p.Arguments
	ctx := r.Context()
	switch p.Name {
	case "factory_report_create":
		var in reports.Create
		if err := decodeTool(args, &in); err != nil {
			http.Error(w, "invalid report", 400)
			return
		}
		if in.NeedsOwnerAction && !ownerNoticeAllowed(w, snapshot, current) {
			return
		}
		in.TaskID = run.Spec.TaskID
		in.RunID = run.Spec.ID
		in.Actor = run.Spec.Agent
		v, err := c.Store.CreateReport(ctx, c.companyFrom(r), run.Spec.Project, in)
		reply(w, v, err)
	case "factory_report_get":
		v, err := c.Store.Report(ctx, c.companyFrom(r), stringArg(args, "report_id"))
		if err == nil && v.ProjectID != run.Spec.Project {
			http.Error(w, "report outside project", 403)
			return
		}
		reply(w, v, err)
	case "factory_report_list":
		a, l, ok := toolPage(w, args)
		if !ok {
			return
		}
		v, err := c.Store.Reports(ctx, c.companyFrom(r), run.Spec.Project, stringArg(args, "kind"), stringArg(args, "task_id"), stringArg(args, "run_id"), a, l)
		if err != nil {
			reply(w, nil, err)
			return
		}
		items := []map[string]any{}
		for _, x := range v.Items {
			summary, truncated := brief(x.Summary, 512)
			items = append(items, map[string]any{"id": x.ID, "kind": x.Kind, "summary": summary, "truncated": truncated, "source": x.Source, "task_id": x.TaskID, "run_id": x.RunID, "created_by": x.CreatedBy, "created_at": x.CreatedAt, "needs_owner_action": x.NeedsOwnerAction, "message_id": x.MessageID})
		}
		write(w, map[string]any{"items": items, "next_after": v.NextAfter, "has_more": v.HasMore})
	case "factory_decision_decide":
		var in decisions.Decide
		if err := decodeTool(args, &in); err != nil {
			http.Error(w, "invalid decision", 400)
			return
		}
		in.Actor, in.RunID, in.TaskID = run.Spec.Agent, run.Spec.ID, run.Spec.TaskID
		v, err := c.Store.DecideDecision(ctx, c.companyFrom(r), run.Spec.Project, in, current)
		reply(w, v, err)
	case "factory_decision_propose":
		if !ownerNoticeAllowed(w, snapshot, current) {
			return
		}
		var in decisions.Propose
		if err := decodeTool(args, &in); err != nil {
			http.Error(w, "invalid proposal", 400)
			return
		}
		in.TaskID = run.Spec.TaskID
		in.RunID = run.Spec.ID
		in.Actor = run.Spec.Agent
		v, err := c.Store.ProposeDecision(ctx, c.companyFrom(r), run.Spec.Project, in)
		reply(w, v, err)
	case "factory_decision_get", "factory_decision_record":
		id := stringArg(args, "decision_id")
		d, err := c.Store.Decision(ctx, c.companyFrom(r), id)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if d.ProjectID != run.Spec.Project {
			http.Error(w, "decision outside project", 403)
			return
		}
		if p.Name == "factory_decision_get" {
			write(w, d)
			return
		}
		if !approvedOperation(ctx, "factory.decision.record") {
			http.Error(w, "exact owner authorization required", 423)
			return
		}
		var in decisions.Record
		if err = decodeTool(args, &in); err != nil {
			http.Error(w, "invalid decision record", 400)
			return
		}
		in.Actor = run.Spec.Agent
		in.ScopeTaskID = run.Spec.TaskID
		in.ApprovalID, _ = ctx.Value(approvalIntentKey{}).(string)
		in.GrantedCall = &p
		v, err := c.Store.RecordDecision(ctx, c.companyFrom(r), id, in)
		reply(w, v, err)
	case "factory_decision_list":
		a, l, ok := toolPage(w, args)
		if !ok {
			return
		}
		status := stringArg(args, "status")
		if _, ok := args["status"]; !ok {
			status = "approved"
		}
		v, err := c.Store.Decisions(ctx, c.companyFrom(r), run.Spec.Project, status, stringArg(args, "topic"), a, l)
		if err != nil {
			reply(w, nil, err)
			return
		}
		items := []map[string]any{}
		for _, x := range v.Items {
			value, t := brief(x.Value, 512)
			rationale, u := brief(x.Rationale, 256)
			items = append(items, map[string]any{"category": x.Category, "authority": x.Authority, "id": x.ID, "topic": x.Topic, "status": x.Status, "version": x.Version, "value": value, "rationale": rationale, "approved_by": x.ApprovedBy, "created_by": x.CreatedBy, "truncated": t || u})
		}
		write(w, map[string]any{"items": items, "next_after": v.NextAfter, "has_more": v.HasMore})
	case "factory_project_feed":
		a, l, ok := toolPage(w, args)
		if !ok {
			return
		}
		v, err := c.Store.ProjectFeed(ctx, c.companyFrom(r), run.Spec.Project, a, l)
		reply(w, v, err)
	case "factory_project_context":
		v, err := c.Store.ProjectContext(ctx, c.companyFrom(r), run.Spec.Project)
		if err != nil {
			reply(w, nil, err)
			return
		}
		name, nt := brief(v.Project.Name, 512)
		description, dt := brief(v.Project.Description, 4096)
		workspace, wt := brief(v.Project.Workspace, 2048)
		v.Project.Name, v.Project.Description, v.Project.Workspace = name, description, workspace
		v.ProjectTruncated = nt || dt || wt
		if len(v.Decisions) > 12 {
			v.Decisions = v.Decisions[:12]
			v.MoreDecisions = true
		}
		for i := range v.Decisions {
			d := &v.Decisions[i]
			value, t := brief(d.Value, 512)
			rationale, u := brief(d.Rationale, 256)
			d.Value = value
			d.Rationale = rationale
			d.Truncated = d.Truncated || t || u
		}
		write(w, v)
	default:
		http.Error(w, "unknown journal tool", 400)
	}
}
func journalToolName(name string) bool {
	return strings.HasPrefix(name, "factory_report_") || strings.HasPrefix(name, "factory_decision_") || name == "factory_project_feed" || name == "factory_project_context"
}

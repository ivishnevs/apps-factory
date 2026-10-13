package core

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// runToken binds a run credential to the run's company, ID and lease.
func (c *Core) runToken(company, id, lease string) string {
	mac := hmac.New(sha256.New, []byte(c.Token))
	mac.Write([]byte("factory-mcp-v2\x00" + company + "\x00" + id + "\x00" + lease))
	return "run." + id + "." + hex.EncodeToString(mac.Sum(nil))
}
func (c *Core) credential(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Lease string `json:"lease"`
	}
	if !decode(w, r, &p) {
		return
	}
	var lease, status string
	var until int64
	company := c.companyFrom(r)
	err := c.Store.DB.QueryRowContext(r.Context(), "SELECT lease,status,lease_until FROM runs WHERE company_id=? AND id=?", company, r.PathValue("id")).Scan(&lease, &status, &until)
	if err != nil || lease == "" || subtle.ConstantTimeCompare([]byte(p.Lease), []byte(lease)) != 1 || status != "RUNNING" || !c.Store.LeaseLive(until) {
		http.Error(w, "active run lease required", 403)
		return
	}
	run, err := c.Store.Get(r.Context(), c.companyFrom(r), r.PathValue("id"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	write(w, map[string]string{"token": c.runToken(company, run.Spec.ID, lease), "instructions": runtimeInstructions(run.Spec)})
}
func (c *Core) agentGateway(w http.ResponseWriter, r *http.Request, routes http.Handler) {
	// Only these two endpoints accept run credentials; they never authorize owner APIs.
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(token, ".")
	if c.Token == "" || len(parts) != 3 || parts[0] != "run" {
		http.Error(w, "run credential required", 401)
		return
	}
	var lease, status string
	var until int64
	// A token minted for another company's run fails both the scoped lookup and the HMAC.
	company := c.company()
	err := c.Store.DB.QueryRowContext(r.Context(), "SELECT lease,status,lease_until FROM runs WHERE company_id=? AND id=?", company, parts[1]).Scan(&lease, &status, &until)
	if err != nil || lease == "" || status != "RUNNING" || !c.Store.LeaseLive(until) || subtle.ConstantTimeCompare([]byte(token), []byte(c.runToken(company, parts[1], lease))) != 1 {
		http.Error(w, "run credential expired or invalid", 401)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), companyKey{}, company))
	run, err := c.Store.Get(r.Context(), c.companyFrom(r), parts[1])
	if err != nil {
		reply(w, nil, err)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), runKey{}, run.Spec))
	current, ok := c.Agents[run.Spec.Agent]
	if !ok {
		http.Error(w, "agent unavailable", 403)
		return
	}
	var snapshot config.Agent
	if len(run.Spec.ConfigSnapshot) > 0 {
		if err = json.Unmarshal(run.Spec.ConfigSnapshot, &snapshot); err != nil {
			http.Error(w, "invalid agent snapshot", 500)
			return
		}
	} else {
		snapshot = current
	}
	allowed := func(t factorytools.Tool) bool {
		for _, a := range []config.Agent{snapshot, current} {
			if a.Authorize(t.Operation).Effect == "deny" {
				return false
			}
			if t.Operation == "factory.task.create" && !a.Spec.Communication.CanCreateTasks {
				return false
			}
			if t.Operation == "factory.task.assign" && !a.Spec.Communication.CanAssignTasks {
				return false
			}
		}
		return true
	}
	if r.URL.Path == "/agent/command" {
		if r.Method != "POST" {
			http.Error(w, "not found", 404)
			return
		}
		c.commandCheck(w, r, run, snapshot, current)
		return
	}
	if r.URL.Path == "/agent/tools" && r.Method == "GET" {
		out := []factorytools.Tool{}
		for _, t := range factorytools.Definitions() {
			if allowed(t) {
				out = append(out, t)
			}
		}
		write(w, out)
		return
	}
	if r.URL.Path != "/agent/call" || r.Method != "POST" {
		http.Error(w, "not found", 404)
		return
	}
	var p factorytools.Call
	if !decode(w, r, &p) {
		return
	}
	// Audit before dispatch. Store only identity/tool/key/status, never tokens or model arguments.
	requestID, _ := p.Arguments["request_id"].(string)
	auditID := protocol.ID()
	if _, err = c.Store.DB.ExecContext(r.Context(), "INSERT INTO tool_calls(id,run_id,agent,project_id,tool,request_id,status,created_at,company_id) VALUES(?,?,?,?,?,?,0,?,?)", auditID, run.Spec.ID, run.Spec.Agent, run.Spec.Project, p.Name, requestID, c.Store.Now().Unix(), company); err != nil {
		reply(w, nil, err)
		return
	}
	c.approvalMu.Lock()
	defer c.approvalMu.Unlock()
	response := &toolResponse{header: http.Header{}}
	r = r.WithContext(context.WithValue(r.Context(), approvalAuditKey{}, auditID))
	c.dispatchTool(response, r, routes, p, run, snapshot, current, allowed)
	if response.status == 0 {
		response.status = 200
	}
	if _, err = c.Store.DB.ExecContext(r.Context(), "UPDATE tool_calls SET status=? WHERE id=?", response.status, auditID); err != nil {
		reply(w, nil, err)
		return
	}
	for k, vs := range response.header {
		w.Header()[k] = vs
	}
	w.WriteHeader(response.status)
	_, _ = w.Write(response.body.Bytes())
}

type toolResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *toolResponse) Header() http.Header { return w.header }
func (w *toolResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *toolResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(b)
}

func (c *Core) dispatchTool(w http.ResponseWriter, r *http.Request, routes http.Handler, p factorytools.Call, run storage.Run, snapshot, current config.Agent, allowed func(factorytools.Tool) bool) {
	var def factorytools.Tool
	found := false
	for _, t := range factorytools.Definitions() {
		if t.Name == p.Name {
			def = t
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "unknown tool", 400)
		return
	}
	if p.Arguments == nil {
		p.Arguments = map[string]any{}
	}
	if err := factorytools.Validate(def, p.Arguments); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !allowed(def) {
		http.Error(w, "agent permissions deny tool", 403)
		return
	}
	if approvalMeta(p.Name) {
		if p.Name == "factory_artifact_request_approval" {
			c.artifactTool(w, r, p, run, snapshot, current)
		} else {
			c.approvalTool(w, r, p, run, snapshot, current, allowed)
		}
		return
	}
	ops := map[string]bool{def.Operation: true}
	if assignee, _ := p.Arguments["assignee"].(string); assignee != "" {
		ops["factory.task.assign"] = true
	}
	if p.Name == "factory_task_submit" {
		submission, _ := p.Arguments["submission"].(map[string]any)
		refs, _ := submission["artifacts"].([]any)
		if len(refs) > 0 {
			ops["factory.artifact.register"] = true
		}
	}
	if p.Name == "factory_decision_decide" {
		category, _ := p.Arguments["category"].(string)
		if len(snapshot.DecisionAuthority(category)) == 0 || len(current.DecisionAuthority(category)) == 0 {
			http.Error(w, "explicit decision category delegation required in captured and current policy", 403)
			return
		}
		ops["factory.decision.decide."+category] = true
	}
	needsApproval := p.Name == "factory_decision_record"
	for op := range ops {
		for _, a := range []config.Agent{snapshot, current} {
			d := a.Authorize(op)
			if d.Effect == "deny" {
				http.Error(w, "policy denies action", 403)
				return
			}
			if d.Effect == "approval" {
				needsApproval = true
			}
		}
	}
	a, err := c.Store.CallApproval(r.Context(), c.companyFrom(r), run.Spec.Project, run.Spec.Agent, run.Spec.TaskID, p)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		reply(w, nil, err)
		return
	}
	if needsApproval || err == nil {
		if err != nil || a.Status != "APPROVED" || a.ExpiresAt <= c.Store.Now().Unix() {
			w.WriteHeader(423)
			write(w, map[string]string{"effect": "approval", "approval_id": a.ID, "status": a.Status, "instruction": "Request factory_owner_request_approval for this exact call and finish the turn. Only an APPROVED decision authorizes it."})
			return
		}
		auditID, _ := r.Context().Value(approvalAuditKey{}).(string)
		if _, err = c.Store.DB.ExecContext(r.Context(), "UPDATE tool_calls SET approval_id=? WHERE id=?", a.ID, auditID); err != nil {
			reply(w, nil, err)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), approvalContextKey{}, ops))
		r = r.WithContext(context.WithValue(r.Context(), approvalIntentKey{}, a.ID))
	}

	args := p.Arguments
	if _, ok := def.InputSchema["properties"].(map[string]any)["request_id"]; ok {
		if strings.TrimSpace(args["request_id"].(string)) == "" {
			http.Error(w, "stable request_id required", 400)
			return
		}
	}
	if strings.HasPrefix(p.Name, "factory_openspec_") {
		c.openSpecTool(w, r, p, run)
		return
	}
	if journalToolName(p.Name) {
		c.journalTool(w, r, p, run, snapshot, current)
		return
	}
	if strings.HasPrefix(p.Name, "factory_artifact_") {
		c.artifactTool(w, r, p, run, snapshot, current)
		return
	}
	if strings.HasPrefix(p.Name, "factory_message_") || p.Name == "factory_owner_ask" {
		c.messageTool(w, r, p.Name, args, run, snapshot, current)
		return
	}
	id, _ := args["id"].(string)
	if id != "" {
		t, err := c.Store.Task(r.Context(), c.companyFrom(r), id)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if t.ProjectID != run.Spec.Project {
			http.Error(w, "task outside run project", 403)
			return
		}
	}
	if _, ok := args["id"]; ok && id == "" {
		http.Error(w, "task id required", 400)
		return
	}
	// Enforce assignment privileges also for create-with-assignee (including self).
	if assignee, ok := args["assignee"].(string); ok && assignee != "" {
		for _, a := range []config.Agent{snapshot, current} {
			if !a.Spec.Communication.CanAssignTasks {
				http.Error(w, "assignment permission required", 403)
				return
			}
			if d := a.Authorize("factory.task.assign"); d.Effect != "allow" && !(d.Effect == "approval" && approvedOperation(r.Context(), "factory.task.assign")) {
				status := 403
				if d.Effect == "approval" {
					status = 423
				}
				w.WriteHeader(status)
				write(w, d)
				return
			}
		}
	}
	if strings.HasPrefix(p.Name, "factory_skills_") {
		if c.Skills == nil {
			http.Error(w, "skill registry unavailable", 503)
			return
		}
		available := []string{}
		for _, s := range snapshot.AvailableSkills {
			for _, v := range current.AvailableSkills {
				if s == v {
					available = append(available, s)
					break
				}
			}
		}
		if p.Name == "factory_skills_read" {
			skill := args["skill"].(string)
			found := false
			for _, s := range available {
				if s == skill {
					found = true
				}
			}
			if !found {
				http.Error(w, "skill not available to run", 403)
				return
			}
			resource, _ := args["resource"].(string)
			content, err := c.Skills.Read(skill, resource)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			write(w, map[string]string{"name": skill, "content": content})
			return
		}
		q, _ := args["q"].(string)
		category, _ := args["category"].(string)
		write(w, c.Skills.Entries(available, q, category))
		return
	}
	if p.Name == "factory_agent_list" {
		out := []map[string]any{}
		for _, name := range config.Names(c.Agents) {
			a := c.Agents[name]
			out = append(out, map[string]any{"name": name, "role": a.Spec.Role, "capabilities": a.Spec.Capabilities})
		}
		write(w, out)
		return
	}
	method, path := "GET", ""
	query := url.Values{"actor": []string{run.Spec.Agent}}
	switch p.Name {
	case "factory_project_get":
		path = "/projects/" + url.PathEscape(run.Spec.Project)
	case "factory_task_get":
		path = "/tasks/" + url.PathEscape(id)
	case "factory_task_list":
		path = "/tasks"
		query.Set("project", run.Spec.Project)
		if status, ok := args["status"].(string); ok {
			query.Set("status", status)
		}
	case "factory_task_create":
		method = "POST"
		path = "/tasks"
		args["project_id"] = run.Spec.Project
	case "factory_task_update", "factory_task_assign":
		method = "PATCH"
		path = "/tasks/" + url.PathEscape(id)
	case "factory_task_ready", "factory_task_block", "factory_task_cancel":
		method = "POST"
		path = "/tasks/" + url.PathEscape(id) + "/actions"
		args["action"] = strings.TrimPrefix(p.Name, "factory_task_")
	case "factory_task_submit":
		if run.Spec.TaskID == "" || id != run.Spec.TaskID {
			http.Error(w, "submission must belong to current task/run", 403)
			return
		}
		method = "POST"
		path = "/tasks/" + url.PathEscape(id) + "/submit"
		args["run_id"] = run.Spec.ID
	case "factory_task_accept", "factory_task_reject":
		method = "POST"
		path = "/tasks/" + url.PathEscape(id) + "/review"
		args["accept"] = p.Name == "factory_task_accept"
	default:
		http.Error(w, "unimplemented tool", 500)
		return
	}
	delete(args, "id")
	args["actor"] = run.Spec.Agent
	b, err := json.Marshal(args)
	if err != nil {
		http.Error(w, fmt.Sprint(err), 400)
		return
	}
	forwarded, err := http.NewRequestWithContext(r.Context(), method, path+"?"+query.Encode(), bytes.NewReader(b))
	if err != nil {
		http.Error(w, "invalid route", 400)
		return
	}
	forwarded.Header.Set("Content-Type", "application/json")
	routes.ServeHTTP(w, forwarded)
}

package core

import (
	"appfactory/internal/tasks"
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

// liveRun: a run whose lease authorizes its credential: claimed (the runtime launches before /start) or
// running; a CANCELLING or ENDED run's credential is refused.
func liveRun(status string) bool { return status == "CLAIMED" || status == "RUNNING" }

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
	switch {
	case err != nil || lease == "":
		refuseWith(w, 403, "unknown_run", "active run lease required", nil)
		return
	case subtle.ConstantTimeCompare([]byte(p.Lease), []byte(lease)) != 1:
		refuseWith(w, 403, "lease_changed", "active run lease required", nil)
		return
	case !liveRun(status) || !c.Store.LeaseLive(until):
		refuseWith(w, 403, "run_not_live", "active run lease required", nil)
		return
	}
	run, err := c.Store.Get(r.Context(), c.companyFrom(r), r.PathValue("id"))
	if err != nil {
		reply(w, nil, err)
		return
	}
	instructions := runtimeInstructions(run.Spec)
	if c.ExplicitExits {
		instructions += exitInstructions
	}
	write(w, map[string]any{"token": c.runToken(company, run.Spec.ID, lease), "instructions": instructions, "explicit_exits": c.ExplicitExits})
}
func (c *Core) agentGateway(w http.ResponseWriter, r *http.Request, routes http.Handler) {
	// Only these two endpoints accept run credentials; they never authorize owner APIs.
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(token, ".")
	if c.Token == "" || len(parts) != 3 || parts[0] != "run" {
		refuseWith(w, 401, "credential_missing", "run credential required", nil)
		return
	}
	var company, lease, status, outcome string
	var until int64
	var unreleased bool
	// The run names its company (one Core serves many, company-self-service D4); the token's HMAC covers the
	// company, so a token minted for another company's run fails it.
	err := c.Store.DB.QueryRowContext(r.Context(), "SELECT company_id,lease,status,lease_until,outcome,unreleased FROM runs WHERE id=? ORDER BY company_id LIMIT 1", parts[1]).Scan(&company, &lease, &status, &until, &outcome, &unreleased)
	if err != nil || lease == "" {
		refuseWith(w, 401, "unknown_run", "run credential expired or invalid", nil)
		return
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(c.runToken(company, parts[1], lease))) != 1 {
		// The token is an HMAC of the lease: after a new claim it never passes again.
		refuseWith(w, 401, "lease_changed", "run credential expired or invalid", nil)
		return
	}
	// A late exit: Core judged the run lost while its node was unreachable, the run still holds its task, and its
	// node delivers the finished exit. Only /agent/exit passes; Store.Exit decides it (node-wake-recovery).
	late := r.URL.Path == "/agent/exit" && status == "ENDED" && unreleased && (outcome == "lost" || outcome == "timed_out")
	if !late && (!liveRun(status) || !c.Store.LeaseLive(until)) {
		if exitPath(r.URL.Path) && r.URL.Path != "/agent/brief" {
			// The credential is genuine but its run was stopped or ended: the agent must stop (410).
			jsonStatus(w, http.StatusGone, map[string]string{"reason": "not_current_run", "hint": "this run was stopped or replaced; finish the turn"})
			return
		}
		refuseWith(w, 401, "run_not_live", "run credential expired or invalid", nil)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), companyKey{}, company))
	run, err := c.Store.Get(r.Context(), c.companyFrom(r), parts[1])
	if err != nil {
		reply(w, nil, err)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), runKey{}, run.Spec))
	current, ok := c.cat(company).Agents[run.Spec.Agent]
	if !ok {
		refuseWith(w, 403, "agent_unavailable", "agent unavailable", nil)
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
			if t.Operation == "factory.task.assign" && !config.CanAssign(c.cat(company).Agents, a.Metadata.Name) {
				return false
			}
			if projectTool(t) && !a.CreatesProjects() {
				return false
			}
		}
		return true
	}
	if exitPath(r.URL.Path) {
		c.exitRoute(w, r, run)
		return
	}
	if r.URL.Path == "/agent/command" {
		if r.Method != "POST" {
			http.Error(w, "not found", 404)
			return
		}
		c.commandCheck(w, r, run, snapshot, current)
		return
	}
	if r.URL.Path == "/agent/blobs" || strings.HasPrefix(r.URL.Path, "/agent/blobs/") {
		c.agentBlob(w, r, run)
		return
	}
	if r.URL.Path == "/agent/tools" && r.Method == "GET" {
		write(w, c.agentTools(company, current, allowed))
		return
	}
	if r.URL.Path != "/agent/call" || r.Method != "POST" {
		http.Error(w, "not found", 404)
		return
	}
	var p factorytools.Call
	if !decodeMax(w, r, &p, maxToolCallBody) {
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
	c.withAuthorityInput(r, run, response)
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
	defs := factorytools.Definitions()
	if c.ExplicitExits {
		defs = append(defs, factorytools.ExitDefinitions()...)
	}
	for _, t := range defs {
		if t.Name == p.Name && !(c.ExplicitExits && factorytools.ReplacedByExits[t.Name]) {
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
	if factorytools.ExitKind(p.Name) != "" {
		c.exitCall(w, r, p, run)
		return
	}
	if p.Name == "factory_task_direct" {
		c.directiveCall(w, r, p, run, current)
		return
	}
	if p.Name == "factory_comment_forward" {
		id, _ := p.Arguments["comment_id"].(string)
		note, _ := p.Arguments["note"].(string)
		v, err := c.Store.ForwardComment(r.Context(), c.companyFrom(r), run.Spec.ID, id, note)
		reply(w, v, err)
		return
	}
	if p.Name == "factory_people_brief" {
		c.peopleBriefTool(w, r, p, run)
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
		cites, _ := p.Arguments["cites"].([]any)
		// approval-delegation: people-only modes and money or destructive categories always go to people; otherwise a
		// policy-delegated category, or the project's decision mode naming this agent (the lead with cites).
		if mode, _, setting := c.approvalMode(r.Context(), c.companyFrom(r), run.Spec.Project, tasks.KindDecisions); mode.Mode == tasks.ApproveOwner || tasks.PeopleOnlyCategory(category) {
			http.Error(w, "this decision is approved by people only ("+setting+"): propose it with factory_decision_propose", 403)
			return
		}
		if len(snapshot.DecisionAuthority(category)) > 0 && len(current.DecisionAuthority(category)) > 0 {
			ops["factory.decision.decide."+category] = true
		} else if ok, setting, err := c.decisionDelegation(r.Context(), c.companyFrom(r), run.Spec.Project, run.Spec.Agent, category, len(cites)); ok {
			r = r.WithContext(context.WithValue(r.Context(), delegationKey{}, setting))
		} else {
			msg := "explicit decision category delegation required in captured and current policy, or the project's decision mode naming you: propose it with factory_decision_propose"
			if err != nil {
				msg = err.Error()
			}
			http.Error(w, msg, 403)
			return
		}
	}
	if p.Name == "factory_approval_decide" {
		c.approvalDecideTool(w, r, p, run)
		return
	}
	// A project is created only with the owner's approval of the exact call (project-creation D4).
	// A catalog change is applied only with the owner's approval of the exact call (company-self-service 3.7).
	needsApproval := p.Name == "factory_decision_record" || p.Name == "factory_project_create" || p.Name == "factory_catalog_change"
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
	if p.Name == "factory_project_create" {
		approval, _ := r.Context().Value(approvalIntentKey{}).(string)
		c.projectCreateTool(w, r, args, run, approval)
		return
	}
	if p.Name == "factory_catalog_get" || p.Name == "factory_catalog_change" {
		c.catalogTool(w, r, p.Name, args, run)
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
	if strings.HasPrefix(p.Name, "factory_message_") || p.Name == "factory_owner_ask" || p.Name == "factory_question_forward" {
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
	// Enforce assignment privileges also for create-with-assignee (including self); a list's assignee is a filter.
	if assignee, ok := args["assignee"].(string); ok && assignee != "" && p.Name != "factory_task_list" {
		if why := config.AssignRefusal(c.cat(c.companyFrom(r)).Agents, run.Spec.Agent, assignee); why != "" {
			http.Error(w, "assignment outside your scope: "+why, 403)
			return
		}
		for _, a := range []config.Agent{snapshot, current} {
			if !config.CanAssign(c.cat(c.companyFrom(r)).Agents, run.Spec.Agent) && assignee != run.Spec.Agent {
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
	if p.Name == "factory_settings_get" {
		// Read-only, for the run's own company and project only.
		scope, _ := args["scope"].(string)
		switch scope {
		case "company":
			rules, err := c.Store.CompanyRules(r.Context(), c.companyFrom(r))
			reply(w, map[string]string{"scope": scope, "rules": rules}, err)
		case "project":
			project, err := c.Store.Project(r.Context(), c.companyFrom(r), run.Spec.Project)
			reply(w, map[string]string{"scope": scope, "project": project.ID, "rules": project.Rules}, err)
		default:
			http.Error(w, "scope must be company or project", 400)
		}
		return
	}
	if p.Name == "factory_feed_list" {
		c.feedListTool(w, r, args, run)
		return
	}
	if p.Name == "factory_nodes" {
		c.nodesTool(w, r)
		return
	}
	if p.Name == "factory_agent_list" {
		out := []map[string]any{}
		agents := c.cat(c.companyFrom(r)).Agents
		for _, name := range config.Names(agents) {
			a := agents[name]
			out = append(out, map[string]any{"name": name, "role": a.Spec.Role, "team": a.Spec.Team, "description": a.Metadata.Description, "capabilities": a.Spec.Capabilities})
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
		// Compact by default (task-duplicate-warnings 3.1); full: true adds descriptions and the rest.
		path = "/tasks"
		query.Set("project", run.Spec.Project)
		for _, k := range []string{"status", "assignee", "openspec_change", "covers", "query"} {
			if v, ok := args[k].(string); ok && v != "" {
				query.Set(k, v)
			}
		}
		if open, _ := args["open"].(bool); open {
			query.Set("open", "1")
		}
		if full, _ := args["full"].(bool); !full {
			query.Set("compact", "1")
		}
	case "factory_task_create":
		method = "POST"
		path = "/tasks"
		project, err := c.taskProject(r, args, run)
		if err != nil {
			reply(w, nil, err)
			return
		}
		args["project_id"] = project
	case "factory_task_update", "factory_task_assign":
		method = "PATCH"
		path = "/tasks/" + url.PathEscape(id)
	case "factory_task_ready", "factory_task_block", "factory_task_cancel":
		method = "POST"
		path = "/tasks/" + url.PathEscape(id) + "/actions"
		args["action"] = strings.TrimPrefix(p.Name, "factory_task_")
	case "factory_task_pause", "factory_task_resume", "factory_task_reassign", "factory_task_reprioritize":
		// Authority actions (hierarchy D2); pause/resume with agent act on an agent of the actor's subtree.
		method, path = "POST", "/tasks/"+url.PathEscape(id)+"/"+strings.TrimPrefix(p.Name, "factory_task_")
		if agent, _ := args["agent"].(string); agent != "" && id == "" {
			path = "/agents/" + url.PathEscape(agent) + "/" + strings.TrimPrefix(p.Name, "factory_task_")
			delete(args, "version")
			delete(args, "request_id")
		} else if id == "" {
			http.Error(w, "task id (or agent) required", 400)
			return
		}
		delete(args, "agent")
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

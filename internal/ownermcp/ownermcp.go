// Package ownermcp is the owner's MCP server (setup-assistant 1): `factory mcp` serves it on stdio to the owner's own
// coding agent (Claude Code, Codex, Claude Desktop), not to company agents. Read tools answer from Core's owner API
// and the local node (status, why a task or chat waits, runs with their causes and usage in words, catalog
// validation with fixes, an agent's effective skills and prompt, the company doctor, runbooks, the node's doctor
// and log). Change tools (pause, resume, enrollment code, fingerprint approval, secret set, skill install) answer a
// call first with the exact effect and a confirmation code; only a second call with that code, after the owner
// agreed in their agent, changes anything. No tool outputs a secret value.
package ownermcp

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"appfactory/internal/factorytools"
	"appfactory/internal/redact"
	"appfactory/internal/storage"
	"appfactory/internal/support"
	"appfactory/internal/tasks"
)

// Core is the owner API (worker.API with the owner's token).
type Core interface {
	Do(ctx context.Context, method, path string, body, result any) error
	Get(ctx context.Context, path string) (io.ReadCloser, error)
}

// Local is the machine the server runs on, for the local node tools; nil fields disable them.
type Local struct {
	// NodeDoctor runs `factory-node doctor` and returns its output and whether it passed.
	NodeDoctor func(ctx context.Context) (string, bool, error)
	// NodeLog returns the last lines of the node's log.
	NodeLog func(ctx context.Context, lines int) (string, error)
	// ReadFile reads a local file (secret_set's file argument).
	ReadFile func(path string) ([]byte, error)
}

// Server answers the MCP tools.
type Server struct {
	Core  Core
	Local Local
	Now   func() time.Time
	// ConfirmTTL is how long a change's confirmation code stays valid (default 10 minutes).
	ConfirmTTL time.Duration

	once sync.Once
	key  []byte
	mu   sync.Mutex
	used map[string]bool
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Serve runs the server on stdio until in ends.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	return factorytools.Serve(ctx, in, out, func(context.Context) (any, error) { return s.Tools(), nil }, s.Call)
}

// Tool is an MCP tool definition.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func schema(props map[string]any, required ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

var confirmProp = str("the confirmation code of this tool's preview; give it only after the owner agreed to the previewed effect")

// Tools lists the server's tools.
func (s *Server) Tools() []Tool {
	t := []Tool{
		{"factory_status", "Company status: projects and their pauses with reasons, nodes with disk, drivers and logins, and the problems the company doctor finds.", schema(nil)},
		{"factory_doctor", "The company doctor: nodes, disk, drivers and logins, failed runs by cause, stuck tasks and pending owner items, each with its fix and runbook.", schema(map[string]any{"since": str("period of failed runs, e.g. 24h (default) or 7d as 168h")})},
		{"factory_why", "Why a task or a chat waits or has no answer, in words, with its fix and runbook. Give task, or agent and project (the agent's chat in the project).", schema(map[string]any{"task": str("task ID"), "agent": str("agent name, for its chat"), "project": str("project ID of the agent's chat")})},
		{"factory_runs", "Recent runs with their outcome, the cause of a failure and the token usage explained.", schema(map[string]any{"failed": map[string]any{"type": "boolean", "description": "only failed runs"}, "since": str("period, e.g. 24h"), "limit": map[string]any{"type": "integer"}})},
		{"factory_catalog_validate", "Validate catalog documents against the stored catalog without saving them; each error comes with its fix. Without documents it checks the stored catalog loads.", schema(map[string]any{"documents": map[string]any{"type": "object", "description": "catalog path → full document content", "additionalProperties": map[string]any{"type": "string"}}, "delete": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}})},
		{"factory_agent", "What an agent really gets on a run: its composed instructions (prompt, policies, skills list, hierarchy), its skills with sources and pinned hashes, its runtime and model.", schema(map[string]any{"agent": str("agent name")}, "agent")},
		{"factory_runbook", "A runbook of the factory-setup skill (no name: the index). Runbooks: " + strings.Join(support.Runbooks, ", ") + ".", schema(map[string]any{"name": str("runbook name")})},
		{"factory_pause", "Pause a project, agent, node or the whole company (scope company). Preview first; runs only with the confirmation code after the owner agreed.", schema(map[string]any{"scope": str("project, agent, node or company"), "target": str("its ID (not for company)"), "reason": str("why, shown to everyone"), "confirm": confirmProp}, "scope")},
		{"factory_resume", "Resume a paused project, agent, node, quota key or the company. The preview says what starts running; runs only with the confirmation code after the owner agreed.", schema(map[string]any{"scope": str("project, agent, node, quota_key or company"), "target": str("its ID (not for company)"), "confirm": confirmProp}, "scope")},
		{"factory_node_enrollment", "Issue a one-time enrollment code for a new machine (factory-node enroll CODE). Preview first.", schema(map[string]any{"name": str("the new node's name"), "confirm": confirmProp})},
		{"factory_node_approve", "Approve a node's keys after comparing the fingerprint the machine shows (factory-node enroll prints it). Preview first.", schema(map[string]any{"node": str("node ID waiting for approval"), "fingerprint": str("the fingerprint shown on the machine"), "confirm": confirmProp}, "node", "fingerprint")},
		{"factory_secret_set", "Set a company secret from a local file (preferred) or a value. The value is never shown in any answer. Preview first.", schema(map[string]any{"name": str("secret name"), "file": str("local file holding the value"), "value": str("the value, if no file"), "kind": str("string (default) or file"), "confirm": confirmProp}, "name")},
		{"factory_skill_install", "Install a library skill for the company, a team or an agent. Preview first.", schema(map[string]any{"source": str("skill source, e.g. factory"), "name": str("skill name"), "scope": str("'' (every agent), team:NAME or agent:NAME"), "confirm": confirmProp}, "source", "name")},
	}
	if s.Local.NodeDoctor != nil {
		t = append(t, Tool{"factory_node_doctor", "Run factory-node doctor on this machine (read-only): git, forge, disk, network (net.dns, net.api, net.forge), runtimes and logins, each with its fix.", schema(nil)})
	}
	if s.Local.NodeLog != nil {
		t = append(t, Tool{"factory_node_log", "The last lines of this machine's node log (read-only; credentials redacted).", schema(map[string]any{"lines": map[string]any{"type": "integer", "description": "default 200, at most 2000"}})})
	}
	return t
}

// Call runs one tool.
func (s *Server) Call(ctx context.Context, c factorytools.Call) (any, error) {
	a := args(c.Arguments)
	switch c.Name {
	case "factory_status":
		return s.status(ctx)
	case "factory_doctor":
		return s.doctor(ctx, a.str("since"))
	case "factory_why":
		return s.why(ctx, a)
	case "factory_runs":
		return s.runs(ctx, a)
	case "factory_catalog_validate":
		return s.validate(ctx, a)
	case "factory_agent":
		return s.agent(ctx, a.str("agent"))
	case "factory_runbook":
		return s.runbook(ctx, a.str("name"))
	case "factory_node_doctor":
		if s.Local.NodeDoctor == nil {
			break
		}
		out, ok, err := s.Local.NodeDoctor(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"passed": ok, "output": scrub(out), "hint": "each failing check has its fix; network failures: runbook " + support.RunbookDNS}, nil
	case "factory_node_log":
		if s.Local.NodeLog == nil {
			break
		}
		n := a.int("lines", 200)
		out, err := s.Local.NodeLog(ctx, min(max(n, 1), 2000))
		if err != nil {
			return nil, err
		}
		return map[string]any{"log": scrub(out)}, nil
	case "factory_pause", "factory_resume", "factory_node_enrollment", "factory_node_approve", "factory_secret_set", "factory_skill_install":
		return s.change(ctx, c.Name, a)
	}
	return nil, fmt.Errorf("unknown tool %s", c.Name)
}

// scrub removes credential shapes from local output (node logs, doctor output).
func scrub(s string) string {
	b, _ := redact.New().Bytes([]byte(s))
	return string(b)
}

type args map[string]any

func (a args) str(k string) string {
	v, _ := a[k].(string)
	return strings.TrimSpace(v)
}

func (a args) int(k string, def int) int {
	switch v := a[k].(type) {
	case float64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func (a args) bool(k string) bool {
	v, _ := a[k].(bool)
	return v
}

// ---- read tools ----

func (s *Server) doctor(ctx context.Context, since string) (any, error) {
	var d support.CompanyDoctor
	path := "/doctor"
	if since != "" {
		path += "?since=" + url.QueryEscape(since)
	}
	if err := s.Core.Do(ctx, "GET", path, nil, &d); err != nil {
		return nil, err
	}
	return map[string]any{"report": d.Text(), "doctor": d}, nil
}

func (s *Server) status(ctx context.Context) (any, error) {
	var projects []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Team string `json:"team,omitempty"`
	}
	if err := s.Core.Do(ctx, "GET", "/projects", nil, &projects); err != nil {
		return nil, err
	}
	var pauses []support.DoctorPause
	if err := s.Core.Do(ctx, "GET", "/pauses", nil, &pauses); err != nil {
		return nil, err
	}
	var d support.CompanyDoctor
	if err := s.Core.Do(ctx, "GET", "/doctor", nil, &d); err != nil {
		return nil, err
	}
	type project struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Team   string `json:"team,omitempty"`
		Paused string `json:"paused,omitempty"`
	}
	ps := []project{}
	companyPause := ""
	for _, p := range pauses {
		if p.Scope == "company" {
			companyPause = "company paused" + colon(p.Reason)
		}
	}
	for _, p := range projects {
		v := project{ID: p.ID, Name: p.Name, Team: p.Team, Paused: companyPause}
		for _, x := range pauses {
			if x.Scope == "project" && x.Target == p.ID {
				v.Paused = "paused" + colon(x.Reason) + " (factory resume --project " + p.ID + ")"
			}
		}
		ps = append(ps, v)
	}
	type node struct {
		ID      string   `json:"id"`
		Name    string   `json:"name,omitempty"`
		State   string   `json:"state"`
		Disk    string   `json:"disk,omitempty"`
		Drivers []string `json:"drivers"`
	}
	ns := []node{}
	for _, n := range d.Nodes {
		v := node{ID: n.ID, Name: n.Name, State: "online", Drivers: []string{}}
		switch {
		case !n.Online:
			v.State = "offline"
		case n.Paused:
			v.State = "paused" + colon(n.PauseReason)
		}
		if n.Disk != "" {
			v.Disk = n.Disk
			if n.FreeBytes > 0 {
				v.Disk += fmt.Sprintf(", %.1f GiB free", float64(n.FreeBytes)/(1<<30))
			}
		}
		for _, dr := range n.Drivers {
			if dr.OK {
				v.Drivers = append(v.Drivers, dr.Name+": usable (logged in)")
			} else {
				v.Drivers = append(v.Drivers, dr.Name+": disabled, "+dr.Check+" "+dr.Detail)
			}
		}
		ns = append(ns, v)
	}
	return map[string]any{"company": d.Company, "ok": d.OK, "projects": ps, "pauses": pauses, "nodes": ns, "problems": d.Items, "pending": d.Pending}, nil
}

func colon(s string) string {
	if s == "" {
		return ""
	}
	return ": " + s
}

func (s *Server) why(ctx context.Context, a args) (any, error) {
	task := a.str("task")
	if task == "" {
		agent, project := a.str("agent"), a.str("project")
		if agent == "" || project == "" {
			return nil, errors.New("give task, or agent and project")
		}
		task = storage.ConversationID(project, agent)
	}
	var raw json.RawMessage
	if err := s.Core.Do(ctx, "GET", "/tasks/"+url.PathEscape(task)+"/why", nil, &raw); err != nil {
		return nil, err
	}
	var w struct {
		tasks.Verdict
		Queued *support.QueuedWhy `json:"queued,omitempty"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	answer := support.Waiting(w.Verdict, w.Queued)
	out := map[string]any{"task": task, "status": w.Status, "reason": w.Reason, "answer": answer, "why": raw}
	if answer.Runbook != "" {
		out["runbook"] = support.RunbookRef(answer.Runbook) + " (factory_runbook name=" + answer.Runbook + ")"
	}
	return out, nil
}

func (s *Server) runs(ctx context.Context, a args) (any, error) {
	q := url.Values{}
	if a.bool("failed") {
		q.Set("failed", "1")
	}
	if v := a.str("since"); v != "" {
		q.Set("since", v)
	}
	q.Set("limit", strconv.Itoa(min(max(a.int("limit", 20), 1), 200)))
	var v []map[string]any
	if err := s.Core.Do(ctx, "GET", "/runs/recent?"+q.Encode(), nil, &v); err != nil {
		return nil, err
	}
	for _, r := range v {
		if res, ok := r["result"].(string); ok && len(res) > 600 {
			r["result"] = res[:600] + "…"
		}
	}
	return map[string]any{"runs": v, "note": "usage_explained says what the token count is made of; runbook " + support.RunbookTokenUsage}, nil
}

func (s *Server) agent(ctx context.Context, name string) (any, error) {
	if name == "" {
		return nil, errors.New("agent required")
	}
	var v any
	if err := s.Core.Do(ctx, "GET", "/agents/"+url.PathEscape(name)+"/effective", nil, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func (s *Server) runbook(ctx context.Context, name string) (any, error) {
	file := "SKILL.md"
	if name != "" {
		ok := false
		for _, r := range support.Runbooks {
			ok = ok || r == name
		}
		if !ok {
			return nil, fmt.Errorf("unknown runbook %q (known: %s)", name, strings.Join(support.Runbooks, ", "))
		}
		file = "references/" + name + ".md"
	}
	rc, err := s.Core.Get(ctx, "/skills/library/factory/"+support.Skill+"/files/"+file)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, 256<<10))
	if err != nil {
		return nil, err
	}
	return map[string]any{"runbook": name, "text": string(b)}, nil
}

// validate dry-runs a catalog change on top of the current version.
func (s *Server) validate(ctx context.Context, a args) (any, error) {
	var cur struct {
		Version int64 `json:"version"`
	}
	if err := s.Core.Do(ctx, "GET", "/catalog", nil, &cur); err != nil {
		return nil, err
	}
	put := map[string]string{}
	if docs, ok := a["documents"].(map[string]any); ok {
		for p, v := range docs {
			if c, ok := v.(string); ok {
				put[p] = c
			}
		}
	}
	var del []string
	if ds, ok := a["delete"].([]any); ok {
		for _, d := range ds {
			if p, ok := d.(string); ok {
				del = append(del, p)
			}
		}
	}
	if len(put) == 0 && len(del) == 0 {
		return map[string]any{"valid": true, "version": cur.Version, "note": "the stored catalog is valid: Core refuses a version that does not load. Pass documents to check a change."}, nil
	}
	body := map[string]any{"base": cur.Version, "put": put, "delete": del, "dry_run": true}
	var v any
	if err := s.Core.Do(ctx, "POST", "/catalog", body, &v); err != nil {
		msg := err.Error()
		return map[string]any{"valid": false, "errors": []map[string]string{{"error": msg, "fix": CatalogFix(msg)}}}, nil
	}
	return map[string]any{"valid": true, "base": cur.Version, "result": v}, nil
}

// CatalogFix is the fix of a catalog loader error.
func CatalogFix(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "field") && strings.Contains(m, "not found in type"):
		return "remove or rename the key the error names: the document has a key the format does not know (docs/catalog.md lists each kind's keys)"
	case strings.Contains(m, "yaml:") && (strings.Contains(m, "line") || strings.Contains(m, "mapping")):
		return "fix the YAML syntax at the line the error names (indentation, a missing colon, a tab)"
	case strings.Contains(m, "prompt") && (strings.Contains(m, "not found") || strings.Contains(m, "no such file")):
		return "add the prompt document at the path the agent names (relative to the catalog root, e.g. prompts/NAME.md) or fix spec.prompt.file"
	case strings.Contains(m, "no such file") || strings.Contains(m, "not found"):
		return "add the document the error names to the change, or fix the reference to it"
	case strings.Contains(m, "unknown"):
		return "define the entity the error names (agent, team, profile, policy, skill) or fix the name that refers to it"
	case strings.Contains(m, "duplicate"):
		return "keep one definition: two documents define the same name"
	case strings.Contains(m, "conflict") || strings.Contains(m, "base"):
		return "the catalog changed meanwhile: validate again (the tool reads the current version each time)"
	}
	return "change the document as the error says; docs/catalog.md describes each kind"
}

// ---- change tools ----

// plan is a change: its effect in words and how to apply it.
type plan struct {
	Effect string
	Apply  func(ctx context.Context) (any, error)
}

func (s *Server) change(ctx context.Context, name string, a args) (any, error) {
	code := a.str("confirm")
	p, err := s.plan(ctx, name, a)
	if err != nil {
		return nil, err
	}
	digest := s.digest(name, a)
	if code == "" {
		return map[string]any{"preview": p.Effect, "confirm": s.issue(digest),
			"next": "Show the owner this effect. Only if they agree, call " + name + " again with the same arguments and confirm set to this code (valid " + s.ttl().String() + ")."}, nil
	}
	if err = s.redeem(code, digest); err != nil {
		return nil, err
	}
	v, err := p.Apply(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"done": p.Effect, "result": v}, nil
}

func (s *Server) ttl() time.Duration {
	if s.ConfirmTTL > 0 {
		return s.ConfirmTTL
	}
	return 10 * time.Minute
}

// digest binds a confirmation to the tool and its exact arguments (without the code).
func (s *Server) digest(name string, a args) string {
	keys := make([]string, 0, len(a))
	for k := range a {
		if k != "confirm" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	h := sha256.New()
	h.Write([]byte(name))
	for _, k := range keys {
		b, _ := json.Marshal(a[k])
		h.Write([]byte{0})
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Server) mac(digest string, exp int64) string {
	s.once.Do(func() {
		s.key = make([]byte, 32)
		_, _ = rand.Read(s.key)
		s.used = map[string]bool{}
	})
	m := hmac.New(sha256.New, s.key)
	fmt.Fprintf(m, "%s.%d", digest, exp)
	return hex.EncodeToString(m.Sum(nil))[:16]
}

// issue is a confirmation code: its expiry and the MAC over the digest.
func (s *Server) issue(digest string) string {
	exp := s.now().Add(s.ttl()).Unix()
	return strconv.FormatInt(exp, 36) + "-" + s.mac(digest, exp)
}

// errConfirm: a code that does not belong to these arguments, expired or was used.
var errConfirm = errors.New("confirmation code invalid for these arguments, expired or already used: call without confirm for a new preview and ask the owner again")

func (s *Server) redeem(code, digest string) error {
	e, m, ok := strings.Cut(code, "-")
	exp, err := strconv.ParseInt(e, 36, 64)
	if !ok || err != nil || s.now().Unix() > exp || !hmac.Equal([]byte(m), []byte(s.mac(digest, exp))) {
		return errConfirm
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.used[code] {
		return errConfirm
	}
	s.used[code] = true
	return nil
}

func (s *Server) plan(ctx context.Context, name string, a args) (plan, error) {
	switch name {
	case "factory_pause":
		return s.pausePlan(ctx, a)
	case "factory_resume":
		return s.resumePlan(ctx, a)
	case "factory_node_enrollment":
		n := a.str("name")
		return plan{Effect: "Issues a one-time enrollment code" + colon(n) + ". Whoever has the code can enroll a machine until it expires or is used; the node then still needs your approval of its fingerprint.",
			Apply: func(ctx context.Context) (any, error) {
				var v any
				err := s.Core.Do(ctx, "POST", "/node-enrollments", map[string]string{"name": n}, &v)
				return v, err
			}}, nil
	case "factory_node_approve":
		return s.approvePlan(ctx, a)
	case "factory_secret_set":
		return s.secretPlan(ctx, a)
	case "factory_skill_install":
		src, n, scope := a.str("source"), a.str("name"), a.str("scope")
		if src == "" || n == "" {
			return plan{}, errors.New("source and name required")
		}
		who := "every agent of the company"
		if scope != "" {
			who = scope
		}
		return plan{Effect: fmt.Sprintf("Installs skill %s from source %s for %s: their next runs get it (a new catalog version).", n, src, who),
			Apply: func(ctx context.Context) (any, error) {
				var cur struct {
					Version int64 `json:"version"`
				}
				if err := s.Core.Do(ctx, "GET", "/catalog", nil, &cur); err != nil {
					return nil, err
				}
				var v any
				err := s.Core.Do(ctx, "POST", "/skills/install", map[string]any{"base": cur.Version, "source": src, "name": n, "scope": scope}, &v)
				return v, err
			}}, nil
	}
	return plan{}, fmt.Errorf("unknown change %s", name)
}

func scopeTarget(a args, scopes ...string) (string, string, error) {
	scope, target := a.str("scope"), a.str("target")
	ok := false
	for _, x := range scopes {
		ok = ok || x == scope
	}
	if !ok {
		return "", "", fmt.Errorf("scope: one of %s", strings.Join(scopes, ", "))
	}
	if scope == "company" {
		target = ""
	} else if target == "" {
		return "", "", errors.New("target required for scope " + scope)
	}
	return scope, target, nil
}

func (s *Server) pausePlan(ctx context.Context, a args) (plan, error) {
	scope, target, err := scopeTarget(a, "project", "agent", "node", "company")
	if err != nil {
		return plan{}, err
	}
	reason := a.str("reason")
	what := scope + " " + target
	if scope == "company" {
		what = "the whole company"
	}
	effect := "Pauses " + what + colon(reason) + ": nothing new starts there until it is resumed; runs already running finish their turn."
	if scope == "node" {
		effect = "Pauses node " + target + colon(reason) + ": it claims no run of any kind; runs tied to it (its sessions, unsaved work) wait for it or must be released."
	}
	return plan{Effect: effect, Apply: func(ctx context.Context) (any, error) {
		var v any
		if scope == "node" {
			err := s.Core.Do(ctx, "POST", "/nodes/"+url.PathEscape(target)+"/pause", map[string]string{"reason": reason}, &v)
			return v, err
		}
		err := s.Core.Do(ctx, "POST", "/pauses", map[string]string{"scope": scope, "target": target, "reason": reason}, &v)
		return v, err
	}}, nil
}

// resumePlan says what starts running once the pause ends: the tasks the pause holds back (their verdict names it)
// and, for a node, the runs that wait for it.
func (s *Server) resumePlan(ctx context.Context, a args) (plan, error) {
	scope, target, err := scopeTarget(a, "project", "agent", "node", "quota_key", "company")
	if err != nil {
		return plan{}, err
	}
	var pauses []support.DoctorPause
	if err = s.Core.Do(ctx, "GET", "/pauses", nil, &pauses); err != nil {
		return plan{}, err
	}
	var held *support.DoctorPause
	for i, p := range pauses {
		if p.Scope == scope && p.Target == target {
			held = &pauses[i]
		}
	}
	what := scope + " " + target
	if scope == "company" {
		what = "the company"
	}
	if held == nil {
		return plan{}, fmt.Errorf("%s is not paused (factory_status lists the pauses)", what)
	}
	effect := "Resumes " + what + " (paused" + colon(held.Reason) + ")."
	var starts []string
	switch scope {
	case "project":
		var vs []tasks.Verdict
		if err = s.Core.Do(ctx, "GET", "/projects/"+url.PathEscape(target)+"/why", nil, &vs); err != nil {
			return plan{}, err
		}
		for _, v := range vs {
			if v.Reason == tasks.ReasonPaused+"project:"+target {
				starts = append(starts, v.TaskID)
			}
		}
	case "node":
		var nodes []struct {
			NodeID string `json:"node_id"`
			Tied   struct {
				Waiting []struct {
					RunID  string `json:"run_id"`
					TaskID string `json:"task_id"`
				} `json:"waiting"`
			} `json:"tied"`
		}
		if err = s.Core.Do(ctx, "GET", "/nodes", nil, &nodes); err != nil {
			return plan{}, err
		}
		for _, n := range nodes {
			if n.NodeID == target {
				for _, w := range n.Tied.Waiting {
					starts = append(starts, cmpOr(w.TaskID, w.RunID))
				}
			}
		}
		effect += " The node claims runs again."
	}
	if len(starts) > 0 {
		effect += fmt.Sprintf(" These can start running now: %s.", strings.Join(starts[:min(len(starts), 20)], ", "))
		if len(starts) > 20 {
			effect += fmt.Sprintf(" (and %d more)", len(starts)-20)
		}
	} else if scope == "project" || scope == "node" {
		effect += " No task waits only for this pause now; new work starts as it comes."
	}
	return plan{Effect: effect, Apply: func(ctx context.Context) (any, error) {
		var v any
		err := s.Core.Do(ctx, "POST", "/pauses/resume", map[string]string{"scope": scope, "target": target}, &v)
		return v, err
	}}, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (s *Server) approvePlan(ctx context.Context, a args) (plan, error) {
	id, fp := a.str("node"), a.str("fingerprint")
	var keys []struct {
		NodeID          string `json:"node_id"`
		Name            string `json:"name"`
		Fingerprint     string `json:"fingerprint"`
		Status          string `json:"status"`
		NextFingerprint string `json:"next_fingerprint,omitempty"`
	}
	if err := s.Core.Do(ctx, "GET", "/node-keys", nil, &keys); err != nil {
		return plan{}, err
	}
	for _, k := range keys {
		if k.NodeID != id {
			continue
		}
		want := k.Fingerprint
		if k.NextFingerprint != "" {
			want = k.NextFingerprint
		}
		if !strings.EqualFold(strings.ReplaceAll(want, ":", ""), strings.ReplaceAll(fp, ":", "")) {
			return plan{}, fmt.Errorf("fingerprint %s does not match the one Core holds for %s (%s): do not approve; enroll the machine again", fp, id, want)
		}
		return plan{Effect: fmt.Sprintf("Approves node %s (%s) with fingerprint %s: it may then register and claim runs of this company.", id, cmpOr(k.Name, id), want),
			Apply: func(ctx context.Context) (any, error) {
				var v any
				err := s.Core.Do(ctx, "POST", "/node-keys/"+url.PathEscape(id)+"/approve", map[string]string{"fingerprint": fp}, &v)
				return v, err
			}}, nil
	}
	return plan{}, fmt.Errorf("no node %s waits for approval", id)
}

func (s *Server) secretPlan(ctx context.Context, a args) (plan, error) {
	name, file, value, kind := a.str("name"), a.str("file"), a["value"], a.str("kind")
	if name == "" {
		return plan{}, errors.New("name required")
	}
	if kind == "" {
		kind = "string"
	}
	var src string
	var read func() ([]byte, error)
	switch v, _ := value.(string); {
	case file != "":
		if s.Local.ReadFile == nil {
			return plan{}, errors.New("reading local files is not available")
		}
		src = "the contents of " + file
		read = func() ([]byte, error) { return s.Local.ReadFile(file) }
		if _, err := read(); err != nil {
			return plan{}, fmt.Errorf("%s: %w", file, err)
		}
	case v != "":
		src = "the value given (not shown)"
		read = func() ([]byte, error) { return []byte(v), nil }
	default:
		return plan{}, errors.New("file or value required")
	}
	var list []struct {
		Name   string   `json:"name"`
		UsedBy []string `json:"used_by"`
	}
	if err := s.Core.Do(ctx, "GET", "/secrets", nil, &list); err != nil {
		return plan{}, err
	}
	verb, users := "Creates", ""
	for _, x := range list {
		if x.Name == name {
			verb = "Replaces"
			if len(x.UsedBy) > 0 {
				users = "; used by " + strings.Join(x.UsedBy, ", ") + " (their next runs get the new value)"
			}
		}
	}
	return plan{Effect: fmt.Sprintf("%s company secret %s (%s) with %s%s. The value never appears in any answer.", verb, name, kind, src, users),
		Apply: func(ctx context.Context) (any, error) {
			b, err := read()
			if err != nil {
				return nil, err
			}
			v := string(b)
			if kind == "file" {
				v = base64.StdEncoding.EncodeToString(b)
			} else {
				v = strings.TrimRight(v, "\r\n")
			}
			var out struct {
				Name    string `json:"name"`
				Kind    string `json:"kind"`
				Version int64  `json:"version"`
			}
			if err = s.Core.Do(ctx, "PUT", "/secrets/"+url.PathEscape(name), map[string]string{"kind": kind, "value": v}, &out); err != nil {
				msg := err.Error()
				if len(v) >= 4 {
					msg = strings.ReplaceAll(msg, v, "[value]")
				}
				return nil, errors.New(msg)
			}
			return map[string]any{"name": out.Name, "kind": out.Kind, "version": out.Version}, nil
		}}, nil
}

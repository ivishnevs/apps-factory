package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/storage"
)

// Explicit exits, Core side of docs/explicit-exits-contract.md. Behind Core.ExplicitExits
// (FACTORY_EXPLICIT_EXITS=1): while it is off, agents keep today's tools and implicit submission.

const (
	ackHeader  = "X-Factory-Ack-Seq"
	seenHeader = "X-Factory-Seen-Seq"
)

func exitPath(path string) bool {
	return path == "/agent/exit" || path == "/agent/exit/preflight" || path == "/agent/brief"
}

// agentTools is the run's tool list: with explicit exits the exit tools replace submit/accept/reject, and
// the directive tool is listed for managers only.
func (c *Core) agentTools(agent config.Agent, allowed func(factorytools.Tool) bool) []factorytools.Tool {
	out := []factorytools.Tool{}
	for _, t := range factorytools.Definitions() {
		if c.ExplicitExits && factorytools.ReplacedByExits[t.Name] {
			continue
		}
		if allowed(t) {
			out = append(out, t)
		}
	}
	if !c.ExplicitExits {
		return out
	}
	for _, t := range factorytools.ExitDefinitions() {
		if t.Name == "factory_task_direct" && agent.Spec.Role != "manager" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// ackSeq is the adapter's ack_seq from the request header, else the run's brief end (an adapter that does
// not track sequences has seen its brief).
func (c *Core) ackSeq(r *http.Request, run storage.Run) (int64, bool) {
	if v := r.Header.Get(ackHeader); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n, true
		}
	}
	n, _, _ := c.Store.BriefEnd(r.Context(), c.companyFrom(r), run.Spec.ID)
	return n, false
}

// exitRoute serves /agent/exit, /agent/exit/preflight and /agent/brief for an authenticated run.
func (c *Core) exitRoute(w http.ResponseWriter, r *http.Request, run storage.Run) {
	company := c.companyFrom(r)
	if r.URL.Path == "/agent/brief" {
		if r.Method != "GET" {
			http.Error(w, "not found", 404)
			return
		}
		to, task, err := c.Store.BriefEnd(r.Context(), company, run.Spec.ID)
		reply(w, map[string]any{"ack_seq": to, "task_id": task}, err)
		return
	}
	if r.Method != "POST" || !c.ExplicitExits {
		http.Error(w, "not found", 404)
		return
	}
	var p storage.ExitRequest
	if !decode(w, r, &p) {
		return
	}
	p.Preflight = r.URL.Path == "/agent/exit/preflight"
	c.approvalMu.Lock() // serialized with tool calls, like /agent/call
	defer c.approvalMu.Unlock()
	c.serveExit(w, r, run, p)
}

// serveExit runs under approvalMu.
func (c *Core) serveExit(w http.ResponseWriter, r *http.Request, run storage.Run, p storage.ExitRequest) {
	p.Known = func(agent string) bool { _, ok := c.Agents[agent]; return ok }
	v, err := c.Store.Exit(r.Context(), c.companyFrom(r), run.Spec.ID, p)
	var refusal *storage.ExitRefusal
	if errors.As(err, &refusal) {
		jsonStatus(w, refusal.Status, refusal)
		return
	}
	reply(w, v, err)
}

// exitCall serves an exit tool called through /agent/call (an adapter without exit facts; the gateway
// holds approvalMu): the exit is sent without facts, with the adapter's ack_seq header or the brief end.
func (c *Core) exitCall(w http.ResponseWriter, r *http.Request, p factorytools.Call, run storage.Run) {
	ack, _ := c.ackSeq(r, run)
	c.serveExit(w, r, run, storage.ExitRequest{Kind: factorytools.ExitKind(p.Name), Arguments: p.Arguments, AckSeq: ack})
}

// directiveCall serves factory_task_direct: a manager's directive to a task of the run's project.
func (c *Core) directiveCall(w http.ResponseWriter, r *http.Request, p factorytools.Call, run storage.Run, current config.Agent) {
	if current.Spec.Role != "manager" {
		http.Error(w, "directives are for managers", 403)
		return
	}
	task, _ := p.Arguments["task_id"].(string)
	t, err := c.Store.Task(r.Context(), c.companyFrom(r), task)
	if err != nil {
		reply(w, nil, err)
		return
	}
	if t.ProjectID != run.Spec.Project {
		http.Error(w, "task outside run project", 403)
		return
	}
	text, _ := p.Arguments["text"].(string)
	urgent, _ := p.Arguments["urgent"].(bool)
	key, _ := p.Arguments["request_id"].(string)
	v, err := c.Store.Directive(r.Context(), c.companyFrom(r), task, run.Spec.Agent, text, urgent, run.Spec.Agent+":"+key)
	reply(w, v, err)
}

// withAuthorityInput wraps a successful JSON tool result with the authority input the agent has not seen
// (contract §4) and tells the adapter up to which seq the response shows.
func (c *Core) withAuthorityInput(r *http.Request, run storage.Run, response *toolResponse) {
	if !c.ExplicitExits || run.Spec.TaskID == "" || response.status/100 != 2 {
		return
	}
	ack, tracked := c.ackSeq(r, run)
	if !tracked {
		return
	}
	input, err := c.Store.AuthorityInputSince(r.Context(), c.companyFrom(r), run.Spec.TaskID, ack)
	if err != nil || len(input) == 0 {
		return
	}
	var result any
	if json.Unmarshal(response.body.Bytes(), &result) != nil {
		return
	}
	winning := input[0]
	for _, a := range input[1:] {
		if a.Rank <= winning.Rank { // the smallest rank wins; later input of the same rank overrides
			winning = a
		}
	}
	b, err := json.Marshal(map[string]any{"result": result, "authority_input": input, "winning": winning.Seq})
	if err != nil {
		return
	}
	response.body = *bytes.NewBuffer(b)
	response.header.Set("Content-Type", "application/json")
	response.header.Set(seenHeader, strconv.FormatInt(input[len(input)-1].Seq, 10))
}

// directiveRoute is the owner's directive (factory direct TASK TEXT [--urgent]).
func (c *Core) directiveRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /tasks/{id}/directives", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Text      string `json:"text"`
			Urgent    bool   `json:"urgent,omitempty"`
			RequestID string `json:"request_id"`
		}
		if !decode(w, r, &p) {
			return
		}
		v, err := c.Store.Directive(r.Context(), c.companyFrom(r), r.PathValue("id"), "owner", p.Text, p.Urgent, p.RequestID)
		reply(w, v, err)
	})
}

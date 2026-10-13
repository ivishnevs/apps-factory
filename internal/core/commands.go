package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"

	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/policies"
	"appfactory/internal/storage"
)

// Command guards, Core side (node-agent 4.1–4.2): factory-shim asks POST /agent/command with the run
// token before running a guarded command; owners grant one exact argv once through approvals.

const commandApprovalHint = "Request factory_owner_request_approval with call {\"name\":\"command\",\"arguments\":{\"argv\":[...]}} and finish the turn; an APPROVED grant runs exactly this command once."

// commandDecision combines the captured and the current agent: deny > approval > allow.
func commandDecision(argv []string, agents ...config.Agent) policies.CommandDecision {
	rank := map[string]int{"allow": 0, "approval": 1, "deny": 2}
	d := policies.CommandDecision{Effect: "allow"}
	for _, a := range agents {
		if x := a.AuthorizeCommand(argv); rank[x.Effect] > rank[d.Effect] {
			d = x
		}
	}
	return d
}

// validArgv: non-empty strings, argv[0] a bare command name.
func validArgv(argv []string) bool {
	if len(argv) == 0 || argv[0] != filepath.Base(argv[0]) || strings.ContainsRune(argv[0], '/') {
		return false
	}
	for _, s := range argv {
		if s == "" {
			return false
		}
	}
	return true
}

type commandAnswer struct {
	Effect      string `json:"effect"`
	Rule        string `json:"rule,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	ApprovalID  string `json:"approval_id,omitempty"`
}

func writeStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (c *Core) commandCheck(w http.ResponseWriter, r *http.Request, run storage.Run, snapshot, current config.Agent) {
	var p struct {
		Argv []string `json:"argv"`
	}
	if !decode(w, r, &p) {
		return
	}
	if !validArgv(p.Argv) {
		http.Error(w, "argv: non-empty strings, argv[0] a command name", 400)
		return
	}
	ctx, company := r.Context(), c.companyFrom(r)
	d := commandDecision(p.Argv, snapshot, current)
	refuse := func(status int, a commandAnswer) {
		if err := c.Store.RecordCommandRefusal(ctx, company, run.Spec.ID, p.Argv, a.Effect, a.Rule); err != nil {
			reply(w, nil, err)
			return
		}
		writeStatus(w, status, a)
	}
	switch d.Effect {
	case "deny":
		refuse(http.StatusForbidden, commandAnswer{Effect: "deny", Rule: d.Rule, Instruction: d.Instruction})
	case "approval":
		c.approvalMu.Lock()
		defer c.approvalMu.Unlock()
		grant, err := c.Store.UseCommandGrant(ctx, company, run.Spec.ID, p.Argv)
		switch {
		case err == nil:
			writeStatus(w, http.StatusOK, commandAnswer{Effect: "allow", Rule: d.Rule, ApprovalID: grant.ID})
		case errors.Is(err, storage.ErrNotFound):
			refuse(http.StatusLocked, commandAnswer{Effect: "approval", Rule: d.Rule, Instruction: strings.TrimSpace(d.Instruction + " " + commandApprovalHint)})
		default:
			reply(w, nil, err)
		}
	default:
		writeStatus(w, http.StatusOK, commandAnswer{Effect: "allow"})
	}
}

// commandApprovalCall validates call {"name":"command","arguments":{"argv":[...]}} for a grant request;
// it answers the error itself and returns false when the request must not proceed.
func (c *Core) commandApprovalCall(w http.ResponseWriter, call factorytools.Call, snapshot, current config.Agent) bool {
	argv := storage.CommandArgv(call.Arguments)
	if len(call.Arguments) != 1 || !validArgv(argv) {
		http.Error(w, "command approval needs exactly arguments {\"argv\": [non-empty strings]}", 400)
		return false
	}
	switch commandDecision(argv, snapshot, current).Effect {
	case "deny":
		http.Error(w, "policy denies command", 403)
		return false
	case "allow":
		http.Error(w, "command does not require approval", 400)
		return false
	}
	return true
}

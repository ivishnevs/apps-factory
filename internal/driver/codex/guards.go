package codex

import (
	"context"
	"encoding/json"

	"appfactory/internal/node/shim"
	"appfactory/internal/policies"
)

// guards maps app-server approval requests onto the agent's command rules (runtime-drivers D2) with
// shim.Guard: deny → decline with the rule's instruction, approval → accept only with Core's single-use
// exact grant, allow → accept. The PATH shims stay in place; a grant consumed here is handed to the shim.
type guards struct{ *shim.Guard }

func newGuards(rules []policies.Rule, env []string) *guards {
	return &guards{shim.NewGuard(rules, env)}
}

// active means the thread asks for approvals: some rule denies or gates a command.
func (g *guards) active() bool { return g.Active() }

// decide answers an approval request; note, when set, tells the agent why it was declined.
func (g *guards) decide(ctx context.Context, method string, params json.RawMessage) (decision, note string) {
	if method != "item/commandExecution/requestApproval" {
		return "accept", "" // file changes: command rules do not cover them
	}
	var p struct {
		Command *string `json:"command"`
		Kind    string  `json:"kind"`
	}
	if json.Unmarshal(params, &p) != nil {
		return "decline", "Factory could not read the command approval request."
	}
	if p.Kind == "writeStdin" || p.Command == nil {
		return "accept", "" // input to a terminal whose command was already decided
	}
	if ok, note := g.Line(ctx, *p.Command); !ok {
		return "decline", note
	}
	return "accept", ""
}

package core

import (
	"context"
	"strings"
	"testing"

	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// TestHandedUpApprovalDecide (people-topics D18): the approval setting decides where an approval goes first and its
// ceiling; after that it moves along the hierarchy and the agent holding it decides. A strict delegate hands an
// approval up to its manager, who approves, rejects or chooses it; a change from one agent mode to another leaves it
// with its holder; people only refuses every agent decision but still lets the holder escalate.
func TestHandedUpApprovalDecide(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	delegate := h.c.Agents["demo"]
	delegate.Spec.Communication.CanMessage = []string{"manager"}
	h.c.Agents["demo"] = delegate
	manager := h.c.escalationTargetIn(co, "demo")
	if manager == "" || manager == "owner" {
		t.Fatalf("fixture: demo has no agent manager (%q)", manager)
	}
	setMode := func(m tasks.ApprovalMode) {
		t.Helper()
		p, _ := h.s.Project(ctx, co, "notes")
		modes := tasks.ApprovalModes{InternalArtifacts: m}
		rules := "Dark theme only."
		if _, err := h.s.UpdateProject(ctx, co, "notes", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, Approvals: &modes, Rules: &rules}); err != nil {
			t.Fatal(err)
		}
	}
	setMode(tasks.ApprovalMode{Mode: tasks.ApproveAgent, Agent: "demo"})
	h.create("draw", "notes", "reviewer")
	// Each run in its own project: a project without a repository runs one task at a time.
	for _, p := range []string{"side", "boss"} {
		if _, err := h.s.CreateProject(ctx, co, tasks.ProjectCreate{ID: p, Name: p, Workspace: "/tmp/exits-" + p}); err != nil {
			t.Fatal(err)
		}
	}
	h.create("check", "side", "demo")
	h.create("lead", "boss", manager)
	requester, holder, boss := h.start("draw"), h.start("check"), h.start("lead")
	n := 0
	// ask requests an approval from the delegate demo, which hands it up to its manager.
	ask := func(options []any) string {
		t.Helper()
		n++
		key := "ask" + string(rune('a'+n))
		args := map[string]any{"reason": "for review", "audience": "internal", "request_id": key}
		if options != nil {
			args["options"] = options
		} else {
			ref := tasks.Artifact{ID: "mock", Version: "v" + string(rune('a'+n)), URI: "https://example.com/mock.png"}
			h.mcp(requester, "factory_artifact_register", map[string]any{"artifact": ref, "request_id": "reg-" + key})
			args["artifact_id"], args["artifact_version"] = ref.ID, ref.Version
		}
		a := h.mcp(requester, "factory_artifact_request_approval", args)
		if a["delegate"] != "demo" {
			t.Fatalf("approval %+v", a)
		}
		id := a["id"].(string)
		h.mcp(holder, "factory_approval_decide", map[string]any{"approval_id": id, "action": "escalate", "reason": "no basis", "request_id": "up-" + key})
		if got := h.approvalOf(id); got.Delegate != manager || got.Escalated || !strings.Contains(got.Setting, "handed up by demo") {
			t.Fatalf("not handed up: %+v", got)
		}
		return id
	}
	decide := func(id string, args map[string]any) (int, string) {
		t.Helper()
		args["approval_id"], args["request_id"] = id, "d-"+id+"-"+args["action"].(string)
		if _, ok := args["reason"]; !ok {
			args["reason"] = "fits the brief"
		}
		return h.callCode(boss, "factory_approval_decide", args)
	}

	// Handed up, the manager decides: approve, reject, choose.
	for _, c := range []struct{ action, status string }{{"approve", "APPROVED"}, {"reject", "REJECTED"}} {
		id := ask(nil)
		if code, body := decide(id, map[string]any{"action": c.action}); code != 200 {
			t.Fatal(c.action, code, body)
		}
		if got := h.approvalOf(id); got.Status != c.status || got.DecidedBy != manager {
			t.Fatalf("%s: %+v", c.action, got)
		}
	}
	id := ask(choiceVersions(t, h, "draw", "variations", 2))
	if code, body := decide(id, map[string]any{"action": "choose", "choice": "2", "cites": []any{"project rules: dark theme only"}}); code != 200 {
		t.Fatal(code, body)
	}
	if got := h.approvalOf(id); got.Status != "APPROVED" || got.Chosen != 2 || got.DecidedBy != manager {
		t.Fatalf("choice: %+v", got)
	}

	// The setting moves from one agent to another: the holder still decides.
	id = ask(nil)
	setMode(tasks.ApprovalMode{Mode: tasks.ApproveAgent, Agent: "reviewer"})
	if code, body := decide(id, map[string]any{"action": "approve"}); code != 200 {
		t.Fatal(code, body)
	}
	setMode(tasks.ApprovalMode{Mode: tasks.ApproveAgent, Agent: "demo"})

	// People only is the ceiling: the holder decides nothing, it escalates.
	yes, choice := ask(nil), ask(choiceVersions(t, h, "draw", "icons", 2))
	setMode(tasks.ApprovalMode{Mode: tasks.ApproveOwner})
	for _, c := range []struct {
		id   string
		args map[string]any
	}{
		{yes, map[string]any{"action": "approve"}},
		{yes, map[string]any{"action": "reject"}},
		{choice, map[string]any{"action": "choose", "choice": "1", "cites": []any{"project rules: dark theme only"}}},
		{choice, map[string]any{"action": "reject"}},
	} {
		if code, body := decide(c.id, c.args); code != 403 || !strings.Contains(body, "people decide") {
			t.Fatalf("%v: %d %s", c.args, code, body)
		}
	}
	brief := map[string]any{"title": "Mock review", "ask": "Approve the mock?", "after": "the designer goes on", "recommendation": "approve"}
	for _, x := range []string{yes, choice} {
		if code, body := decide(x, map[string]any{"action": "escalate", "reason": "people decide here", "brief": brief}); code != 200 {
			t.Fatal(code, body)
		}
		if got := h.approvalOf(x); !got.Escalated || got.Status != "PENDING" {
			t.Fatalf("not escalated: %+v", got)
		}
	}

	// A strict requester under people only: its manager receives the approval only to bring it to people.
	p, _ := h.s.Project(ctx, co, "notes")
	if _, err := h.s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "solo", Name: "solo", Workspace: "/tmp/exits-solo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.UpdateProject(ctx, co, "solo", tasks.ProjectUpdate{Version: 1, Name: "solo", Approvals: &p.Approvals}); err != nil {
		t.Fatal(err)
	}
	h.create("own", "solo", "demo")
	h.end(holder, "succeeded", "") // one run per agent at a time
	own := h.start("own")
	ref := tasks.Artifact{ID: "poster", Version: "v1", URI: "https://example.com/poster.png"}
	h.mcp(own, "factory_artifact_register", map[string]any{"artifact": ref, "request_id": "reg-poster"})
	a := h.mcp(own, "factory_artifact_request_approval", map[string]any{"artifact_id": "poster", "artifact_version": "v1", "reason": "poster", "audience": "internal", "request_id": "ask-poster"})
	if a["delegate"] != manager || !strings.Contains(a["setting"].(string), "brings it to them") {
		t.Fatalf("approval %+v", a)
	}
	if code, body := decide(a["id"].(string), map[string]any{"action": "approve"}); code != 403 {
		t.Fatal(code, body)
	}
}

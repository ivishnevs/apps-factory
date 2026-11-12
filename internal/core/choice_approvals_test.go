package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"appfactory/internal/approvals"
	"appfactory/internal/artifacts"
	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// choiceBrief is what people read when a choice reaches them (people-topics D3).
var choiceBrief = map[string]any{"title": "Pick a variation", "ask": "Which variation goes on?", "after": "the designer finishes the chosen one", "recommendation": "the first"}

// choiceVersions registers versions v1..vN of artifact id on task and returns them as request options V1..VN.
func choiceVersions(t *testing.T, h *exitHarness, task, id string, n int) []any {
	t.Helper()
	var out []any
	for i := 1; i <= n; i++ {
		v := "v" + string(rune('0'+i))
		a := tasks.Artifact{ID: id, Version: v, URI: "https://example.com/" + id + "-" + v + ".png"}
		if _, err := h.s.RegisterArtifact(context.Background(), storage.DefaultCompany, "notes", artifacts.Register{Artifact: a, TaskID: task, Actor: "owner", RequestID: "reg-" + id + v}); err != nil {
			t.Fatal(err)
		}
		out = append(out, map[string]any{"artifact_id": id, "artifact_version": v, "label": "V" + string(rune('0'+i))})
	}
	return out
}

func (h *exitHarness) callCode(r exitRun, name string, args map[string]any) (int, string) {
	h.t.Helper()
	w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: name, Arguments: args}, nil)
	return w.Code, w.Body.String()
}

// TestChoiceApprovalDelegation (artifact-choice-approvals 1.2, 2.1–2.4, 1.5): an agent asks one choice approval over
// registered versions; its delegate never approves it without choosing, chooses only citing what people said (an
// answer people wrote, a recorded decision, a quote of the company's or project's rules: Core checks each), asks for
// changes or rejects with a reason and no cite, or escalates dropping weak options; the requester receives the
// decision, the chosen option and who decided; people-only mode refuses every agent decision.
func TestChoiceApprovalDelegation(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	p, _ := h.s.Project(ctx, co, "notes")
	modes := tasks.ApprovalModes{InternalArtifacts: tasks.ApprovalMode{Mode: tasks.ApproveAgent, Agent: "manager"}}
	rules := "Dark theme only.\nIcons are flat."
	if _, err := h.s.UpdateProject(ctx, co, "notes", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, Approvals: &modes, Rules: &rules}); err != nil {
		t.Fatal(err)
	}
	h.create("design", "notes", "demo")
	h.create("review", "code", "manager")
	variations := choiceVersions(t, h, "design", "variations", 3)
	r := h.start("design")
	ask := func(key string, options []any, extra map[string]any) approvals.Approval {
		t.Helper()
		args := map[string]any{"options": options, "reason": "three variations of the paywall", "audience": "internal", "request_id": key}
		for k, v := range extra {
			args[k] = v
		}
		out := h.callOK(r, "factory_artifact_request_approval", args)
		var a approvals.Approval
		b, _ := json.Marshal(out)
		_ = json.Unmarshal(b, &a)
		return a
	}
	unknown := append([]any{}, variations[:2]...)
	unknown = append(unknown, map[string]any{"artifact_id": "variations", "artifact_version": "v7", "label": "V7"})
	if code, body := h.callCode(r, "factory_artifact_request_approval", map[string]any{"options": unknown, "reason": "x", "request_id": "unknown"}); code != 400 || !strings.Contains(body, "option 3 (V7): variations@v7 is not a registered version") {
		t.Fatal(code, body)
	}
	a := ask("choice", variations, map[string]any{"recommended": "1"})
	if !a.Choice() || a.Delegate != "manager" || a.Recommended != 1 || len(a.Options) != 3 {
		t.Fatalf("%+v", a)
	}
	m := h.start("review")
	decide := func(args map[string]any) (int, string) {
		args["approval_id"] = a.ID
		if _, ok := args["reason"]; !ok {
			args["reason"] = "fits the brief"
		}
		args["request_id"] = "d-" + args["action"].(string) + "-" + a.ID
		return h.callCode(m, "factory_approval_decide", args)
	}
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"action": "approve"}, "never approved without choosing"},
		{map[string]any{"action": "choose", "choice": "V3"}, "cite their earlier answers"},
		{map[string]any{"action": "choose", "choice": "V3", "cites": []any{"the owner likes dark"}}, "names no answer people wrote"},
		{map[string]any{"action": "choose", "choice": "V3", "cites": []any{"project rules: \"light theme\""}}, "the project rules do not say that"},
		{map[string]any{"action": "choose", "choice": "V9", "cites": []any{"project rules: dark theme only"}}, "choice: option"},
		{map[string]any{"action": "reject", "drop": []any{map[string]any{"option": "1", "reason": "x"}}}, "use it with action escalate"},
	} {
		if code, body := decide(c.args); code != 400 || !strings.Contains(body, c.want) {
			t.Fatalf("%v: %d %s", c.args, code, body)
		}
	}
	// Covered by the project's rules, which people write: the delegate chooses V3 and the requester learns it.
	if code, body := decide(map[string]any{"action": "choose", "choice": "V3", "reason": "only V3 is dark", "cites": []any{"project rules: “Dark theme only”"}}); code != 200 {
		t.Fatal(code, body)
	}
	tr := h.lastTrigger("design")
	if tr.kind != "approval_decided" || tr.actor != "manager" || !strings.Contains(tr.body, "V3 (variations@v3) chosen by manager (delegated: project notes: internal artifacts agent manager): only V3 is dark") {
		t.Fatalf("%+v", tr)
	}
	if got := h.approvalOf(a.ID); got.Status != "APPROVED" || got.Chosen != 3 || got.DecidedBy != "manager" || len(got.Cites) != 1 {
		t.Fatalf("%+v", got)
	}

	// Covered by an answer people wrote: the cite names their message.
	said, err := h.s.SendMessage(ctx, co, messaging.Send{ProjectID: "notes", Sender: "owner", Recipient: "manager", MessageType: "notification", Body: "flat icons, the second style", RequestID: "said"})
	if err != nil {
		t.Fatal(err)
	}
	a = ask("icons", choiceVersions(t, h, "design", "icons", 2), nil)
	if code, body := decide(map[string]any{"action": "choose", "choice": "2", "cites": []any{"owner message " + said.ID}}); code != 200 {
		t.Fatal(code, body)
	}
	// Weak work sent back: changes and reject need a reason, no cite, and no person is asked.
	a = ask("covers", choiceVersions(t, h, "design", "covers", 2), nil)
	if code, body := decide(map[string]any{"action": "changes", "options": []any{"V1"}, "reason": "the title is unreadable"}); code != 200 {
		t.Fatal(code, body)
	}
	if tr = h.lastTrigger("design"); !strings.Contains(tr.body, "changes requested by manager (delegated: project notes: internal artifacts agent manager) on V1 (covers@v1): the title is unreadable") {
		t.Fatalf("%+v", tr)
	}
	a = ask("badges", choiceVersions(t, h, "design", "badges", 2), nil)
	if code, body := decide(map[string]any{"action": "reject", "reason": "none follows the brief"}); code != 200 {
		t.Fatal(code, body)
	}
	if tr = h.lastTrigger("design"); !strings.Contains(tr.body, "all options rejected by manager") || tr.actor != "manager" {
		t.Fatalf("%+v", tr)
	}
	for _, it := range h.ownerView().inbox {
		if strings.Contains(it.Body, "badges") || strings.Contains(it.Body, "covers") {
			t.Fatalf("people were asked about weak work: %s", it.Body)
		}
	}

	// Without a basis the delegate escalates, dropping two of five; people still choose a dropped one.
	a = ask("posters", choiceVersions(t, h, "design", "posters", 5), nil)
	if code, body := decide(map[string]any{"action": "escalate", "reason": "no owner answer covers the poster", "brief": choiceBrief,
		"drop": []any{map[string]any{"option": "2", "reason": "off-brief"}, map[string]any{"option": "V4", "reason": "off-brief too"}}}); code != 200 {
		t.Fatal(code, body)
	}
	got := h.approvalOf(a.ID)
	if !got.Escalated || got.Options[1].Dropped != "off-brief" || got.Options[3].DroppedBy != "manager" || got.Options[0].Dropped != "" {
		t.Fatalf("%+v", got)
	}
	if w := h.do(h.c.Token, "POST", "/approvals/"+a.ID+"/decision", approvals.Decision{Version: got.Version, Action: "choose", Option: 2, Reason: "darker paywall", RequestID: "owner-choose"}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if tr = h.lastTrigger("design"); tr.actor != "owner" || !strings.Contains(tr.body, "V2 (posters@v2) chosen by the owner: darker paywall") {
		t.Fatalf("%+v", tr)
	}

	// People only: no agent decides a choice.
	p, _ = h.s.Project(ctx, co, "notes")
	modes.InternalArtifacts = tasks.ApprovalMode{Mode: tasks.ApproveOwner}
	if _, err = h.s.UpdateProject(ctx, co, "notes", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, Approvals: &modes}); err != nil {
		t.Fatal(err)
	}
	a = ask("people", choiceVersions(t, h, "design", "logos", 2), map[string]any{"brief": choiceBrief})
	if a.Delegate != "" {
		t.Fatalf("%+v", a)
	}
	if code, body := decide(map[string]any{"action": "choose", "choice": "1", "cites": []any{"project rules: dark theme only"}}); code != 403 {
		t.Fatal(code, body)
	}
}

// TestSingleVersionApprovalDelegation (artifact-choice-approvals 5.3, 5.5): a request for one version is an approval
// with one option and its delegate decides it by the same rules as any choice: approving (choosing the only option)
// only with cites Core checks, changes and reject with a reason and no cite, escalating whole (drop is for several
// options); the requester learns what was decided and by whom; people only refuses every agent decision.
func TestSingleVersionApprovalDelegation(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	p, _ := h.s.Project(ctx, co, "notes")
	modes := tasks.ApprovalModes{InternalArtifacts: tasks.ApprovalMode{Mode: tasks.ApproveAgent, Agent: "manager"}}
	rules := "Dark theme only."
	if _, err := h.s.UpdateProject(ctx, co, "notes", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, Approvals: &modes, Rules: &rules}); err != nil {
		t.Fatal(err)
	}
	h.create("design", "notes", "demo")
	h.create("review", "code", "manager")
	r := h.start("design")
	ask := func(id string, extra map[string]any) approvals.Approval {
		t.Helper()
		v := choiceVersions(t, h, "design", id, 1)[0].(map[string]any)
		args := map[string]any{"artifact_id": v["artifact_id"], "artifact_version": v["artifact_version"], "reason": "the " + id, "audience": "internal", "request_id": "ask-" + id}
		for k, x := range extra {
			args[k] = x
		}
		out := h.callOK(r, "factory_artifact_request_approval", args)
		var a approvals.Approval
		b, _ := json.Marshal(out)
		_ = json.Unmarshal(b, &a)
		if !a.Single() || a.Artifact == nil || a.Artifact.ID != id {
			t.Fatalf("not one option: %+v", a)
		}
		return a
	}
	m := h.start("review")
	n := 0
	decide := func(a approvals.Approval, args map[string]any) (int, string) {
		t.Helper()
		n++
		args["approval_id"], args["request_id"] = a.ID, "d-"+string(rune('a'+n))
		if _, ok := args["reason"]; !ok {
			args["reason"] = "fits the brief"
		}
		return h.callCode(m, "factory_approval_decide", args)
	}
	a := ask("paywall", nil)
	if a.Delegate != "manager" {
		t.Fatalf("%+v", a)
	}
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"action": "approve"}, "you approve only what people said covers"},
		{map[string]any{"action": "approve", "cites": []any{"the owner likes it"}}, "names no answer people wrote"},
		{map[string]any{"action": "approve", "cites": []any{"project rules: light theme"}}, "the project rules do not say that"},
		{map[string]any{"action": "choose", "choice": "2", "cites": []any{"project rules: dark theme only"}}, "choice: option"},
		{map[string]any{"action": "escalate", "brief": choiceBrief, "drop": []any{map[string]any{"option": "1", "reason": "weak"}}}, "this approval has one"},
	} {
		if code, body := decide(a, c.args); code != 400 || !strings.Contains(body, c.want) {
			t.Fatalf("%v: %d %s", c.args, code, body)
		}
	}
	if code, body := decide(a, map[string]any{"action": "approve", "reason": "it is dark", "cites": []any{"project rules: dark theme only"}}); code != 200 {
		t.Fatal(code, body)
	}
	if got := h.approvalOf(a.ID); got.Status != "APPROVED" || got.Chosen != 1 || got.DecidedBy != "manager" || len(got.Cites) != 1 {
		t.Fatalf("%+v", got)
	}
	tr := h.lastTrigger("design")
	if want := "artifact approval " + a.ID + " of paywall@v1 approved by manager (delegated: project notes: internal artifacts agent manager): it is dark"; tr.kind != "approval_decided" || tr.actor != "manager" || tr.body != want {
		t.Fatalf("%+v", tr)
	}
	// choose with choice 1 is the same decision, with the same cites rule.
	b := ask("onboarding", nil)
	if code, body := decide(b, map[string]any{"action": "choose", "choice": "1", "cites": []any{"project rules: Dark theme only."}}); code != 200 {
		t.Fatal(code, body)
	}
	// Weak work sent back: changes and reject need a reason and no cite.
	c := ask("settings", nil)
	if code, body := decide(c, map[string]any{"action": "changes", "reason": "the toggles are unreadable"}); code != 200 {
		t.Fatal(code, body)
	}
	if got := h.approvalOf(c.ID); got.Status != approvals.StatusChanges {
		t.Fatalf("%+v", got)
	}
	if tr = h.lastTrigger("design"); !strings.Contains(tr.body, "changes requested by manager") || !strings.Contains(tr.body, "the toggles are unreadable") {
		t.Fatalf("%+v", tr)
	}
	d := ask("profile", nil)
	if code, body := decide(d, map[string]any{"action": "reject", "reason": "off-brief"}); code != 200 {
		t.Fatal(code, body)
	}
	if got := h.approvalOf(d.ID); got.Status != "REJECTED" {
		t.Fatalf("%+v", got)
	}

	// People only is the ceiling: an approval already with the delegate is not decided by it; it escalates.
	e := ask("search", nil)
	p, _ = h.s.Project(ctx, co, "notes")
	modes.InternalArtifacts = tasks.ApprovalMode{Mode: tasks.ApproveOwner}
	if _, err := h.s.UpdateProject(ctx, co, "notes", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, Approvals: &modes}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"approve", "changes", "reject"} {
		if code, body := decide(e, map[string]any{"action": action, "cites": []any{"project rules: dark theme only"}}); code != 403 || !strings.Contains(body, "people decide") {
			t.Fatalf("%s: %d %s", action, code, body)
		}
	}
	if code, body := decide(e, map[string]any{"action": "escalate", "reason": "people decide here", "brief": choiceBrief}); code != 200 {
		t.Fatal(code, body)
	}
	// People decide it with the same card: approve, ask for changes, reject.
	got := h.approvalOf(e.ID)
	if w := h.do(h.c.Token, "POST", "/approvals/"+e.ID+"/decision", approvals.Decision{Version: got.Version, Action: "changes", Reason: "a lighter header", RequestID: "owner-changes"}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if tr = h.lastTrigger("design"); tr.actor != "owner" || !strings.Contains(tr.body, "changes requested by the owner: a lighter header") {
		t.Fatalf("%+v", tr)
	}
}

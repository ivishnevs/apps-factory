package core

import (
	"context"
	"strings"
	"testing"

	"appfactory/internal/approvals"
	"appfactory/internal/artifacts"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// TestApprovalDelegation (approval-delegation 2.1–2.3): modes resolve project over team over default; a delegated
// artifact approval goes to its agent and stays out of the people's Inbox until it escalates; the agent's decision is
// recorded with the setting and citations; people-only modes and money decisions refuse agents.
func TestApprovalDelegation(t *testing.T) {
	ctx := context.Background()
	h := newProjectHarness(t)
	h.c.Root, h.c.Agents = "../..", nil
	if err := h.c.LoadCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	co := storage.DefaultCompany
	if _, w := h.create(map[string]any{"id": "art", "name": "Art", "workspace": "/tmp/art", "team": "core"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	lead := h.c.cat(co).Lead("core")
	if m, d, _ := h.c.approvalMode(ctx, co, "art", tasks.KindInternalArtifacts); m.Mode != tasks.ApproveLead || d != lead {
		t.Fatalf("default internal %+v %s", m, d)
	}
	if m, d, _ := h.c.approvalMode(ctx, co, "art", tasks.KindExternalArtifacts); m.Mode != tasks.ApproveOwner || d != "" {
		t.Fatalf("default external %+v %s", m, d)
	}
	p, _ := h.s.Project(ctx, co, "art")
	modes := tasks.ApprovalModes{InternalArtifacts: tasks.ApprovalMode{Mode: tasks.ApproveAgent, Agent: "claude-demo"}, Decisions: tasks.ApprovalMode{Mode: tasks.ApproveOwner}}
	if _, err := h.s.UpdateProject(ctx, co, "art", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, Approvals: &modes}); err != nil {
		t.Fatal(err)
	}
	rd := requestDelegation{audience: "internal", requester: "demo"}
	h.c.artifactDelegate(ctx, co, "art", &rd)
	if rd.delegate != "claude-demo" || !strings.Contains(rd.setting, "project art") {
		t.Fatalf("%+v", rd)
	}
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "draw", ProjectID: "art", Title: "draw", Assignee: "demo", RequestID: "draw"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.RegisterArtifact(ctx, co, "art", artifacts.Register{Artifact: tasks.Artifact{ID: "concept", Version: "v1", URI: "https://example.com/c.png"}, TaskID: "draw", Actor: "owner", RequestID: "reg"}); err != nil {
		t.Fatal(err)
	}
	a, err := h.s.RequestArtifactApproval(ctx, co, "art", artifacts.ApprovalRequest{ArtifactID: "concept", ArtifactVersion: "v1", Agent: "demo", Reason: "concept pack", RequestID: "ask", Audience: "internal", Delegate: rd.delegate, Setting: rd.setting})
	if err != nil {
		t.Fatal(err)
	}
	inbox, _ := h.s.Inbox(ctx, co, "")
	for _, m := range inbox {
		if m.ApprovalID == a.ID {
			t.Fatal("a delegated approval is in the people's Inbox")
		}
	}
	if _, err = h.s.EscalateApproval(ctx, co, a.ID, "claude-demo", "the style is new"); err != nil {
		t.Fatal(err)
	}
	inbox, _ = h.s.Inbox(ctx, co, "")
	found := false
	for _, m := range inbox {
		found = found || m.ApprovalID == a.ID && strings.Contains(m.Body, "the style is new")
	}
	if !found {
		t.Fatal("escalated approval not in the Inbox")
	}
	// A second version, decided by the delegate.
	if _, err = h.s.RegisterArtifact(ctx, co, "art", artifacts.Register{Artifact: tasks.Artifact{ID: "concept", Version: "v2", URI: "https://example.com/c2.png"}, TaskID: "draw", Actor: "owner", RequestID: "reg2"}); err != nil {
		t.Fatal(err)
	}
	a2, err := h.s.RequestArtifactApproval(ctx, co, "art", artifacts.ApprovalRequest{ArtifactID: "concept", ArtifactVersion: "v2", Agent: "demo", Reason: "v2", RequestID: "ask2", Audience: "internal", Delegate: rd.delegate, Setting: rd.setting})
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.s.DecideApproval(ctx, co, a2.ID, approvals.Decision{Version: a2.Version, Action: "approve", Reason: "matches the guide", RequestID: "d2", DecidedBy: "claude-demo", Cites: []string{"style guide decision"}})
	if err != nil || got.Status != "APPROVED" || got.DecidedBy != "claude-demo" || len(got.Cites) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	// Decisions: people only on this project; a money decision is never delegated.
	if ok, _, err := h.c.decisionDelegation(ctx, co, "art", lead, "technical", 1); ok || err == nil {
		t.Fatal("people-only decisions delegated")
	}
	p, _ = h.s.Project(ctx, co, "art")
	modes.Decisions = tasks.ApprovalMode{}
	if _, err = h.s.UpdateProject(ctx, co, "art", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, Approvals: &modes}); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := h.c.decisionDelegation(ctx, co, "art", lead, "technical", 0); ok || err == nil {
		t.Fatal("the lead decided without citing earlier answers")
	}
	if ok, setting, err := h.c.decisionDelegation(ctx, co, "art", lead, "technical", 2); !ok || err != nil || !strings.Contains(setting, "decisions lead") {
		t.Fatal(ok, setting, err)
	}
	if ok, _, err := h.c.decisionDelegation(ctx, co, "art", lead, "budget", 2); ok || err == nil {
		t.Fatal("a money decision delegated")
	}
	if ok, _, _ := h.c.decisionDelegation(ctx, co, "art", "demo", "technical", 2); ok {
		t.Fatal("a non-lead decided in lead mode")
	}
}

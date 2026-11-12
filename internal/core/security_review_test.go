package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"appfactory/internal/artifacts"
	"appfactory/internal/factorytools"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

// Regression tests of the public-surface security review (docs/security-review-2026-11.md).

// TestOwnerTokenActsAsAdmin (F1): a token an admin issued must not reach what only an owner may do (roles,
// invitations, billing, renaming), so an admin cannot make themselves an owner through a token. The company's first
// token, issued by the operator, stays the owner's.
func TestOwnerTokenActsAsAdmin(t *testing.T) {
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	ctx := context.Background()
	if err = s.CreateCompany(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	admin, err := s.CreateOwnerToken(ctx, "acme", "admin's CLI", "user:u-admin")
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CreateOwnerToken(ctx, "acme", "first owner", "operator")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Token: "operator"}
	h := c.Handler()
	call := func(token, method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct{ method, path string }{
		{"PATCH", "/memberships/u-admin"},
		{"DELETE", "/memberships/u-owner"},
		{"POST", "/invitations"},
		{"GET", "/invitations"},
		{"PATCH", "/company"},
		{"GET", "/billing"},
	} {
		if w := call(admin.Token, tc.method, tc.path, map[string]string{"role": "owner"}); w.Code != 403 {
			t.Fatal("an admin's token reached an owner route", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if w := call(admin.Token, "GET", "/company", nil); w.Code != 200 {
		t.Fatal("an admin's token reads its company", w.Code, w.Body.String())
	}
	if w := call(admin.Token, "GET", "/owner-tokens", nil); w.Code != 200 {
		t.Fatal("an admin's token lists tokens", w.Code, w.Body.String())
	}
	if w := call(first.Token, "POST", "/invitations", map[string]string{"role": "member"}); w.Code != 201 {
		t.Fatal("the operator's first token is the owner's", w.Code, w.Body.String())
	}
}

// TestDelegatedApprovalDecideReturns (F3): the delegate agent's factory_approval_decide answers and leaves agent
// calls working; it used to take approvalMu a second time under /agent/call and hang every agent of every company.
func TestDelegatedApprovalDecideReturns(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	p, err := h.s.Project(ctx, co, "notes")
	if err != nil {
		t.Fatal(err)
	}
	modes := tasks.ApprovalModes{InternalArtifacts: tasks.ApprovalMode{Mode: tasks.ApproveAgent, Agent: "reviewer"}}
	rules := "Concepts follow the brief."
	if _, err = h.s.UpdateProject(ctx, co, "notes", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, Approvals: &modes, Rules: &rules}); err != nil {
		t.Fatal(err)
	}
	if _, err = h.s.CreateTask(ctx, co, tasks.Create{ID: "draw", ProjectID: "notes", Title: "draw", Assignee: "demo", RequestID: "draw"}); err != nil {
		t.Fatal(err)
	}
	if _, err = h.s.RegisterArtifact(ctx, co, "notes", artifacts.Register{Artifact: tasks.Artifact{ID: "concept", Version: "v1", URI: "https://example.com/c.png"}, TaskID: "draw", Actor: "owner", RequestID: "reg"}); err != nil {
		t.Fatal(err)
	}
	rd := requestDelegation{audience: "internal", requester: "demo"}
	h.c.artifactDelegate(ctx, co, "notes", &rd)
	if rd.delegate != "reviewer" {
		t.Fatalf("%+v", rd)
	}
	a, err := h.s.RequestArtifactApproval(ctx, co, "notes", artifacts.ApprovalRequest{ArtifactID: "concept", ArtifactVersion: "v1", Agent: "demo", Reason: "concept", RequestID: "ask", Audience: "internal", Delegate: rd.delegate, Setting: rd.setting})
	if err != nil {
		t.Fatal(err)
	}
	h.create("judge", "notes", "reviewer")
	r := h.start("judge")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_approval_decide", Arguments: map[string]any{"approval_id": a.ID, "action": "approve", "reason": "matches the brief", "cites": []any{"project rules: concepts follow the brief"}, "request_id": "d1"}}, nil)
	}()
	select {
	case w := <-done:
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatal("factory_approval_decide hangs (approvalMu taken twice)")
	}
	got, err := h.s.Approval(ctx, co, a.ID)
	if err != nil || got.Status != "APPROVED" {
		t.Fatalf("%+v %v", got, err)
	}
	// The next agent call still goes through.
	h.mcp(r, "factory_task_get", map[string]any{"id": "judge"})
}

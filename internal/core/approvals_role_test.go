package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"appfactory/internal/approvals"
	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

// TestApprovalRoles (core-secret-store 1.4): approving a catalog change or a project creation needs an owner or admin;
// a member can still reject them and can approve anything else.
func TestApprovalRoles(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	cat, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Agents: cat.Agents}
	if _, err = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: "/tmp/approval-roles"}); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "waiting", Assignee: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if task, err = s.TaskAction(ctx, storage.DefaultCompany, task.ID, tasks.Action{Version: task.Version, Action: "ready"}); err != nil {
		t.Fatal(err)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, storage.DefaultCompany, task.ID)
	if _, err = s.Claim(ctx, storage.DefaultCompany, task.CurrentRunID); err != nil {
		t.Fatal(err)
	}
	request := func(name, key string) approvals.Approval {
		t.Helper()
		a, err := s.RequestApproval(ctx, storage.DefaultCompany, approvals.Request{ProjectID: "p", Agent: "demo", TaskID: task.ID, RunID: task.CurrentRunID,
			Call: factorytools.Call{Name: name, Arguments: map[string]any{"request_id": key}}, Reason: "test", RequestID: key})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	m := http.NewServeMux()
	c.approvalRoutes(m)
	decide := func(role string, a approvals.Approval, action string) int {
		t.Helper()
		b, _ := json.Marshal(approvals.Decision{Version: a.Version, Action: action, Reason: "test", RequestID: action + role + a.ID})
		r := httptest.NewRequest("POST", "/approvals/"+a.ID+"/decision", bytes.NewReader(b))
		r = r.WithContext(context.WithValue(context.WithValue(r.Context(), principalKey{}, principal{Kind: "session", Role: role}), companyKey{}, storage.DefaultCompany))
		w := httptest.NewRecorder()
		m.ServeHTTP(w, r)
		return w.Code
	}
	catalog := request("factory_catalog_change", "c1")
	if code := decide(storage.RoleMember, catalog, "approve"); code != http.StatusForbidden {
		t.Fatal("member approved a catalog change", code)
	}
	if got, err := s.Approval(ctx, storage.DefaultCompany, catalog.ID); err != nil || got.Status != catalog.Status {
		t.Fatal("refused approval changed", got.Status, err)
	}
	if code := decide(storage.RoleAdmin, catalog, "approve"); code != http.StatusOK {
		t.Fatal("admin refused", code)
	}
	project := request("factory_project_create", "c2")
	if code := decide(storage.RoleMember, project, "approve"); code != http.StatusForbidden {
		t.Fatal("member approved a project creation", code)
	}
	if code := decide(storage.RoleMember, project, "reject"); code != http.StatusOK {
		t.Fatal("member could not reject", code)
	}
	if code := decide(storage.RoleMember, request("factory_task_create", "c3"), "approve"); code != http.StatusOK {
		t.Fatal("member could not approve an ordinary action", code)
	}
}

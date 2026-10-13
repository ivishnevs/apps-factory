package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

func TestRunScopedMCP(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "db")
	s, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.DB.Close() }()
	catalog, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	a := catalog.Agents["demo"]
	a.Spec.Communication.CanCreateTasks = true
	a.Spec.Communication.CanAssignTasks = true
	catalog.Agents["demo"] = a
	reviewer := a
	reviewer.Metadata.Name = "reviewer"
	catalog.Agents["reviewer"] = reviewer
	c := &Core{Store: s, Agents: catalog.Agents, Skills: catalog.Skills, Token: "owner-secret"}
	call := func(token, method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w
	}
	for _, id := range []string{"p", "other"} {
		if _, err = s.CreateProject(ctx, tasks.ProjectCreate{ID: id, Name: id, Workspace: "/tmp/mcp-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	other, err := s.CreateTask(ctx, tasks.Create{ProjectID: "other", Title: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, tasks.Create{ProjectID: "p", Title: "main", Assignee: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.TaskAction(ctx, task.ID, tasks.Action{Version: task.Version, Action: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, task.ID)
	run, _ := s.Get(ctx, task.CurrentRunID)
	lease, err := s.Claim(ctx, run.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if w := call(c.Token, "POST", "/runs/"+run.Spec.ID+"/credential", map[string]string{"lease": "fake"}); w.Code != 403 {
		t.Fatal(w.Code)
	}
	w := call(c.Token, "POST", "/runs/"+run.Spec.ID+"/credential", map[string]string{"lease": lease})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var credential struct{ Token string }
	if err = json.Unmarshal(w.Body.Bytes(), &credential); err != nil {
		t.Fatal(err)
	}
	token := credential.Token
	tool := func(name string, args map[string]any) *httptest.ResponseRecorder {
		return call(token, "POST", "/agent/call", factorytools.Call{Name: "factory_" + name, Arguments: args})
	}
	if strings.Contains(token, c.Token) || strings.Contains(token, lease) {
		t.Fatal("credential exposed parent secret")
	}
	for _, path := range []string{"/runs", "/agents", "/tasks", "/projects"} {
		if w = call(token, "GET", path, nil); w.Code != 401 {
			t.Fatal("owner API bypass", path, w.Code)
		}
	}
	if w = call(token+"x", "GET", "/agent/tools", nil); w.Code != 401 {
		t.Fatal("forged token accepted")
	}
	if w = tool("task_get", map[string]any{"id": other.ID}); w.Code != 403 {
		t.Fatal("cross project read", w.Code, w.Body.String())
	}
	if w = tool("task_create", map[string]any{"title": "bypass", "actor": "owner", "request_id": "bad"}); w.Code != 400 {
		t.Fatal("actor accepted", w.Code)
	}
	if w = tool("task_create", map[string]any{"title": "bypass", "project_id": "other", "request_id": "bad"}); w.Code != 400 {
		t.Fatal("project accepted", w.Code)
	}
	if w = tool("task_create", map[string]any{"title": "child", "request_id": ""}); w.Code != 400 {
		t.Fatal("empty replay key", w.Code)
	}
	input := map[string]any{"title": "child", "assignee": "demo", "request_id": "create-child"}
	w = tool("task_create", input)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	original := w.Body.String()
	var child tasks.Task
	if err = json.Unmarshal(w.Body.Bytes(), &child); err != nil {
		t.Fatal(err)
	}
	if child.CreatedBy != "demo" || child.ProjectID != "p" {
		t.Fatal(child)
	}
	if w = tool("task_create", input); w.Code != 200 || w.Body.String() != original {
		t.Fatal("replay differs", w.Code, w.Body.String())
	}
	if w = tool("task_update", map[string]any{"id": child.ID, "version": 0, "title": "stale", "request_id": "stale"}); w.Code != 409 {
		t.Fatal("stale accepted", w.Code)
	}
	// Current config may tighten but never loosen the captured run policy.
	changed := a
	changed.Spec.Tools.Deny = append(changed.Spec.Tools.Deny, "factory.task.get")
	c.Agents["demo"] = changed
	if w = tool("task_get", map[string]any{"id": child.ID}); w.Code != 403 {
		t.Fatal("current deny ignored", w.Code)
	}
	c.Agents["demo"] = a
	changed = a
	changed.Spec.Approvals.Require = append(changed.Spec.Approvals.Require, "factory.task.update")
	c.Agents["demo"] = changed
	if w = tool("task_update", map[string]any{"id": child.ID, "version": child.Version, "title": "approval", "request_id": "approval"}); w.Code != 423 {
		t.Fatal("approval bypass", w.Code, w.Body.String())
	}
	c.Agents["demo"] = a
	if w = tool("skills_read", map[string]any{"skill": "openspec", "resource": "references/conventions.md"}); w.Code != 200 || !strings.Contains(w.Body.String(), "AGENT_CONFIG_OK") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = tool("skills_read", map[string]any{"skill": "openspec", "resource": "../../.env"}); w.Code != 400 {
		t.Fatal("skill traversal", w.Code)
	}
	// Submission is a draft while the runtime still holds the writer lock.
	if w = tool("task_submit", map[string]any{"id": child.ID, "version": child.Version, "submission": map[string]any{"summary": "wrong task"}, "request_id": "wrong-submit"}); w.Code != 403 {
		t.Fatal("wrong-run submit", w.Code)
	}
	submit := map[string]any{"id": task.ID, "version": task.Version, "submission": map[string]any{"summary": "structured", "checks": []string{"go test"}}, "request_id": "submit"}
	if w = tool("task_submit", submit); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	task, _ = s.Task(ctx, task.ID)
	if task.Status != "IN_PROGRESS" || task.Submission == nil {
		t.Fatal(task)
	}
	if w = tool("task_accept", map[string]any{"id": task.ID, "version": task.Version, "request_id": "self-review"}); w.Code != 403 {
		t.Fatal("self review", w.Code)
	}
	if w = tool("task_ready", map[string]any{"id": child.ID, "version": child.Version, "request_id": "ready-child"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	child, _ = s.Task(ctx, child.ID)
	if child.Attempt != 0 {
		t.Fatal("writer released before terminal")
	}
	// Persisted credentials and audits survive a Core restart.
	s.DB.Close()
	s, err = storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	c.Store = s
	if w = tool("project_get", map[string]any{}); w.Code != 200 {
		t.Fatal("restart invalidated live token", w.Code, w.Body.String())
	}
	event := protocol.Wrap("agent.completed", run.Spec.ID, "", protocol.Event{RunID: run.Spec.ID, Lease: lease, Status: "COMPLETED", Result: "final text"})
	if err = s.Apply(ctx, event); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, task.ID)
	if task.Status != "IN_REVIEW" || task.Submission.Summary != "structured" || len(task.Submission.Checks) != 1 {
		t.Fatal("draft lost", task)
	}
	if w = tool("task_get", map[string]any{"id": task.ID}); w.Code != 401 {
		t.Fatal("terminal credential alive", w.Code)
	}
	var count, pending int
	if err = s.DB.QueryRow("SELECT count(*),sum(status=0) FROM tool_calls WHERE run_id=?", run.Spec.ID).Scan(&count, &pending); err != nil || count < 10 || pending != 0 {
		t.Fatal(count, pending, err)
	}
	// Reviewer is a distinct bound run; accepting own work never gets owner treatment.
	reviewSpec := protocol.RunSpec{ID: protocol.ID(), Project: "p", Agent: "reviewer", Workspace: "/tmp/mcp-p", Provider: protocol.RuntimeCodex}
	if err = s.Create(ctx, reviewSpec); err != nil {
		t.Fatal(err)
	}
	reviewLease, err := s.Claim(ctx, reviewSpec.ID)
	if err != nil {
		t.Fatal(err)
	}
	token = c.runToken(reviewSpec.ID, reviewLease)
	if w = tool("task_reject", map[string]any{"id": task.ID, "version": task.Version, "feedback": "Add evidence", "request_id": "review-reject"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	task, _ = s.Task(ctx, task.ID)
	if task.Status != "REWORK" {
		t.Fatal(task)
	}
	if _, err = s.DB.Exec("UPDATE runs SET lease_until=? WHERE id=?", time.Now().Unix()-1, reviewSpec.ID); err != nil {
		t.Fatal(err)
	}
	if w = tool("task_get", map[string]any{"id": task.ID}); w.Code != 401 {
		t.Fatal("expired lease accepted", w.Code)
	}
}

package core

import (
	"appfactory/internal/config"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestTaskMutationPoliciesAndVersions(t *testing.T) {
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Agents: agents, Token: "test"}
	_, err = s.CreateProject(context.Background(), storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/api-test"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer test")
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/tasks", tasks.Create{ProjectID: "p", Title: "agent task", Actor: "demo"}); w.Code != 403 {
		t.Fatal("create permission ignored", w.Code, w.Body.String())
	}
	w := call("POST", "/tasks", tasks.Create{ProjectID: "p", Title: "owner task", Assignee: "demo", RequestID: "create"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var task tasks.Task
	if err = json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if w = call("PATCH", "/tasks/"+task.ID, tasks.Update{Version: task.Version - 1}); w.Code != 409 {
		t.Fatal("stale version accepted", w.Code)
	}
	assignee := "demo"
	if w = call("PATCH", "/tasks/"+task.ID, tasks.Update{Version: task.Version, Assignee: &assignee, Actor: "demo"}); w.Code != 403 {
		t.Fatal("assignment permission ignored", w.Code)
	}
	agent := agents["demo"]
	agent.Spec.Approvals.Mode = "always"
	agents["demo"] = agent
	if w = call("POST", "/tasks/"+task.ID+"/actions", tasks.Action{Version: task.Version, Action: "ready", Actor: "demo"}); w.Code != 423 {
		t.Fatal("approval bypass", w.Code, w.Body.String())
	}
	if w = call("POST", "/tasks", map[string]any{"project_id": "p", "title": "status bypass", "status": "DONE"}); w.Code != 400 {
		t.Fatal("arbitrary status accepted", w.Code)
	}
}
func TestSchedulerPriority(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Agents: agents}
	_, err = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/priority-test"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, priority := range []int{1, 10} {
		task, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "priority", Priority: priority, Assignee: "demo"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.TaskAction(ctx, storage.DefaultCompany, task.ID, tasks.Action{Version: task.Version, Action: "ready"}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, task.ID)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	low, _ := s.Task(ctx, storage.DefaultCompany, ids[0])
	high, _ := s.Task(ctx, storage.DefaultCompany, ids[1])
	if low.Status != "READY" || high.Status != "IN_PROGRESS" {
		t.Fatal("priority/reservation ignored", low, high)
	}
}

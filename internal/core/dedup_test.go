package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

func TestMCPTaskCreateRecordsOriginAndDeduplicates(t *testing.T) {
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
	demo := agents["demo"]
	demo.Spec.Communication.CanCreateTasks = true
	agents["demo"] = demo
	c := &Core{Store: s, Agents: agents, Token: "owner"}
	if _, err = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/core-dedup"}); err != nil {
		t.Fatal(err)
	}
	origin, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ID: "origin", ProjectID: "p", Title: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(demo)
	run := protocol.RunSpec{ID: "planner", Project: "p", Agent: "demo", Workspace: "/tmp/core-dedup", TaskID: origin.ID, ConfigSnapshot: snapshot}
	if err = s.Create(ctx, storage.DefaultCompany, run); err != nil {
		t.Fatal(err)
	}
	// The claim re-check wants the run to be the task's current run of its assignee.
	if _, err = s.DB.Exec(`UPDATE tasks SET status='IN_PROGRESS',assignee='demo',body=json_set(body,'$.status','IN_PROGRESS','$.assignee','demo','$.current_run_id','planner') WHERE id=?`, origin.ID); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Claim(ctx, storage.DefaultCompany, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	call := func(args map[string]any) (int, tasks.Task) {
		b, _ := json.Marshal(factorytools.Call{Name: "factory_task_create", Arguments: args})
		r := httptest.NewRequest("POST", "/agent/call", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+c.runToken(storage.DefaultCompany, run.ID, lease))
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		var v tasks.Task
		_ = json.Unmarshal(w.Body.Bytes(), &v)
		return w.Code, v
	}
	code, first := call(map[string]any{"title": "Build settings screen", "request_id": "one"})
	if code != 200 || first.OriginTask != origin.ID || first.OriginRun != run.ID || first.Deduplicated {
		t.Fatal(code, first)
	}
	// A repeated run asks again with a new request_id: the open task is returned, not duplicated.
	code, again := call(map[string]any{"title": "build  settings screen", "request_id": "two"})
	if code != 200 || again.ID != first.ID || !again.Deduplicated {
		t.Fatal(code, again)
	}
	if code, _ = call(map[string]any{"title": "x", "request_id": "forged", "origin_task": "other"}); code != 400 {
		t.Fatal("origin accepted from tool arguments", code)
	}
	if all, _ := s.Tasks(ctx, storage.DefaultCompany, "p", ""); len(all) != 2 {
		t.Fatal(len(all))
	}
}

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
	"appfactory/internal/testnode"
)

// TestTaskDuplicateWarningsMCP (task-duplicate-warnings 3.1, 3.2, 4.1, replayed with fakes): on a project shaped like
// the game project (285 tasks with descriptions), listing the open tasks of one change is compact and costs under
// 5 % of the full list; a list filtered by assignee needs no assign permission; the second follow-up for an item
// gets the warning and the hint in the tool answer; not_duplicate_of answers it; replaces cancels the original
// split by the producer.
func TestTaskDuplicateWarningsMCP(t *testing.T) {
	ctx := context.Background()
	co := storage.DefaultCompany
	s, err := storage.Open(storagetest.URL(t))
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
	demo.Spec.Communication.CanAssignTasks = false
	agents["demo"] = demo
	c := &Core{Store: s, Agents: agents, Token: "owner"}
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/core-duplicates"}); err != nil {
		t.Fatal(err)
	}
	// The game project's shape: 285 tasks, descriptions of a few KiB, 14 open ones of world-models.
	description := strings.Repeat("Acceptance: the scene loads, the art matches the brief, the checks pass on device. ", 30)
	for i := range 285 {
		in := tasks.Create{ID: fmt.Sprintf("g%03d", i), ProjectID: "p", Title: fmt.Sprintf("3D-%03d: game work item %d", i, i), Description: description, Assignee: "demo", RequestID: fmt.Sprintf("g%d", i)}
		switch {
		case i < 14:
			in.OpenSpecChange, in.Covers = "world-models", []string{fmt.Sprintf("world-models#%d.%d", i/5+1, i%5+1)}
		case i < 100:
			in.OpenSpecChange = fmt.Sprintf("change-%d", i%9)
		}
		if _, err = s.CreateTask(ctx, co, in); err != nil {
			t.Fatal(err)
		}
	}
	origin, err := s.CreateTask(ctx, co, tasks.Create{ID: "origin", ProjectID: "p", Title: "plan"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(demo)
	run := protocol.RunSpec{ID: "planner", Project: "p", Agent: "demo", Workspace: "/tmp/core-duplicates", TaskID: origin.ID, ConfigSnapshot: snapshot, Provider: protocol.RuntimeCodex}
	if err = s.Create(ctx, co, run); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE tasks SET status='IN_PROGRESS',assignee='demo',body=(body::jsonb||jsonb_build_object('status','IN_PROGRESS','assignee','demo','current_run_id','planner'))::json WHERE id=?`, origin.ID); err != nil {
		t.Fatal(err)
	}
	lease, err := testnode.Claim(t, s, co, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args map[string]any) (int, []byte) {
		t.Helper()
		b, _ := json.Marshal(factorytools.Call{Name: name, Arguments: args})
		r := httptest.NewRequest("POST", "/agent/call", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+c.runToken(co, run.ID, lease))
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w.Code, w.Body.Bytes()
	}

	full := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/tasks?project=p", nil)
	req.Header.Set("Authorization", "Bearer owner")
	c.Handler().ServeHTTP(full, req)
	if full.Code != 200 {
		t.Fatal(full.Code, full.Body.String())
	}
	code, body := call("factory_task_list", map[string]any{"open": true, "openspec_change": "world-models"})
	if code != 200 {
		t.Fatal(code, string(body))
	}
	var compact []map[string]any
	if err = json.Unmarshal(body, &compact); err != nil || len(compact) != 14 {
		t.Fatalf("%d tasks: %v", len(compact), err)
	}
	if _, ok := compact[0]["description"]; ok || compact[0]["covers"] == nil {
		t.Fatalf("not compact: %v", compact[0])
	}
	if ratio := float64(len(body)) / float64(full.Body.Len()); ratio >= 0.05 {
		t.Fatalf("open tasks of one change cost %.1f%% of the full list (%d of %d bytes)", ratio*100, len(body), full.Body.Len())
	}
	// Filters: covers (a group matches its items), query words, assignee without assign permission, full.
	if code, body = call("factory_task_list", map[string]any{"covers": "world-models#2", "query": "ITEM game"}); code != 200 || strings.Count(string(body), `"id"`) != 5 {
		t.Fatal(code, string(body))
	}
	if code, body = call("factory_task_list", map[string]any{"assignee": "demo", "query": "3D-007:", "full": true}); code != 200 || !strings.Contains(string(body), `"description"`) || strings.Count(string(body), `"id"`) != 1 {
		t.Fatal(code, string(body))
	}

	// The second follow-up for an item, from another run: created, with the warning and the hint.
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "first", ProjectID: "p", Title: "M5b-P3 follow-up", Description: "world-models 7.2 from PR #148", Assignee: "demo", RequestID: "first"}); err != nil {
		t.Fatal(err)
	}
	code, body = call("factory_task_create", map[string]any{"title": "world-models 7.2 remainder", "request_id": "second"})
	var created tasks.Task
	if err = json.Unmarshal(body, &created); code != 200 || err != nil {
		t.Fatal(code, string(body))
	}
	var warned *tasks.DuplicateWarning
	for i, w := range created.Warnings {
		if w.TaskID == "first" {
			warned = &created.Warnings[i]
		}
	}
	if warned == nil || !slices.Equal(warned.Reasons, []string{tasks.ReasonSameWorkItem}) || !strings.Contains(created.DuplicateHint, "not_duplicate_of") || created.OpenSpecChange != "world-models" {
		t.Fatalf("answer: %s", body)
	}
	// Keep both: an update with only not_duplicate_of needs no version.
	if code, body = call("factory_task_update", map[string]any{"id": created.ID, "version": 0, "not_duplicate_of": []any{"first"}, "request_id": "keep"}); code != 200 {
		t.Fatal(code, string(body))
	}
	if got, _ := s.Task(ctx, co, "first"); len(got.PossibleDuplicates) != 0 || !slices.Contains(got.NotDuplicateOf, created.ID) {
		t.Fatalf("%+v", got)
	}

	// The producer splits a task it created: the original is cancelled (demo's policy allows cancel).
	code, body = call("factory_task_create", map[string]any{"title": "split original", "request_id": "orig"})
	var orig tasks.Task
	_ = json.Unmarshal(body, &orig)
	if code != 200 {
		t.Fatal(code, string(body))
	}
	code, body = call("factory_task_create", map[string]any{"title": "split part 1", "replaces": []any{orig.ID}, "request_id": "part1"})
	if code != 200 {
		t.Fatal(code, string(body))
	}
	got, _ := s.Task(ctx, co, orig.ID)
	if !c.cancelAllowed(co, "demo") || got.Status != "CANCELLED" || !slices.Equal(got.ReplacedBy, []string{gjson(t, body, "id")}) {
		t.Fatalf("split: %+v / %s", got, body)
	}
}

func gjson(t *testing.T, body []byte, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	v, _ := m[key].(string)
	return v
}

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

func TestWhyExplainsEveryWaitingTask(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Agents: agents, Token: "owner"}
	if _, err = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/core-why"}); err != nil {
		t.Fatal(err)
	}
	create := func(id, assignee string, ready bool, deps ...string) {
		t.Helper()
		v, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ID: id, ProjectID: "p", Title: id, Assignee: assignee, Dependencies: deps, SideEffects: id == "a"})
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			if _, err = s.TaskAction(ctx, storage.DefaultCompany, id, tasks.Action{Version: v.Version, Action: "ready"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	create("a", "demo", true)
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	create("b", "demo", true, "a")
	create("c", "demo", false)
	create("d", "demo", true)
	create("e", "ghost", true)
	why := func() map[string]tasks.Verdict {
		t.Helper()
		r := httptest.NewRequest("GET", "/projects/p/why", nil)
		r.Header.Set("Authorization", "Bearer owner")
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		var vs []tasks.Verdict
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &vs) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		out := map[string]tasks.Verdict{}
		for _, v := range vs {
			out[v.TaskID] = v
		}
		return out
	}
	expect := func(got map[string]tasks.Verdict, want map[string]string) {
		t.Helper()
		for id, reason := range want {
			if got[id].Reason != reason {
				t.Errorf("%s: %+v, want %s", id, got[id], reason)
			}
		}
	}
	expect(why(), map[string]string{"a": tasks.ReasonLiveRun, "b": tasks.ReasonDependencies, "c": tasks.ReasonBacklog, "d": tasks.ReasonCapacity, "e": tasks.ReasonUnassigned})

	a, _ := s.Task(ctx, storage.DefaultCompany, "a")
	if _, err = s.Claim(ctx, storage.DefaultCompany, a.CurrentRunID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE runs SET lease_until=?", time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err = s.Expire(ctx, storage.DefaultCompany); err != nil {
		t.Fatal(err)
	}
	// a has side effects: inspected, not retried. Its v1 runtime is gone (the worker stops on a lost
	// lease), so the run holds no writer and d can run.
	got := why()
	expect(got, map[string]string{"a": tasks.ReasonParked + tasks.ParkEscalation, "b": tasks.ReasonDependencies, "d": tasks.ReasonRunnable})
	// A lost run whose runtime may still run (a silent v2 node) keeps the project's writer.
	if _, err = s.DB.Exec("UPDATE runs SET unreleased=1 WHERE id=?", a.CurrentRunID); err != nil {
		t.Fatal(err)
	}
	got = why()
	expect(got, map[string]string{"d": tasks.ReasonCapacity})
	if got["d"].Clearer.Who != "node" || !strings.Contains(got["d"].Clearer.Action, "factory runs resolve") {
		t.Fatal("a held writer must name its node and the resolve", got["d"])
	}

	r := httptest.NewRequest("GET", "/tasks/b/why", nil)
	r.Header.Set("Authorization", "Bearer owner")
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	var v tasks.Verdict
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Clearer.Who != "task:a" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestTaskReadsCarryReason(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Agents: agents, Token: "owner"}
	if _, err = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/core-reason"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ID: "a", ProjectID: "p", Title: "a", Assignee: "demo", Ready: true}); err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ID: "b", ProjectID: "p", Title: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TaskAction(ctx, storage.DefaultCompany, "b", tasks.Action{Version: b.Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	get := func(path string, v any) {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer owner")
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), v) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	var list []taskView
	get("/tasks?project=p", &list)
	reasons := map[string]string{}
	for _, v := range list {
		reasons[v.ID] = v.Reason
	}
	if reasons["a"] != tasks.ReasonRunnable || reasons["b"] != tasks.ReasonTerminal {
		t.Fatal(reasons)
	}
	var one taskView
	get("/tasks/a", &one)
	if one.ID != "a" || one.Title != "a" || one.Reason != tasks.ReasonRunnable || one.Clearer.Who != "scheduler" {
		t.Fatal(one)
	}
}

func TestQuotaPauseAndWorkspaceOverHTTP(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Agents: agents, Token: "owner"}
	co := storage.DefaultCompany
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/core-quota", RepoURL: "/srv/git/app.git"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "a", ProjectID: "p", Title: "a", Assignee: "demo", Ready: true}); err != nil {
		t.Fatal(err)
	}
	// demo takes quota_key codex-subscription from the developer profile (one ChatGPT account).
	if err = s.SetPause(ctx, co, storage.Pause{Scope: "quota_key", Target: "codex-subscription", Actor: "runtime", Until: s.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any, v any) int {
		t.Helper()
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer owner")
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		if v != nil {
			_ = json.Unmarshal(w.Body.Bytes(), v)
		}
		return w.Code
	}
	var verdict tasks.Verdict
	call("GET", "/tasks/a/why", nil, &verdict)
	if verdict.Reason != tasks.ReasonPaused+"quota_key:codex-subscription" || verdict.Clearer.Who != "time" {
		t.Fatal(verdict)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Task(ctx, co, "a"); a.Status != "READY" {
		t.Fatal("dispatched on a paused quota key", a.Status)
	}
	if err = s.ClearPause(ctx, co, "quota_key", "codex-subscription", ""); err != nil {
		t.Fatal(err)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Task(ctx, co, "a")
	run, _ := s.Get(ctx, co, a.CurrentRunID)
	if run.Spec.RepoURL != "/srv/git/app.git" || run.Spec.WorkspaceMode != "" {
		t.Fatalf("%+v", run.Spec)
	}
	lease, err := s.Claim(ctx, co, a.CurrentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if code := call("POST", "/runs/"+a.CurrentRunID+"/workspace", map[string]any{"lease": lease, "mode": "shared"}, nil); code != 200 {
		t.Fatal(code)
	}
	var ws tasks.Workspace
	if code := call("GET", "/tasks/a/workspace", nil, &ws); code != 200 || ws.Mode != "shared" || ws.RunID != a.CurrentRunID {
		t.Fatal(code, ws)
	}
}

package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

func TestWhyExplainsEveryWaitingTask(t *testing.T) {
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
	c := &Core{Store: s, Agents: agents, Token: "owner"}
	if _, err = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/core-why"}); err != nil {
		t.Fatal(err)
	}
	create := func(id, assignee string, ready bool, deps ...string) {
		t.Helper()
		v, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ID: id, ProjectID: "p", Title: id, Assignee: assignee, Dependencies: deps})
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
	got := why()
	expect(got, map[string]string{"a": tasks.ReasonParked + tasks.ParkInspect, "b": tasks.ReasonDependencies, "d": tasks.ReasonCapacity})
	if got["d"].Clearer.Who != "owner" {
		t.Fatal("quarantine must name the owner", got["d"])
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

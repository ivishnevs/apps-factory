package worker

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver/fake"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

// TestStaleOfferIsDropped (trigger-log-scheduling D6): a node claims a run whose task no
// longer wants it (a dependency reopened after dispatch); Core answers 409 drop, the worker drops the offer
// without starting the runtime (no retry), and the run ends stale.
func TestStaleOfferIsDropped(t *testing.T) {
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	c := &core.Core{Store: s, Token: "test-token", Agents: map[string]config.Agent{"demo": {}}}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	co := storage.DefaultCompany
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "dep", ProjectID: "p", Title: "dep", Assignee: "demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE tasks SET status='DONE',body=(body::jsonb||jsonb_build_object('status','DONE'))::json WHERE id='dep'`); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, co, tasks.Create{ID: "work", ProjectID: "p", Title: "work", Assignee: "demo", Dependencies: []string{"dep"}, Ready: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Dispatch(ctx, co, task.ID, task.Version, protocol.RunSpec{ID: "stale-run", Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex}); err != nil {
		t.Fatal(err)
	}
	// The dependency reopens after dispatch.
	if _, err = s.DB.Exec(`UPDATE tasks SET status='READY',body=(body::jsonb||jsonb_build_object('status','READY'))::json WHERE id='dep'`); err != nil {
		t.Fatal(err)
	}
	d := &fake.Driver{}
	w := Worker{Driver: d}
	asNode(t, s, &w, server.URL)
	if retry := w.ProcessRun(ctx, "stale-run", nil); retry {
		t.Fatal("a dropped claim was reported as retry")
	}
	if len(d.Launches()) != 0 {
		t.Fatal("the runtime started for a stale command")
	}
	r, err := s.Get(ctx, co, "stale-run")
	if err != nil || r.Status != "ENDED" || r.Outcome != "stale" || r.StartedAt != 0 {
		t.Fatalf("%+v %v", r, err)
	}
}

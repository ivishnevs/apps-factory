package main

import (
	"context"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

// TestTaskAuthorityCommands (hierarchy 1.3): the owner pauses, resumes, reassigns, reprioritizes and cascades a
// cancel through factory tasks, and says something to the default direct report.
func TestTaskAuthorityCommands(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	catalog, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &core.Core{Store: s, Agents: catalog.Agents, DefaultReport: catalog.DefaultReport, Token: "owner"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	api := worker.API{URL: server.URL, Token: "owner"}
	co := storage.DefaultCompany
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/authority-commands"}); err != nil {
		t.Fatal(err)
	}
	for _, create := range []tasks.Create{{ID: "a", ProjectID: "p", Title: "a", Assignee: "demo", Ready: true}, {ID: "kid", ProjectID: "p", Title: "kid", Assignee: "demo", ParentTaskID: "a"}} {
		if _, err = s.CreateTask(ctx, co, create); err != nil {
			t.Fatal(err)
		}
	}
	version := func() string {
		v, _ := s.Task(ctx, co, "a")
		return strconv.Itoa(v.Version)
	}
	tasksCmd := func(args ...string) error {
		return engineCommand(ctx, api, append([]string{"tasks"}, args...))
	}
	capture(t, func() error { return tasksCmd("pause", "a", version(), "hold") })
	if a, _ := s.Task(ctx, co, "a"); a.PausedBy != "owner" {
		t.Fatalf("%+v", a)
	}
	capture(t, func() error { return tasksCmd("resume", "a", version()) })
	capture(t, func() error { return tasksCmd("reassign", "a", version(), "claude-demo", "faster") })
	capture(t, func() error { return tasksCmd("reprioritize", "a", version(), "7") })
	if a, _ := s.Task(ctx, co, "a"); a.PausedBy != "" || a.Assignee != "claude-demo" || a.Priority != 7 {
		t.Fatalf("%+v", a)
	}
	if err = tasksCmd("cancel", "a", version()); err == nil || !strings.Contains(err.Error(), "needs_cascade") {
		t.Fatal(err)
	}
	capture(t, func() error { return tasksCmd("cancel", "a", version(), "obsolete", "--cascade") })
	if kid, _ := s.Task(ctx, co, "kid"); kid.Status != "CANCELLED" {
		t.Fatal(kid.Status)
	}
	if out := capture(t, func() error {
		return communicationCommand(ctx, api, []string{"conversations", "say", "p", "Plan the release"})
	}); !strings.Contains(out, `"recipient": "manager"`) {
		t.Fatal(out)
	}
}

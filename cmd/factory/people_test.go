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

// TestPersonTaskCommands (people-assignees 4.1): the owner's CLI lists "my" tasks (the company's owners for a token)
// and moves a person's task: start, submit-pr, and done after the review with --merged.
func TestPersonTaskCommands(t *testing.T) {
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
	c := &core.Core{Store: s, Agents: catalog.Agents, Teams: catalog.Teams, Token: "owner"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	api := worker.API{URL: server.URL, Token: "owner"}
	co := storage.DefaultCompany
	if err = s.CreateUser(ctx, storage.DefaultAccount, "ilya", "ilya@example.com"); err != nil {
		t.Fatal(err)
	}
	if err = s.AddMember(ctx, co, "ilya", storage.RoleOwner); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Team: "core", Workspace: "/tmp/person-commands"}); err != nil {
		t.Fatal(err)
	}
	for _, create := range []tasks.Create{{ID: "mine", ProjectID: "p", Title: "mine", Assignee: "member:ilya", ReviewBy: "owner", Ready: true}, {ID: "theirs", ProjectID: "p", Title: "theirs", Assignee: "demo", Ready: true}} {
		if _, err = s.CreateTask(ctx, co, create); err != nil {
			t.Fatal(err)
		}
	}
	tasksCmd := func(args ...string) string {
		return capture(t, func() error { return engineCommand(ctx, api, append([]string{"tasks"}, args...)) })
	}
	version := func() string {
		v, _ := s.Task(ctx, co, "mine")
		return strconv.Itoa(v.Version)
	}
	if out := tasksCmd("list", "--assignee", "me"); !strings.Contains(out, `"mine"`) || strings.Contains(out, `"theirs"`) {
		t.Fatal(out)
	}
	if out := tasksCmd("list", "p", "--assignee", "demo"); strings.Contains(out, `"mine"`) || !strings.Contains(out, `"theirs"`) {
		t.Fatal(out)
	}
	tasksCmd("start", "mine", version())
	tasksCmd("submit-pr", "mine", version(), "#12", "laid out the menu")
	v, _ := s.Task(ctx, co, "mine")
	if v.Status != "IN_REVIEW" || v.Submission == nil || v.Submission.PullRequest.Number != 12 {
		t.Fatalf("%+v", v)
	}
	// Without explicit exits the owner's accept is the end, as for an agent's task.
	tasksCmd("review", "mine", version(), "accept")
	if v, _ = s.Task(ctx, co, "mine"); v.Status != "DONE" {
		t.Fatalf("%+v", v)
	}
	if err = engineCommand(ctx, api, []string{"tasks", "done", "mine", version(), "--merged"}); err == nil {
		t.Fatal("done twice")
	}
}

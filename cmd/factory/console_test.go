package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

func capture(t *testing.T, fn func() error) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	runErr := fn()
	os.Stdout = stdout
	w.Close()
	var b bytes.Buffer
	_, _ = io.Copy(&b, r)
	if runErr != nil {
		t.Fatal(runErr, b.String())
	}
	return b.String()
}

func TestConsoleCommandsAgainstCore(t *testing.T) {
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
	c := &core.Core{Store: s, Agents: agents, Token: "owner"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	api := worker.API{URL: server.URL, Token: "owner"}
	co := storage.DefaultCompany
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "Calories", Workspace: "/tmp/console"}); err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateTask(ctx, co, tasks.Create{ID: "a", ProjectID: "p", Title: "Build screen", Assignee: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TaskAction(ctx, co, "a", tasks.Action{Version: a.Version, Action: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "b", ProjectID: "p", Title: "Test screen", Assignee: "demo", Dependencies: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Task(ctx, co, "a")
	if _, err = s.Claim(ctx, co, a.CurrentRunID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SendMessage(ctx, co, messaging.Send{ProjectID: "p", TaskID: "a", RunID: a.CurrentRunID, Sender: "demo", Recipient: "owner", MessageType: "question", Body: "Metric or imperial?", Blocking: true, RequestID: "q"}); err != nil {
		t.Fatal(err)
	}

	status := capture(t, func() error { return consoleCommand(ctx, api, []string{"status"}) })
	for _, want := range []string{"p (Calories)", "BACKLOG 1", "IN_PROGRESS 1", "inbox: 1", a.CurrentRunID, "RUNNING", "b ", "backlog"} {
		if !strings.Contains(status, want) {
			t.Errorf("status lacks %q:\n%s", want, status)
		}
	}
	log := capture(t, func() error { return consoleCommand(ctx, api, []string{"log", "a"}) })
	for _, want := range []string{"task.created", "task.trigger.ready", "task.started", "! task.parked.ask"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, " b ") {
		t.Errorf("log of a shows task b:\n%s", log)
	}
	attention, _, err := feedAll(ctx, api, map[string][]string{"attention": {"1"}}, 0, 0)
	if err != nil || len(attention) == 0 {
		t.Fatal(attention, err)
	}
	for _, item := range attention {
		if !item.Attention {
			t.Fatal("attention filter leaked", item)
		}
	}
	why := capture(t, func() error { return engineCommand(ctx, api, []string{"tasks", "why", "--project", "p"}) })
	for _, want := range []string{"TASK", "REASON", "backlog", "live_run"} {
		if !strings.Contains(why, want) {
			t.Errorf("why lacks %q:\n%s", want, why)
		}
	}
	if _, err = s.CommentTask(ctx, co, "b", tasks.Comment{Text: "wait for the design"}); err != nil {
		t.Fatal(err)
	}
	showB := capture(t, func() error { return consoleCommand(ctx, api, []string{"show", "b"}) })
	for _, want := range []string{"b — Test screen  [BACKLOG]", "why: backlog", "owner: factory tasks ready b VERSION", "dependencies: a", "comments:", "owner: wait for the design", "recent events:", "task.trigger.owner_comment"} {
		if !strings.Contains(showB, want) {
			t.Errorf("show lacks %q:\n%s", want, showB)
		}
	}
	var detail taskDetail
	if err = json.Unmarshal([]byte(capture(t, func() error { return consoleCommand(ctx, api, []string{"show", "a", "--json"}) })), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Task["id"] != "a" || detail.Why.Reason != "live_run" || len(detail.Attempts) != 1 || len(detail.Events) == 0 {
		t.Fatalf("%+v", detail)
	}
	var projects []core.ProjectStatus
	if err = json.Unmarshal([]byte(capture(t, func() error { return consoleCommand(ctx, api, []string{"status", "--json"}) })), &projects); err != nil || len(projects) != 1 || projects[0].Inbox != 1 {
		t.Fatal(projects, err)
	}
	t.Log("\n" + status + "\n" + log + "\n" + why + "\n" + showB)
}

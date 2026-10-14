package worker

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver/fake"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/workspace"
)

// acceptExit is a scripted runtime command that records an accepted exit as the MCP adapter does.
func acceptExit(kind string) fake.Activity {
	return fake.Activity{Exec: []string{"sh", "-c", `printf '{"state":"accepted","kind":"` + kind + `"}' > "$FACTORY_EXIT_FILE"`}}
}

// fakeMCP is a stand-in factory-mcp executable: the fake driver never starts it.
func fakeMCP(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "factory-mcp")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadExitRecord(t *testing.T) {
	p := filepath.Join(t.TempDir(), "exit.json")
	if r := readExitRecord(p); r.State != "none" {
		t.Fatalf("missing file: %+v", r)
	}
	if err := os.WriteFile(p, []byte(`{"state":"accepted","kind":"submit","at":"2026-10-14T10:00:00Z","exit_sha":"abc"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := readExitRecord(p); r.State != "accepted" || r.Kind != "submit" || r.ExitSHA != "abc" {
		t.Fatalf("%+v", r)
	}
}

// A runtime stopped after its accepted exit (ExitGrace, or the run's own timeout meanwhile) ends
// COMPLETED without a failure outcome: the exit stands, it is not a timeout or a cancel.
func TestJetStreamStopAfterAcceptedExit(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	defer func(g time.Duration) { ExitGrace = g }(ExitGrace)
	hanging := fake.Script{SessionID: "s", Activity: []fake.Activity{acceptExit("submit")}}
	for name, c := range map[string]struct {
		grace   time.Duration
		mods    []any
		exit    bool
		status  string
		outcome string
	}{
		"exit grace":           {50 * time.Millisecond, nil, true, "COMPLETED", ""},
		"timeout after exit":   {time.Hour, []any{func(s *protocol.RunSpec) { s.TimeoutSeconds = 1 }}, true, "COMPLETED", ""},
		"timeout without exit": {50 * time.Millisecond, []any{func(s *protocol.RunSpec) { s.TimeoutSeconds = 1 }}, false, "CANCELLED", "timed_out"},
	} {
		t.Run(name, func(t *testing.T) {
			ExitGrace = c.grace
			sc := hanging
			if !c.exit {
				sc.Activity = nil
			}
			r, events := runDriver(t, url, Worker{Driver: &fake.Driver{Script: sc, MCP: true}, MCPBinary: fakeMCP(t)}, false, c.mods...)
			var terminal protocol.Event
			if err := json.Unmarshal(events[len(events)-1].Payload, &terminal); err != nil {
				t.Fatal(err)
			}
			want := "none"
			if c.exit {
				want = "accepted"
			}
			if terminal.Status != c.status || terminal.Outcome != c.outcome || terminal.ExitState != want {
				t.Fatalf("terminal %s/%s exit %s, want %s/%s exit %s", terminal.Status, terminal.Outcome, terminal.ExitState, c.status, c.outcome, want)
			}
			if c.exit && (r.Status != "COMPLETED" || r.Outcome == "timed_out" || r.Outcome == "failed") {
				t.Fatalf("run %s/%s", r.Status, r.Outcome)
			}
		})
	}
}

// A worktree run that submits at commit C and then keeps writing is reset to C at its seal; Core gets
// exit_sha = sealed_head = C, and the late work exists only on the WIP ref (task-worktrees 2.2).
func TestJetStreamWorktreeExitReset(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated JetStream")
	}
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	defer b.JS.DeleteStream("FACTORY_COMMANDS")
	defer b.JS.DeleteStream("FACTORY_EVENTS")
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
	demo.Spec.Workspace.Mode = "worktree"
	agents["demo"] = demo
	c := &core.Core{Store: s, Bus: b, Agents: agents, Token: "test"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	api := API{URL: server.URL, Token: "test"}
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	engineDone := make(chan error, 1)
	go func() { engineDone <- c.Run(ctx) }()
	root := t.TempDir()
	trees, err := workspace.NewWorktrees(workspace.WorktreesConfig{Enabled: true, Root: root, ScanCommand: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	git := []string{"git", "-c", "user.name=t", "-c", "user.email=t@t"}
	sc := fake.Script{SessionID: "s", Status: "COMPLETED", Text: "submitted", Activity: []fake.Activity{
		{Exec: []string{"sh", "-c", "echo feature > feature.txt"}},
		{Exec: []string{"git", "add", "feature.txt"}},
		{Exec: append(append([]string{}, git...), "commit", "-qm", "feature")},
		acceptExit("submit"),
		{Exec: []string{"sh", "-c", "echo late > late.txt"}},
	}}
	w := Worker{Bus: b, API: api, Driver: &fake.Driver{Script: sc, MCP: true}, MCPBinary: fakeMCP(t), Worktrees: trees, NodeID: "mini-1", ShimBinary: buildShim(t)}
	workerDone := make(chan error, 1)
	go func() { workerDone <- w.Run(ctx) }()
	defer func() { stop(); <-workerDone; <-engineDone }()

	remote := bareRemote(t)
	var project tasks.Project
	if err = api.Do(ctx, "POST", "/projects", tasks.ProjectCreate{ID: "wt", Name: "WT", Workspace: t.TempDir(), RepoURL: remote, DefaultBranch: "main"}, &project); err != nil {
		t.Fatal(err)
	}
	var task tasks.Task
	if err = api.Do(ctx, "POST", "/tasks", tasks.Create{ProjectID: project.ID, Title: "work", Assignee: "demo", RequestID: "work"}, &task); err != nil {
		t.Fatal(err)
	}
	if err = api.Do(ctx, "POST", "/tasks/"+task.ID+"/actions", tasks.Action{Version: task.Version, Action: "ready"}, &task); err != nil {
		t.Fatal(err)
	}
	var ws tasks.Workspace
	for ctx.Err() == nil {
		if err = api.Do(ctx, "GET", "/tasks/"+task.ID+"/workspace", nil, &ws); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, storage.DefaultCompany, project.ID, "tasks", task.ID)
	exitCommit := gitOut(t, path, "rev-parse", "HEAD")
	if gitOut(t, path, "log", "-1", "--format=%s") != "feature" {
		t.Fatal("HEAD is not the exit's commit")
	}
	if ws.ExitSHA != exitCommit || ws.SealedHead != exitCommit || ws.NodeID != "mini-1" || ws.Branch != "af/"+task.ID {
		t.Fatalf("workspace %+v, exit commit %s", ws, exitCommit)
	}
	if st := gitOut(t, path, "status", "--porcelain"); st != "" {
		t.Fatalf("not clean after the reset: %q", st)
	}
	wip := workspace.WIPRef(task.ID)
	if gitOut(t, path, "show", wip+":late.txt") != "late" || gitOut(t, path, "rev-parse", wip+"^") != exitCommit {
		t.Fatal("work after the exit not on the WIP ref")
	}
}

// Git lock contention that outlives the allocator's retries fails the run retryably (failed), not as
// setup_failed (task-worktrees D1).
func TestJetStreamWorktreeLockError(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	remote := bareRemote(t)
	root := t.TempDir()
	trees, err := workspace.NewWorktrees(workspace.WorktreesConfig{Enabled: true, Root: root, ScanCommand: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	req := workspace.Request{Company: storage.DefaultCompany, Project: "fake-project", Task: "t1", RepoURL: remote, DefaultBranch: "main"}
	if _, err = trees.Ensure(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	// A stale config.lock: every `git config` of the clone fails with a lock error.
	repo := filepath.Join(root, storage.DefaultCompany, "fake-project", "repo")
	if err = os.WriteFile(filepath.Join(repo, "config.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	d := &fake.Driver{Script: fake.Script{Status: "COMPLETED"}}
	r, events := runDriver(t, url, Worker{Driver: d, Worktrees: trees}, false, func(s *protocol.RunSpec) {
		s.TaskID, s.RepoURL, s.DefaultBranch, s.WorkspaceMode = "t1", remote, "main", "worktree"
	})
	var terminal protocol.Event
	if err = json.Unmarshal(events[len(events)-1].Payload, &terminal); err != nil {
		t.Fatal(err)
	}
	if terminal.Status != "FAILED" || terminal.Outcome != "failed" || !strings.Contains(terminal.Result, "lock") || len(d.Launches()) != 0 {
		t.Fatalf("terminal %+v", terminal)
	}
	if r.Status != "FAILED" || r.Outcome != "failed" {
		t.Fatalf("run %s/%s", r.Status, r.Outcome)
	}
}

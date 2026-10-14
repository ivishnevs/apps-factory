package worker

import (
	"appfactory/internal/bus"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver/fake"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/workspace"
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// bareRemote is a local bare repository with one commit on main.
func bareRemote(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote.git")
	gitOut(t, dir, "init", "--quiet", "--bare", "-b", "main", remote)
	seed := filepath.Join(dir, "seed")
	gitOut(t, dir, "clone", "--quiet", remote, seed)
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, seed, "add", ".")
	gitOut(t, seed, "commit", "--quiet", "-m", "init")
	gitOut(t, seed, "push", "--quiet", "origin", "HEAD:main")
	return remote
}

func TestWorktreeRequest(t *testing.T) {
	trees, err := workspace.NewWorktrees(workspace.WorktreesConfig{Enabled: true, Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	spec := protocol.RunSpec{ID: "r", Project: "p", TaskID: "t1", RepoURL: "/repo.git", DefaultBranch: "main", WorkspaceMode: "worktree"}
	r, ok := (&Worker{Worktrees: trees}).worktreeRequest(spec)
	if !ok || r.Company != "default" || r.Project != "p" || r.Task != "t1" || r.Branch != "af/t1" || r.RepoURL != "/repo.git" || r.DefaultBranch != "main" {
		t.Fatalf("%+v %v", r, ok)
	}
	for name, s := range map[string]protocol.RunSpec{
		"shared":  {WorkspaceMode: "shared", TaskID: "t1", RepoURL: "/repo.git"},
		"scratch": {WorkspaceMode: "scratch", TaskID: "t1", RepoURL: "/repo.git"},
		"empty":   {TaskID: "t1", RepoURL: "/repo.git"},
		"no task": {WorkspaceMode: "worktree", RepoURL: "/repo.git"},
		"no repo": {WorkspaceMode: "worktree", TaskID: "t1"},
	} {
		if _, ok = (&Worker{Worktrees: trees}).worktreeRequest(s); ok {
			t.Errorf("%s: worktree", name)
		}
	}
	if _, ok = (&Worker{}).worktreeRequest(spec); ok {
		t.Error("worktrees disabled: worktree")
	}
	if env := setEnv([]string{"A=1", "FACTORY_SCRATCH=/old"}, "FACTORY_SCRATCH", "/new"); strings.Join(env, ",") != "A=1,FACTORY_SCRATCH=/new" {
		t.Fatal(env)
	}
}

// Runs of one task share its worktree and are sealed onto the local WIP ref; another task gets its own
// worktree; shared mode keeps the project workspace.
func TestJetStreamWorktreeRuns(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	sc, err := fake.Load(filepath.Join("testdata", "fake", "completed.json"))
	if err != nil {
		t.Fatal(err)
	}
	remote := bareRemote(t)
	trees, err := workspace.NewWorktrees(workspace.WorktreesConfig{Enabled: true, Root: t.TempDir(), ScanCommand: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	run := func(task, mode string, files map[string]string) (string, []string) {
		t.Helper()
		d := &fake.Driver{Script: sc, Files: files, Command: []string{"sleep", "60"}}
		r, _ := runDriver(t, url, Worker{Driver: d, Worktrees: trees}, false, func(s *protocol.RunSpec) {
			s.TaskID, s.RepoURL, s.DefaultBranch, s.WorkspaceMode = task, remote, "main", mode
		})
		l := d.Launches()
		if r.Status != "COMPLETED" || len(l) != 1 {
			t.Fatalf("%s: %+v, %d launches", task, r, len(l))
		}
		return l[0].Cwd, l[0].Env
	}
	cwd, env := run("t1", "worktree", map[string]string{"draft.txt": "unsaved work\n"})
	if !strings.HasSuffix(cwd, filepath.Join("default", "fake-project", "tasks", "t1")) {
		t.Fatalf("cwd %s", cwd)
	}
	scratch := filepath.Join(filepath.Dir(filepath.Dir(cwd)), "scratch", "t1")
	if !slices.Contains(env, "FACTORY_SCRATCH="+scratch) {
		t.Fatalf("FACTORY_SCRATCH missing: %s", scratch)
	}
	if b := gitOut(t, cwd, "rev-parse", "--abbrev-ref", "HEAD"); b != "af/t1" {
		t.Fatalf("branch %s", b)
	}
	head := gitOut(t, cwd, "rev-parse", "HEAD")
	wip := gitOut(t, cwd, "rev-parse", workspace.WIPRef("t1"))
	if gitOut(t, cwd, "show", wip+":draft.txt") != "unsaved work" || gitOut(t, cwd, "rev-parse", wip+"^") != head {
		t.Fatal("WIP ref lacks the run's changes")
	}
	if gitOut(t, cwd, "status", "--porcelain") != "?? draft.txt" {
		t.Fatal("seal changed the worktree")
	}
	if _, err = os.Stat(filepath.Join(cwd, ".factory", "run.pid")); !os.IsNotExist(err) {
		t.Fatalf("run group still tracked: %v", err)
	}

	again, _ := run("t1", "worktree", map[string]string{"more.txt": "more\n"})
	if again != cwd {
		t.Fatalf("same task got %s, want %s", again, cwd)
	}
	if _, err = os.Stat(filepath.Join(cwd, "draft.txt")); err != nil {
		t.Fatal("next run lost the previous run's work")
	}
	if gitOut(t, cwd, "show", workspace.WIPRef("t1")+":more.txt") != "more" {
		t.Fatal("second seal missing")
	}

	other, _ := run("t2", "worktree", nil)
	if other == cwd || !strings.HasSuffix(other, filepath.Join("tasks", "t2")) || gitOut(t, other, "rev-parse", "--abbrev-ref", "HEAD") != "af/t2" {
		t.Fatalf("other task in %s", other)
	}
	if _, err = os.Stat(filepath.Join(other, "draft.txt")); !os.IsNotExist(err) {
		t.Fatal("tasks share files")
	}

	shared, env := run("t3", "shared", nil)
	if strings.Contains(shared, "tasks") || env != nil {
		t.Fatalf("shared mode ran in %s with env %v", shared, env != nil)
	}

	// HEAD moved outside any run: the start check fails the run before the runtime starts and leaves the
	// worktree as it is; the run-stop callback still sees the run.
	gitOut(t, cwd, "add", "-A")
	gitOut(t, cwd, "commit", "--quiet", "-m", "outside a run")
	moved := gitOut(t, cwd, "rev-parse", "HEAD")
	d := &fake.Driver{Script: sc}
	var stopped []string
	r, _ := runDriver(t, url, Worker{Driver: d, Worktrees: trees, RunStopped: func(s protocol.RunSpec, wt string) { stopped = append(stopped, s.TaskID+":"+wt) }}, false, func(s *protocol.RunSpec) {
		s.TaskID, s.RepoURL, s.DefaultBranch, s.WorkspaceMode = "t1", remote, "main", "worktree"
	})
	if r.Status != "FAILED" || !strings.Contains(r.Result, "workspace_diverged") || !strings.Contains(r.Result, "HEAD changed outside any run") || len(d.Launches()) != 0 {
		t.Fatalf("%+v, %d launches", r, len(d.Launches()))
	}
	if gitOut(t, cwd, "rev-parse", "HEAD") != moved || len(stopped) != 1 || stopped[0] != "t1:" {
		t.Fatalf("worktree touched or stop callback %v", stopped)
	}
}

// A task run of a worktree agent, dispatched by a real Core, reports its sealed workspace to Core.
func TestJetStreamWorktreeReport(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated JetStream")
	}
	sc, err := fake.Load(filepath.Join("testdata", "fake", "completed.json"))
	if err != nil {
		t.Fatal(err)
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
	w := Worker{Bus: b, API: api, Driver: &fake.Driver{Script: sc, Files: map[string]string{"draft.txt": "x\n"}}, Worktrees: trees, NodeID: "mini-1", ShimBinary: buildShim(t)}
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
	head := gitOut(t, path, "rev-parse", "HEAD")
	if ws.Mode != "worktree" || ws.Branch != "af/"+task.ID || ws.NodeID != "mini-1" || ws.SealedHead != head || ws.SealedAt == 0 || ws.RunID == "" {
		t.Fatalf("workspace %+v, head %s", ws, head)
	}
	if gitOut(t, path, "show", workspace.WIPRef(task.ID)+":draft.txt") != "x" {
		t.Fatal("WIP ref lacks the run's changes")
	}
}

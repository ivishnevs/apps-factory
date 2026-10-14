package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/procgroup"
	"appfactory/internal/protocol"
	"appfactory/internal/workspace"
)

// launchHook calls at before each launch, so a test sees the workspace as the runtime would find it.
type launchHook struct {
	*fake.Driver
	at func(driver.Launch)
}

func (d launchHook) Launch(ctx context.Context, l driver.Launch) (driver.Session, error) {
	d.at(l)
	return d.Driver.Launch(ctx, l)
}

// TestJetStreamWorktreeNextRunWaitsForGroupAndSeal (task-worktrees 1.3): two slots of a node get runs of the
// same task. The second run waits while the first run's runtime process group is alive; when the first run
// is stopped, its group is killed and its worktree sealed before the second run's runtime starts.
func TestJetStreamWorktreeNextRunWaitsForGroupAndSeal(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	hanging, err := fake.Load(filepath.Join("testdata", "fake", "hanging.json"))
	if err != nil {
		t.Fatal(err)
	}
	completed, err := fake.Load(filepath.Join("testdata", "fake", "completed.json"))
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
	// Core's HTTP side as the executor needs it here: the sealed workspace reports.
	var reports atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/workspace") {
			reports.Add(1)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()

	remote := bareRemote(t)
	trees, err := workspace.NewWorktrees(workspace.WorktreesConfig{Enabled: true, Root: t.TempDir(), ScanCommand: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	api := API{URL: server.URL, Token: "test-token"}
	start := func(id string, d driver.Driver, runCtx context.Context) chan error {
		t.Helper()
		spec := protocol.RunSpec{ID: id, Project: "fake-project", Agent: "demo", Provider: d.Name(), Workspace: t.TempDir(), Prompt: "work", Instructions: "base", TimeoutSeconds: 30,
			TaskID: "t1", RepoURL: remote, DefaultBranch: "main", WorkspaceMode: "worktree"}
		// Executor level: v1 Core would not claim two runs of one project at once (a node's v2 claim does),
		// so the runs are executed directly; their events go to JetStream unapplied.
		w := Worker{Bus: b, API: api, Driver: d, Worktrees: trees}
		done := make(chan error, 1)
		go func() { done <- w.execute(runCtx, spec, "lease-"+id, "cmd-"+id) }()
		return done
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for !ok() {
			select {
			case <-ctx.Done():
				t.Fatal("timeout waiting for", what)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}

	// Run 1: a runtime whose process group has a child, writing unsaved work, running until stopped.
	first := &fake.Driver{Script: hanging, Files: map[string]string{"first.txt": "first run\n"}, Command: []string{"sh", "-c", "sleep 60 & wait"}}
	ctx1, stop1 := context.WithCancel(ctx)
	defer stop1()
	done1 := start("run-1", first, ctx1)
	waitFor("first launch", func() bool { return len(first.Launches()) == 1 })
	path := first.Launches()[0].Cwd
	pidfile := filepath.Join(path, ".factory", "run.pid")
	waitFor("first run's group and work", func() bool {
		_, e1 := os.Stat(pidfile)
		_, e2 := os.Stat(filepath.Join(path, "first.txt"))
		return e1 == nil && e2 == nil
	})
	// The group the worktree tracks is the runtime's, with a child besides its leader.
	var group procgroup.Group
	if raw, err := os.ReadFile(pidfile); err != nil || json.Unmarshal(raw, &group) != nil || group.ID <= 1 {
		t.Fatalf("pidfile %s: %v", raw, err)
	}
	waitFor("the runtime's child", func() bool {
		out, _ := exec.Command("pgrep", "-g", strconv.Itoa(group.ID)).Output()
		return len(strings.Fields(string(out))) >= 2
	})
	alive := func() bool { return procgroup.Alive(group.ID) }

	// Run 2 of the same task on another slot: what its runtime finds when it is launched.
	var mu sync.Mutex
	var seen struct {
		launched, groupAlive, pidfile bool
		wip                           string
	}
	second := launchHook{Driver: &fake.Driver{Script: completed, Files: map[string]string{"second.txt": "second run\n"}}, at: func(l driver.Launch) {
		mu.Lock()
		defer mu.Unlock()
		seen.launched, seen.groupAlive = true, alive()
		_, err := os.Stat(pidfile)
		seen.pidfile = err == nil
		seen.wip = gitOut(t, l.Cwd, "show", workspace.WIPRef("t1")+":first.txt")
	}}
	done2 := start("run-2", second, ctx)
	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	launched := seen.launched
	mu.Unlock()
	if launched || len(second.Launches()) != 0 {
		t.Fatal("the second run started while the first held the worktree")
	}
	if !alive() {
		t.Fatal("first run's group gone before it was stopped")
	}

	stop1() // Core stops the first run (owner cancel)
	if err = <-done1; err != nil {
		t.Fatal("first run:", err)
	}
	if err = <-done2; err != nil {
		t.Fatal("second run:", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !seen.launched || seen.groupAlive || seen.pidfile || seen.wip != "first run" {
		t.Fatalf("at the second launch: %+v", seen)
	}
	if l := second.Launches(); len(l) != 1 || l[0].Cwd != path {
		t.Fatalf("second run launched %+v", l)
	}
	if n := reports.Load(); n != 2 {
		t.Fatalf("%d sealed workspace reports, want 2", n)
	}
	if gitOut(t, path, "show", workspace.WIPRef("t1")+":second.txt") != "second run" || gitOut(t, path, "show", workspace.WIPRef("t1")+":first.txt") != "first run" {
		t.Fatal("second seal lacks the runs' work")
	}
}

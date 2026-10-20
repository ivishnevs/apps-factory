package runner

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/procgroup"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

// TestHelperCrashableNode is the node process TestJetStreamOrphanGoneBeforeRetry starts and kills: a v2
// node whose runtime is a real process group (a shell with a child) that runs until stopped.
func TestHelperCrashableNode(t *testing.T) {
	if os.Getenv("FACTORY_HELPER_NODE") != "1" {
		t.Skip("helper process")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	b, err := bus.Connect(os.Getenv("NATS_TEST_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	cfg := node.Defaults()
	cfg.Name, cfg.Slots, cfg.StateDir, cfg.ProtocolVersion = "crash-node", 1, os.Getenv("FACTORY_HELPER_STATE"), 2
	d := &fake.Driver{Command: []string{"sh", "-c", "sleep 60 & wait"}}
	if err = Run(ctx, Node{Config: cfg, Bus: b, API: worker.API{URL: os.Getenv("FACTORY_HELPER_CORE"), Token: "test-token"}, Drivers: []driver.Driver{perRunSessions{d}}, FetchWait: 200 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
}

// TestJetStreamOrphanGoneBeforeRetry (reliable-recovery 3.3): the node process is killed mid-run, so its
// runtime's process group survives as an orphan. While the node is unreachable the task is not retried. The
// restarted node kills the orphaned group before it registers its new epoch; only then does Core end the
// old run lost (no budget) and let the task run again, and the retry is claimed with the old group gone.
func TestJetStreamOrphanGoneBeforeRetry(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to a dedicated JetStream instance")
	}
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	defer b.JS.DeleteStream("FACTORY_COMMANDS")
	defer b.JS.DeleteStream("FACTORY_EVENTS")
	beat := storage.HeartbeatSeconds
	storage.HeartbeatSeconds = 1
	defer func() { storage.HeartbeatSeconds = beat }()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../../..")
	if err != nil {
		t.Fatal(err)
	}
	demo := agents["demo"]
	demo.Rules = nil // no guarded commands: the node needs no factory-shim build
	agents["demo"] = demo
	c := &core.Core{Store: s, Bus: b, Agents: agents, Token: "test-token"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	go c.Run(ctx)
	co := storage.DefaultCompany
	state := t.TempDir()
	start := func() *exec.Cmd {
		t.Helper()
		bin, _ := os.Executable()
		cmd := exec.Command(bin, "-test.run=^TestHelperCrashableNode$")
		cmd.Env = append(os.Environ(), "FACTORY_HELPER_NODE=1", "FACTORY_HELPER_STATE="+state, "FACTORY_HELPER_CORE="+server.URL, "NATS_TEST_URL="+url)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	groups := map[int]bool{}
	t.Cleanup(func() {
		for g := range groups {
			_ = syscall.Kill(-g, syscall.SIGKILL)
		}
	})
	groupOf := func(run string) int {
		var p struct {
			Group int `json:"group"`
		}
		if raw, err := os.ReadFile(filepath.Join(state, run+".pid.json")); err == nil {
			_ = json.Unmarshal(raw, &p)
		}
		if p.Group > 0 {
			groups[p.Group] = true
		}
		return p.Group
	}
	wait := func(what string, ok func() bool) {
		t.Helper()
		for !ok() {
			select {
			case <-ctx.Done():
				t.Fatal("timeout waiting for", what)
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	epoch := func() int64 {
		ns, _ := s.Nodes(ctx, co)
		if len(ns) != 1 {
			return 0
		}
		return ns[0].Epoch
	}
	run := func(id string) storage.Run {
		r, _ := s.Get(ctx, co, id)
		return r
	}

	first := start()
	wait("register", func() bool { return epoch() == 1 })
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, co, tasks.Create{ID: "t", ProjectID: "p", Title: "t", Assignee: "demo", Ready: true, RequestID: "t"})
	if err != nil {
		t.Fatal(err)
	}
	var old string
	wait("the first run's runtime", func() bool {
		task, _ = s.Task(ctx, co, "t")
		old = task.CurrentRunID
		return old != "" && run(old).Status == "RUNNING" && groupOf(old) > 0
	})
	group := groupOf(old)
	wait("the runtime's child", func() bool {
		out, _ := exec.Command("pgrep", "-g", strconv.Itoa(group)).Output()
		return len(strings.Fields(string(out))) >= 2
	})

	// The node process dies without stopping its runtime: the group is orphaned and still runs.
	if err = first.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = first.Wait()
	time.Sleep(2 * time.Second) // a few Core ticks with the node unreachable
	if !procgroup.Alive(group) {
		t.Fatal("the orphan died with its node; the test proves nothing")
	}
	if task, _ = s.Task(ctx, co, "t"); task.CurrentRunID != old {
		t.Fatalf("retried while the old runtime may still run: %+v", task)
	}

	// The restarted node kills the orphan before it registers epoch 2.
	second := start()
	defer func() {
		_ = second.Process.Signal(syscall.SIGTERM)
		_ = second.Wait()
	}()
	wait("epoch 2", func() bool { return epoch() == 2 })
	if procgroup.Alive(group) {
		t.Fatal("epoch 2 registered while the orphaned runtime still runs")
	}
	wait("the old run lost", func() bool { return run(old).Status == "ENDED" })
	if r := run(old); r.Outcome != "lost" || !strings.Contains(r.Result, "restarted (epoch 2)") {
		t.Fatalf("%+v", r)
	}
	var used int
	if err = s.DB.QueryRow("SELECT coalesce(sum(used),0) FROM task_budget WHERE task_id='t'").Scan(&used); err != nil || used != 0 {
		t.Fatal("a new-epoch loss counted against the budget", used, err)
	}
	// The owner's directive ends the backoff; the retry runs on the restarted node.
	if _, err = s.Directive(ctx, co, "t", "owner", "the node crashed; continue", false, "after-crash"); err != nil {
		t.Fatal(err)
	}
	var retry string
	wait("the retry claimed on epoch 2", func() bool {
		task, _ = s.Task(ctx, co, "t")
		retry = task.CurrentRunID
		if retry == old || retry == "" {
			return false
		}
		var e int64
		_ = s.DB.QueryRow("SELECT epoch FROM runs WHERE id=?", retry).Scan(&e)
		return e == 2
	})
	if procgroup.Alive(group) {
		t.Fatal("the retry was claimed while the orphaned runtime runs")
	}
	wait("the retry's runtime", func() bool { return groupOf(retry) > 0 })
}

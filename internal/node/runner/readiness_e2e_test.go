package runner

import (
	"context"
	"errors"
	"net/http/httptest"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

// TestNodeReadinessEndToEnd (node-readiness 5.1): real Core, real nodes over signed HTTP. Node A loses the project's
// required tool after its readiness check passed: its run of the task ends setup_failed as A's fault, the task's
// retries stay untouched, A gets a readiness fault and no runs of the project, and with only A the task's next run
// waits no_ready_node without escalating. Node B, ready, then takes the run; A passes its check again after the fix.
func TestNodeReadinessEndToEnd(t *testing.T) {
	beat := storage.HeartbeatSeconds
	storage.HeartbeatSeconds = 1
	defer func() { storage.HeartbeatSeconds = beat }()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	c := &core.Core{Store: s, Token: "test-token"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	go c.Run(ctx)
	co := storage.DefaultCompany
	d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "done"}}
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "game", Name: "game", Workspace: t.TempDir(), Requires: []tasks.Requirement{{Command: "godot", Version: "4.7.2"}}}); err != nil {
		t.Fatal(err)
	}
	// Each node's host has godot 4.7.2 until the test takes it away; every other command is the machine's own.
	start := func(godot *atomic.Bool) string {
		cfg := node.Defaults()
		cfg.Slots, cfg.StateDir, cfg.Poll.WaitSeconds = 1, t.TempDir(), 1
		api := enrolledIn(t, s, co, cfg.StateDir, server.URL)
		host := node.LocalHost(runtime.GOOS)
		real := host.Run
		host.Run = func(ctx context.Context, name string, args ...string) (string, string, error) {
			if name == "godot" {
				if godot.Load() {
					return "4.7.2.stable.official\n", "", nil
				}
				return "", "", errors.New(`exec: "godot": executable file not found in $PATH`)
			}
			return real(ctx, name, args...)
		}
		nodeCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			done <- Run(nodeCtx, Node{Config: cfg, API: api, Drivers: []driver.Driver{perRunSessions{d}}, Host: host})
		}()
		t.Cleanup(func() { stop(); <-done })
		return api.Node
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
	readiness := func(node string) []storage.ProjectReadiness {
		rs, err := s.NodesReadiness(ctx, co)
		if err != nil {
			t.Fatal(err)
		}
		return rs[node]
	}
	ready := func(node string) bool {
		rs := readiness(node)
		return len(rs) == 1 && rs[0].Ready && rs[0].CheckedAt > 0 && !rs[0].Pending
	}

	var godotA, godotB atomic.Bool
	godotA.Store(true)
	godotB.Store(true)
	a := start(&godotA)
	wait("node A checked the project", func() bool { return ready(a) })
	godotA.Store(false) // uninstalled after the check: only a run finds out

	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "t", ProjectID: "game", Title: "t", Assignee: "dev", Ready: true, RequestID: "t"}); err != nil {
		t.Fatal(err)
	}
	dispatch := func(id string) {
		t.Helper()
		v, err := s.Task(ctx, co, "t")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: id, Project: "game", Agent: "dev", Provider: protocol.RuntimeCodex, Prompt: "work", TimeoutSeconds: 60}); err != nil {
			t.Fatal(err)
		}
	}
	dispatch("r1")
	var r storage.Run
	wait("r1 ended", func() bool { r, _ = s.Get(ctx, co, "r1"); return r.Status == "ENDED" })
	var fault, ranOn string
	if err = s.DB.QueryRow("SELECT node_fault,node_id FROM runs WHERE id='r1'").Scan(&fault, &ranOn); err != nil {
		t.Fatal(err)
	}
	if r.Outcome != "setup_failed" || fault != protocol.FaultTool || ranOn != a || !strings.Contains(r.Result, "godot") {
		t.Fatalf("r1: outcome %s fault %q node %s result %q", r.Outcome, fault, ranOn, r.Result)
	}
	v, _ := s.Task(ctx, co, "t")
	if v.AttemptsNoProgress != 0 || v.FailStreak != 0 || v.NotBefore != 0 {
		t.Fatalf("node fault counted against the task: attempts %d streak %d not_before %d", v.AttemptsNoProgress, v.FailStreak, v.NotBefore)
	}
	// A re-checks at once (a tool fault) and stays not ready, now from its own check.
	wait("A's check confirms the fault", func() bool {
		rs := readiness(a)
		return len(rs) == 1 && !rs[0].Ready && rs[0].Source == "probe"
	})

	// Only A: the next run waits no_ready_node naming A and the fix, and nothing escalates.
	dispatch("r2")
	time.Sleep(3 * time.Second) // A polls and beats meanwhile
	if r, _ = s.Get(ctx, co, "r2"); r.Status != "QUEUED" {
		t.Fatalf("r2 left the queue on a node that is not ready: %+v", r)
	}
	q, err := s.QueuedWhy(ctx, co, "r2")
	if err != nil || q == nil || q.Reason != storage.QueuedNoReadyNode || !strings.Contains(q.Detail, "godot") || !strings.Contains(q.Detail, "fix:") {
		t.Fatalf("queued why %+v %v", q, err)
	}
	if es, _ := s.Escalations(ctx, co, true); len(es) != 0 {
		t.Fatalf("escalated before the fault was fixed: %+v", es)
	}

	// Node B is ready: it takes the run, which succeeds; the task's retries were never spent.
	b := start(&godotB)
	wait("r2 ended", func() bool { r, _ = s.Get(ctx, co, "r2"); return r.Status == "ENDED" })
	if err = s.DB.QueryRow("SELECT node_id FROM runs WHERE id='r2'").Scan(&ranOn); err != nil || r.Outcome != "succeeded" || ranOn != b {
		t.Fatalf("r2: %s on %s (b %s) %v", r.Outcome, ranOn, b, err)
	}

	// A's fix: godot back, Check again; A is ready for the project again.
	godotA.Store(true)
	if err = s.CheckNodeAgain(ctx, co, a); err != nil {
		t.Fatal(err)
	}
	wait("A ready again", func() bool { return ready(a) })
	var kinds []string
	rows, err := s.DB.Query("SELECT kind FROM feed_events WHERE kind IN ('node.not_ready','node.ready') ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		_ = rows.Scan(&k)
		kinds = append(kinds, k)
	}
	rows.Close()
	if !slices.Equal(kinds, []string{"node.not_ready", "node.ready"}) {
		t.Fatalf("feed %v", kinds)
	}
}

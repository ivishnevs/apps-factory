package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

const repoNotFound = "worktree: git clone https://github.com/o/game: exit status 128: remote: Repository not found."

// readinessFixture: project game (a repository and a required tool), task t of dev, its first run claimed by n1.
func readinessFixture(t *testing.T, nodes ...string) (*Store, func(time.Duration)) {
	t.Helper()
	s, fake := clockStore(t)
	ctx := context.Background()
	if _, err := s.CreateProject(ctx, DefaultCompany, tasks.ProjectCreate{ID: "game", Name: "game", Workspace: "/tmp/readiness-game", RepoURL: "https://github.com/o/game",
		Requires: []tasks.Requirement{{Command: "godot", Version: "4.7.2"}}}); err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		register(t, s, n, 1)
	}
	if _, err := s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: "t", ProjectID: "game", Title: "t", Assignee: "dev", Ready: true, RequestID: "t"}); err != nil {
		t.Fatal(err)
	}
	return s, fake.Advance
}

func dispatchRun(t *testing.T, s *Store, id string) {
	t.Helper()
	v, err := s.Task(context.Background(), DefaultCompany, "t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Dispatch(context.Background(), DefaultCompany, v.ID, v.Version, protocol.RunSpec{ID: id, Project: "game", Agent: "dev", Provider: protocol.RuntimeCodex}); err != nil {
		t.Fatal(err)
	}
}

func offered(t *testing.T, s *Store, node, run string) bool {
	t.Helper()
	offers, err := s.Offers(context.Background(), DefaultCompany, node, 1, map[string]int{"codex": 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range offers {
		if o.RunID == run {
			return true
		}
	}
	return false
}

func readyBeat(t *testing.T, s *Store, node string, reports []protocol.ProjectReadiness) HeartbeatAnswer {
	t.Helper()
	a, err := s.NodeHeartbeat(context.Background(), DefaultCompany, node, NodeHeartbeat{Epoch: 1, Inventory: []InventoryRun{}, Readiness: reports})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func check(a HeartbeatAnswer, project string) *protocol.ProjectCheck {
	for i := range a.Checks {
		if a.Checks[i].Project == project {
			return &a.Checks[i]
		}
	}
	return nil
}

// TestNodeFaultChargesTheNode (node-readiness 1.1, 1.2; 2026-11-07: three game tasks escalated retries_exhausted after
// new nodes without GitHub access kept failing their clones): a run that fails on the node's repository access does not
// count against its task, the task goes to another node at once, and the faulted node gets no runs of the project.
func TestNodeFaultChargesTheNode(t *testing.T) {
	s, _ := readinessFixture(t, "n1", "n2")
	ctx := context.Background()
	co := DefaultCompany
	dispatchRun(t, s, "r1")
	c, err := claimV2(s, "r1", "n1", 1, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.RunSpec.Requires) != 1 || c.RunSpec.Requires[0].Command != "godot" {
		t.Fatalf("claim answer lacks the project's requires: %+v", c.RunSpec.Requires)
	}
	if err = end(t, s, "r1", c.Lease, protocol.Event{Status: "ENDED", Outcome: "setup_failed", Result: repoNotFound}); err != nil {
		t.Fatal(err)
	}
	var outcome, fault string
	if err = s.DB.QueryRowContext(ctx, "SELECT outcome,node_fault FROM runs WHERE id='r1'").Scan(&outcome, &fault); err != nil || outcome != "setup_failed" || fault != protocol.FaultRepoAccess {
		t.Fatalf("run r1: %s %q %v", outcome, fault, err)
	}
	v, _ := s.Task(ctx, co, "t")
	if v.Status != "READY" || v.AttemptsNoProgress != 0 || v.FailStreak != 0 || v.NotBefore != 0 {
		t.Fatalf("the node's fault counted against the task: %s attempts %d streak %d not_before %d", v.Status, v.AttemptsNoProgress, v.FailStreak, v.NotBefore)
	}
	if es, _ := s.Escalations(ctx, co, true); len(es) != 0 {
		t.Fatalf("escalated: %+v", es)
	}
	var notReady int
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM feed_events WHERE company_id=? AND kind='node.not_ready' AND attention=1 AND summary LIKE '%Repository not found%gh auth login%'", co).Scan(&notReady); err != nil || notReady != 1 {
		t.Fatalf("node.not_ready events %d %v", notReady, err)
	}

	// The retry goes to n2 at once; n1 gets none of the project's runs.
	dispatchRun(t, s, "r2")
	if offered(t, s, "n1", "r2") || !offered(t, s, "n2", "r2") {
		t.Fatal("offers ignore the readiness fault")
	}
	refused(t, func() error { _, err := claimV2(s, "r2", "n1", 1, "c2"); return err }(), true, "not_ready")
	if _, err = claimV2(s, "r2", "n2", 1, "c3"); err != nil {
		t.Fatal(err)
	}
	rs, err := s.NodesReadiness(ctx, co)
	if err != nil || len(rs["n1"]) != 1 || rs["n1"][0].Ready || rs["n1"][0].Source != "run" || rs["n1"][0].RunID != "r1" || len(rs["n2"]) != 0 {
		t.Fatalf("readiness %+v %v", rs, err)
	}
}

// TestNoReadyNode (node-readiness 1.2, 5.1): with only the faulted node, the task waits no_ready_node naming the node and
// its fix and does not escalate; the node's check runs again, a stale pass is ignored, and the passing check ends it.
func TestNoReadyNode(t *testing.T) {
	s, advance := readinessFixture(t, "n1")
	ctx := context.Background()
	co := DefaultCompany
	// Before any fault the node checks the project at generation 1; Core knows nothing against it yet.
	a := readyBeat(t, s, "n1", []protocol.ProjectReadiness{})
	if c := check(a, "game"); c == nil || c.Generation != 1 || c.Repo != "https://github.com/o/game" || len(c.Requires) != 1 {
		t.Fatalf("checks %+v", a.Checks)
	}
	readyBeat(t, s, "n1", []protocol.ProjectReadiness{{Project: "game", Generation: 1, CheckedAt: 100}})
	dispatchRun(t, s, "r1")
	c, err := claimV2(s, "r1", "n1", 1, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, "r1", c.Lease, protocol.Event{Status: "ENDED", Outcome: "setup_failed", Result: repoNotFound}); err != nil {
		t.Fatal(err)
	}
	// Many sweeps later: no attempt, no escalation, the task waits for a ready node.
	for i := 0; i < 5; i++ {
		advance(time.Minute)
		snap, err := s.TaskSnapshot(ctx, co, "t")
		if err != nil {
			t.Fatal(err)
		}
		snap.AgentConfigured, snap.Needs = true, tasks.NodeNeeds{Provider: "codex"}
		v := tasks.Evaluate(snap)
		if v.Reason != tasks.ReasonNoReadyNode || !strings.Contains(v.Detail, "mini-n1") || !strings.Contains(v.Detail, "gh auth login") {
			t.Fatalf("verdict %+v", v)
		}
	}
	if es, _ := s.Escalations(ctx, co, true); len(es) != 0 {
		t.Fatalf("escalated before the fault was fixed: %+v", es)
	}
	if v, _ := s.Task(ctx, co, "t"); v.AttemptsNoProgress != 0 {
		t.Fatalf("attempts %d", v.AttemptsNoProgress)
	}
	// A run queued meanwhile (or before the fault) says no_ready_node with the node and the fix.
	dispatchRun(t, s, "r2")
	q, err := s.QueuedWhy(ctx, co, "r2")
	if err != nil || q == nil || q.Reason != QueuedNoReadyNode || !strings.Contains(q.Detail, "mini-n1") || !strings.Contains(q.Detail, "fix:") {
		t.Fatalf("queued why %+v %v", q, err)
	}

	// The node's own result from before the fault does not clear it; Core asks for a new generation.
	a = readyBeat(t, s, "n1", []protocol.ProjectReadiness{{Project: "game", Generation: 1, CheckedAt: 100}})
	if c := check(a, "game"); c == nil || c.Generation < 2 {
		t.Fatalf("no new check asked after the fault: %+v", a.Checks)
	}
	gen := check(a, "game").Generation
	readyBeat(t, s, "n1", []protocol.ProjectReadiness{{Project: "game", Generation: 1, CheckedAt: 101}})
	if offered(t, s, "n1", "r2") {
		t.Fatal("a check older than the fault cleared it")
	}
	// The new check still fails: the fault stays, now from the probe.
	probe := []protocol.NodeFault{{Kind: protocol.FaultRepoAccess, Detail: "cannot read https://github.com/o/game: Repository not found", Fix: "gh auth login"}}
	readyBeat(t, s, "n1", []protocol.ProjectReadiness{{Project: "game", Generation: gen, Faults: probe, CheckedAt: 200}})
	if rs, _ := s.NodesReadiness(ctx, co); len(rs["n1"]) != 1 || rs["n1"][0].Ready || rs["n1"][0].Source != "probe" {
		t.Fatalf("readiness %+v", rs)
	}
	// Check again: a new generation at once; the owner fixed gh auth, the check passes and the run goes to n1.
	if err = s.CheckNodeAgain(ctx, co, "n1"); err != nil {
		t.Fatal(err)
	}
	a = readyBeat(t, s, "n1", []protocol.ProjectReadiness{{Project: "game", Generation: gen, Faults: probe, CheckedAt: 200}})
	again := check(a, "game").Generation
	if again <= gen {
		t.Fatalf("check again kept generation %d", again)
	}
	readyBeat(t, s, "n1", []protocol.ProjectReadiness{{Project: "game", Generation: again, CheckedAt: 300}})
	if !offered(t, s, "n1", "r2") {
		t.Fatal("the node passed its check and still gets no runs of the project")
	}
	var ready int
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM feed_events WHERE company_id=? AND kind='node.ready'", co).Scan(&ready); err != nil || ready != 1 {
		t.Fatalf("node.ready events %d %v", ready, err)
	}
}

// TestRuntimeFaultsOfOldNodes (node-readiness 1.1): an older node reports the 2026-11-07/08 runtime refusals as plain
// failed runs; Core still charges them to the node when the runtime used nothing, and not when the agent worked.
func TestRuntimeFaultsOfOldNodes(t *testing.T) {
	s, advance := readinessFixture(t, "n1", "n2")
	ctx := context.Background()
	dispatchRun(t, s, "r1")
	c, err := claimV2(s, "r1", "n1", 1, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, "r1", c.Lease, protocol.Event{Status: "ENDED", Outcome: "failed", Result: "Sandbox required but unavailable: bubblewrap (bwrap) not installed, socat not installed"}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Task(ctx, DefaultCompany, "t")
	if v.AttemptsNoProgress != 0 || v.NotBefore != 0 {
		t.Fatalf("a sandbox fault counted: attempts %d", v.AttemptsNoProgress)
	}
	// A runtime fault is not re-checked at once (the project check cannot see it): the node waits ReadinessRecheck.
	if a := readyBeat(t, s, "n1", []protocol.ProjectReadiness{}); check(a, "game").Generation != 1 {
		t.Fatalf("runtime fault re-checked at once: %+v", a.Checks)
	}
	advance(ReadinessRecheck)
	if a := readyBeat(t, s, "n1", []protocol.ProjectReadiness{}); check(a, "game").Generation != 2 {
		t.Fatalf("runtime fault not re-checked after %s: %+v", ReadinessRecheck, a.Checks)
	}
	// The agent worked (tokens) and its text quotes the same words: the task's own failure.
	dispatchRun(t, s, "r2")
	c, err = claimV2(s, "r2", "n2", 1, "c2")
	if err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, "r2", c.Lease, protocol.Event{Status: "ENDED", Outcome: "failed", Result: "Sandbox required but unavailable", Usage: []protocol.Usage{{Model: "m", Input: 10, Output: 5}}}); err != nil {
		t.Fatal(err)
	}
	var fault string
	if err = s.DB.QueryRowContext(ctx, "SELECT node_fault FROM runs WHERE id='r2'").Scan(&fault); err != nil || fault != "" {
		t.Fatalf("agent failure charged to the node: %q %v", fault, err)
	}
	if v, _ = s.Task(ctx, DefaultCompany, "t"); v.AttemptsNoProgress != 1 {
		t.Fatalf("the task's own failure not counted: %d", v.AttemptsNoProgress)
	}
}

// TestProjectRequiresValidated (node-readiness 2.1): requires take the short form and refuse what a node must not run.
func TestProjectRequiresValidated(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	if _, err := s.CreateProject(ctx, DefaultCompany, tasks.ProjectCreate{ID: "bad", Name: "bad", Workspace: "/tmp/req-bad", Requires: []tasks.Requirement{{Command: "sh -c id"}}}); err == nil {
		t.Fatal("a shell command accepted as a requirement")
	}
	p, err := s.CreateProject(ctx, DefaultCompany, tasks.ProjectCreate{ID: "ok", Name: "ok", Workspace: "/tmp/req-ok"})
	if err != nil {
		t.Fatal(err)
	}
	reqs := []tasks.Requirement{{Command: "openspec"}, {Command: "gh"}}
	if p, err = s.UpdateProject(ctx, DefaultCompany, "ok", tasks.ProjectUpdate{Version: p.Version, Name: "ok", Requires: &reqs}); err != nil || len(p.Requires) != 2 {
		t.Fatalf("%+v %v", p.Requires, err)
	}
}

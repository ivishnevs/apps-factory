package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

func register(t *testing.T, s *Store, node string, epoch int64, mods ...func(*NodeRegistration)) NodeRegistered {
	t.Helper()
	r := NodeRegistration{NodeID: node, Name: "mini-" + node, Epoch: epoch, ProtocolVersion: 2, Slots: 2,
		Drivers: []NodeDriver{{Name: "codex", Version: "codex-cli 0.162.1", OK: true}}, Resources: map[string]int{"simulator": 1}}
	for _, m := range mods {
		m(&r)
	}
	v, err := s.RegisterNode(context.Background(), DefaultCompany, r)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func claimV2(s *Store, run, node string, epoch int64, claim string) (Claimed, error) {
	return s.ClaimV2(context.Background(), DefaultCompany, run, ClaimRequest{NodeID: node, Epoch: epoch, ClaimID: claim, Slot: 0})
}

func refused(t *testing.T, err error, retry bool, reason string) {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || r.Retry != retry || r.Drop == retry || r.Reason != reason {
		t.Fatalf("want refusal retry=%v %s, got %v", retry, reason, err)
	}
}

func queue(t *testing.T, s *Store, id, project, agent string, snapshot string) {
	t.Helper()
	spec := protocol.RunSpec{ID: id, Project: project, Agent: agent, Provider: protocol.RuntimeCodex, Workspace: "/tmp/v2-" + project, Prompt: "work", TimeoutSeconds: 3600}
	if snapshot != "" {
		spec.ConfigSnapshot = json.RawMessage(snapshot)
	}
	if err := s.Create(context.Background(), DefaultCompany, spec); err != nil {
		t.Fatal(err)
	}
}

func runRow(t *testing.T, s *Store, id string) (status, result string, startBy, leaseUntil int64) {
	t.Helper()
	if err := s.DB.QueryRow("SELECT status,result,start_by,lease_until FROM runs WHERE id=?", id).Scan(&status, &result, &startBy, &leaseUntil); err != nil {
		t.Fatal(err)
	}
	return
}

func TestRegisterNodeEpochs(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	a := register(t, s, "n1", 1)
	if a.Incarnation == "" || a.HeartbeatSeconds != 10 || a.StartWindowSeconds != 60 {
		t.Fatalf("%+v", a)
	}
	for _, epoch := range []int64{1, 0} {
		if _, err := s.RegisterNode(ctx, DefaultCompany, NodeRegistration{NodeID: "n1", Name: "x", Epoch: epoch, ProtocolVersion: 2, Slots: 1}); epoch == 1 && !errors.Is(err, ErrStaleEpoch) || epoch == 0 && !errors.Is(err, ErrInput) {
			t.Fatal(epoch, err)
		}
	}
	if _, err := s.RegisterNode(ctx, DefaultCompany, NodeRegistration{NodeID: "n2", Name: "x", Epoch: 1, ProtocolVersion: 1, Slots: 1}); !errors.Is(err, ErrInput) {
		t.Fatal("protocol 1 registered", err)
	}
	if b := register(t, s, "n1", 5); b.Incarnation != a.Incarnation {
		t.Fatal("incarnation changed without restore")
	}
	nodes, err := s.Nodes(ctx, DefaultCompany)
	if err != nil || len(nodes) != 1 || nodes[0].Epoch != 5 || !nodes[0].Online || nodes[0].Resources["simulator"] != 1 {
		t.Fatalf("%+v %v", nodes, err)
	}
}

// TestIncarnationRestore: reopening keeps the incarnation; a database that no longer matches its
// incarnation file (a restored backup) gets a new one.
func TestIncarnationRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := s.Incarnation(context.Background())
	s.DB.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Incarnation(context.Background()); again != first {
		t.Fatal("reopen changed the incarnation")
	}
	s.DB.Close()
	if err = os.WriteFile(path+".incarnation", []byte("other-database\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	restored, _ := s.Incarnation(context.Background())
	b, _ := os.ReadFile(path + ".incarnation")
	if restored == first || strings.TrimSpace(string(b)) != restored {
		t.Fatalf("restored %q first %q file %q", restored, first, b)
	}
}

// TestClaimV2: a claim answers lease, start window, RunSpec and brief, is idempotent per claim_id, and
// every refusal names its reason.
func TestClaimV2(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	register(t, s, "n1", 3)
	queue(t, s, "r1", "p1", "dev", `{"spec":{"resources":{"simulator":1}}}`)
	refused(t, func() error { _, err := claimV2(s, "r1", "n1", 2, "2-0-1"); return err }(), false, "old_epoch")
	refused(t, func() error { _, err := claimV2(s, "r1", "ghost", 1, "1-0-1"); return err }(), false, "old_epoch")
	c, err := claimV2(s, "r1", "n1", 3, "3-0-1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Lease == "" || c.RunSpec.ID != "r1" || c.RunSpec.Company != DefaultCompany || !c.StartBy.Equal(fake.Now().Add(StartWindow).UTC().Truncate(time.Second)) {
		t.Fatalf("%+v", c)
	}
	again, err := claimV2(s, "r1", "n1", 3, "3-0-1")
	if err != nil || again.Lease != c.Lease || !again.StartBy.Equal(c.StartBy) {
		t.Fatal("retried claim got another answer", again, err)
	}
	refused(t, func() error { _, err := claimV2(s, "r1", "n1", 3, "3-1-2"); return err }(), false, "duplicate")
	if st, _, _, _ := runRow(t, s, "r1"); st != "RUNNING" {
		t.Fatal(st)
	}
	// The node's only simulator is held by r1: another run needing one waits.
	queue(t, s, "r2", "p2", "ios", `{"spec":{"resources":{"simulator":1}}}`)
	refused(t, func() error { _, err := claimV2(s, "r2", "n1", 3, "3-1-3"); return err }(), true, "capacity")
	// The agent already has a run.
	queue(t, s, "r3", "p3", "dev", "")
	refused(t, func() error { _, err := claimV2(s, "r3", "n1", 3, "3-1-4"); return err }(), true, "agent_busy")
	// No usable driver for the provider.
	register(t, s, "n2", 1, func(r *NodeRegistration) { r.Drivers = []NodeDriver{{Name: "codex", OK: false}} })
	queue(t, s, "r4", "p4", "web", "")
	refused(t, func() error { _, err := claimV2(s, "r4", "n2", 1, "1-0-1"); return err }(), true, "capacity")
	// Paused agent.
	if err = s.SetPause(ctx, DefaultCompany, Pause{Scope: "agent", Target: "web", Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	refused(t, func() error { _, err := claimV2(s, "r4", "n1", 3, "3-1-5"); return err }(), true, "paused")
	if err = s.ClearPause(ctx, DefaultCompany, "agent", "web", "owner"); err != nil {
		t.Fatal(err)
	}
	// Slots: n1 has two; r1 and r4 fill them.
	if _, err = claimV2(s, "r4", "n1", 3, "3-1-6"); err != nil {
		t.Fatal(err)
	}
	queue(t, s, "r5", "p5", "api", "")
	refused(t, func() error { _, err := claimV2(s, "r5", "n1", 3, "3-0-7"); return err }(), true, "capacity")
	// A run that is no longer QUEUED is stale; a revoked one says so.
	if err = s.Cancel(ctx, DefaultCompany, "r5"); err != nil {
		t.Fatal(err)
	}
	refused(t, func() error { _, err := claimV2(s, "r5", "n1", 3, "3-0-8"); return err }(), false, "stale")
}

// TestStartAndHeartbeat: /start moves CLAIMED to started; the inventory heartbeat renews leases, extends
// the start window up to 15 minutes, stops revoked, terminal and unknown runs, acknowledges applied ends
// and loses started runs the node stopped listing.
func TestStartAndHeartbeat(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	inc := register(t, s, "n1", 1, func(r *NodeRegistration) { r.Slots = 4 }).Incarnation
	for i, id := range []string{"a", "b", "c", "d"} {
		queue(t, s, id, "p-"+id, "agent-"+id, "")
		if _, err := claimV2(s, id, "n1", 1, "1-0-"+string(rune('1'+i))); err != nil {
			t.Fatal(err)
		}
	}
	lease := func(id string) string {
		var l string
		s.DB.QueryRow("SELECT lease FROM runs WHERE id=?", id).Scan(&l)
		return l
	}
	if _, err := s.StartRun(ctx, DefaultCompany, "a", "wrong"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	for range 2 {
		if st, err := s.StartRun(ctx, DefaultCompany, "a", lease("a")); err != nil || st != "RUNNING" {
			t.Fatal(st, err)
		}
	}
	if _, err := s.Heartbeat(ctx, DefaultCompany, "a", lease("a")); !errors.Is(err, ErrConflict) {
		t.Fatal("per-run heartbeat renewed a v2 run", err)
	}
	_, _ = s.StartRun(ctx, DefaultCompany, "c", lease("c"))
	_, _ = s.StartRun(ctx, DefaultCompany, "d", lease("d"))
	beat := func(inv ...InventoryRun) HeartbeatAnswer {
		t.Helper()
		a, err := s.NodeHeartbeat(ctx, DefaultCompany, "n1", NodeHeartbeat{Epoch: 1, Inventory: inv, Health: json.RawMessage(`{"disk":"ok"}`)})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	// b stays claimed for 20 minutes of heartbeats: its window grows, but not past 15 minutes.
	claimedAt := fake.Now()
	for range 40 {
		fake.Advance(30 * time.Second)
		a := beat(InventoryRun{RunID: "a", State: "running"}, InventoryRun{RunID: "b", State: "starting"}, InventoryRun{RunID: "c", State: "running"}, InventoryRun{RunID: "d", State: "running"})
		if a.Incarnation != inc || len(a.Stop) != 0 {
			t.Fatalf("%+v", a)
		}
	}
	if _, _, startBy, leaseUntil := runRow(t, s, "b"); startBy != claimedAt.Add(MaxStartWindow).Unix() || leaseUntil != fake.Now().Add(NodeOffline).Unix() {
		t.Fatal(startBy, leaseUntil)
	}
	if err := s.ExpireNodes(ctx, DefaultCompany); err != nil {
		t.Fatal(err)
	}
	if st, res, _, _ := runRow(t, s, "b"); st != "FAILED" || !strings.HasPrefix(res, "lost: not started") {
		t.Fatal(st, res)
	}
	// c ends: until its terminal event is applied the node keeps it as ended_unacked.
	if a := beat(InventoryRun{RunID: "a", State: "running"}, InventoryRun{RunID: "c", State: "ended_unacked"}, InventoryRun{RunID: "d", State: "running"}); len(a.Acked) != 0 {
		t.Fatal("acked before the end was applied", a)
	}
	if err := s.Apply(ctx, DefaultCompany, protocol.Wrap("agent.completed", "c", "", protocol.Event{RunID: "c", Lease: lease("c"), Status: "COMPLETED", Result: "ok"})); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(ctx, DefaultCompany, "a"); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Minute) // d leaves the inventory and stays out past the grace
	a := beat(InventoryRun{RunID: "a", State: "running"}, InventoryRun{RunID: "b", State: "running"}, InventoryRun{RunID: "c", State: "ended_unacked"}, InventoryRun{RunID: "zz", State: "running"})
	stops := map[string]string{}
	for _, x := range a.Stop {
		stops[x.RunID] = x.Reason
	}
	if stops["a"] != "revoked" || stops["b"] != "terminal" || stops["zz"] != "unknown_run" || len(a.Acked) != 1 || a.Acked[0] != "c" {
		t.Fatalf("%+v", a)
	}
	if st, res, _, _ := runRow(t, s, "d"); st != "UNKNOWN" || !strings.Contains(res, "missing from node n1 inventory") {
		t.Fatal(st, res)
	}
	if a, _ := s.NodeHeartbeat(ctx, DefaultCompany, "n1", NodeHeartbeat{Epoch: 1, Health: json.RawMessage(`{"disk":"critical"}`)}); !a.Pause {
		t.Fatal("critical disk did not pause")
	}
	if _, err := s.NodeHeartbeat(ctx, DefaultCompany, "n1", NodeHeartbeat{Epoch: 0}); !errors.Is(err, ErrStaleEpoch) {
		t.Fatal(err)
	}
	for _, hb := range []struct {
		node  string
		epoch int64
	}{{"n1", 2}, {"ghost", 1}} {
		if _, err := s.NodeHeartbeat(ctx, DefaultCompany, hb.node, NodeHeartbeat{Epoch: hb.epoch}); !errors.Is(err, ErrUnknownNode) {
			t.Fatal(hb, err)
		}
	}
}

// TestTimedOutRun: a started run listed past timeout + grace on the node's sleep-excluding clock
// (elapsed_ms) ends UNKNOWN timed_out and is stopped; wall-clock time the host slept does not count.
func TestTimedOutRun(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	register(t, s, "n1", 1)
	queue(t, s, "r", "p", "dev", "")
	c, err := claimV2(s, "r", "n1", 1, "1-0-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRun(ctx, DefaultCompany, "r", c.Lease); err != nil {
		t.Fatal(err)
	}
	beat := func(elapsed time.Duration) HeartbeatAnswer {
		t.Helper()
		a, err := s.NodeHeartbeat(ctx, DefaultCompany, "n1", NodeHeartbeat{Epoch: 1, Inventory: []InventoryRun{{RunID: "r", State: "running", ElapsedMS: elapsed.Milliseconds()}}})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	fake.Advance(2 * time.Hour) // the laptop slept: the node's clock did not advance
	if a := beat(time.Minute); len(a.Stop) != 0 {
		t.Fatal("sleep counted as run time", a)
	}
	if a := beat(time.Hour + TimeoutGrace); len(a.Stop) != 0 {
		t.Fatal("stopped within the grace", a)
	}
	a := beat(time.Hour + TimeoutGrace + time.Second)
	if len(a.Stop) != 1 || a.Stop[0].Reason != "timed_out" {
		t.Fatal(a)
	}
	r, _ := s.Get(ctx, DefaultCompany, "r")
	if r.Status != "UNKNOWN" || !strings.HasPrefix(r.Result, "timed_out") || r.Outcome != "timed_out" {
		t.Fatalf("%+v", r)
	}
}

// TestLostRuns: a new epoch loses the older epoch's runs (claimed → FAILED and retryable, started →
// UNKNOWN); a silent node goes offline and loses its runs the same way.
func TestLostRuns(t *testing.T) {
	s := engineStore(t)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	ctx := context.Background()
	register(t, s, "n1", 1)
	task := readyEngineTask(t, s, createEngineTask(t, s, "t1"))
	spec := dispatchEngineTask(t, s, task, "claimed-run")
	if _, err := claimV2(s, spec.ID, "n1", 1, "1-0-1"); err != nil {
		t.Fatal(err)
	}
	register(t, s, "n1", 2)
	if st, res, _, _ := runRow(t, s, spec.ID); st != "FAILED" || !strings.Contains(res, "lost: not started (node n1 restarted (epoch 2))") {
		t.Fatal(st, res)
	}
	got, err := s.Task(ctx, DefaultCompany, "t1")
	if err != nil || got.Status != "FAILED" || !got.RetryEligible {
		t.Fatalf("%+v %v", got, err)
	}

	queue(t, s, "started", "p2", "other", "")
	c, err := claimV2(s, "started", "n1", 2, "2-0-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRun(ctx, DefaultCompany, "started", c.Lease); err != nil {
		t.Fatal(err)
	}
	fake.Advance(NodeOffline + time.Second)
	if err = s.Expire(ctx, DefaultCompany); err != nil {
		t.Fatal(err)
	}
	if err = s.ExpireNodes(ctx, DefaultCompany); err != nil {
		t.Fatal(err)
	}
	if st, _, _, _ := runRow(t, s, "started"); st != "UNKNOWN" {
		t.Fatal(st)
	}
	fake.Advance(NodeOffline)
	if err = s.ExpireNodes(ctx, DefaultCompany); err != nil {
		t.Fatal(err)
	}
	nodes, _ := s.Nodes(ctx, DefaultCompany)
	if len(nodes) != 1 || nodes[0].Online {
		t.Fatalf("%+v", nodes)
	}
	// The node comes back: its heartbeat brings it online and stops the run Core already ended.
	a, err := s.NodeHeartbeat(ctx, DefaultCompany, "n1", NodeHeartbeat{Epoch: 2, Inventory: []InventoryRun{{RunID: "started", State: "running"}}})
	if err != nil || len(a.Stop) != 1 || a.Stop[0].Reason != "terminal" {
		t.Fatal(a, err)
	}
	if nodes, _ = s.Nodes(ctx, DefaultCompany); !nodes[0].Online {
		t.Fatal("heartbeat did not bring the node online")
	}
}

// TestCleanupListsClosedWorktrees: a closed task's worktree on the node is listed for removal a bounded
// number of times; open tasks and other nodes' worktrees are not.
func TestCleanupListsClosedWorktrees(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	register(t, s, "n1", 1)
	task := readyEngineTask(t, s, createEngineTask(t, s, "t1"))
	spec := dispatchEngineTask(t, s, task, "wt-run")
	c, err := claimV2(s, spec.ID, "n1", 1, "1-0-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReportWorkspace(ctx, DefaultCompany, spec.ID, c.Lease, tasks.Workspace{Mode: "worktree", Branch: "af/t1", NodeID: "n1"}); err != nil {
		t.Fatal(err)
	}
	beat := func() []string {
		a, err := s.NodeHeartbeat(ctx, DefaultCompany, "n1", NodeHeartbeat{Epoch: 1})
		if err != nil {
			t.Fatal(err)
		}
		return a.Cleanup
	}
	if got := beat(); len(got) != 0 {
		t.Fatal("open task listed", got)
	}
	if err = s.Cancel(ctx, DefaultCompany, spec.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Apply(ctx, DefaultCompany, protocol.Wrap("agent.cancelled", spec.ID, "", protocol.Event{RunID: spec.ID, Lease: c.Lease, Status: "CANCELLED"})); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Task(ctx, DefaultCompany, "t1"); got.Status != "CANCELLED" {
		t.Fatal(got.Status)
	}
	for i := range cleanupListings {
		if got := beat(); len(got) != 1 || got[0] != "t1" {
			t.Fatal(i, got)
		}
	}
	if got := beat(); len(got) != 0 {
		t.Fatal("listed forever", got)
	}
}

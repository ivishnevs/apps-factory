package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// throughputAlerts are the active throughput alerts by ref.
func throughputAlerts(t *testing.T, s *Store) map[string]string {
	t.Helper()
	rows, err := s.DB.QueryContext(context.Background(), "SELECT ref_id,summary FROM task_alerts WHERE company_id=? AND kind='throughput' AND status='active'", DefaultCompany)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var ref, summary string
		if err = rows.Scan(&ref, &summary); err != nil {
			t.Fatal(err)
		}
		out[ref] = summary
	}
	return out
}

// notices are the throughput notices Factory sent to agent.
func notices(t *testing.T, s *Store, agent string) []string {
	t.Helper()
	rows, err := s.DB.QueryContext(context.Background(), "SELECT body->>'body' FROM messages WHERE company_id=? AND sender='factory' AND recipient=? AND message_type='notification' AND body->>'body' LIKE 'Throughput:%' ORDER BY created_at", DefaultCompany, agent)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		if err = rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

// TestThroughputDiskPausedHolder replays 2026-11-12 (throughput-signals 1.1, 4.2): runs tied to a node with their
// sessions wait while the node is paused for a critical disk and another node has free slots. Within the threshold
// nothing is said; past it the producer gets one notice naming the node, its free disk and the release time, and
// the owner one alert; the next sweeps repeat nothing; when the run is claimed the episode ends and the alert clears.
func TestThroughputDiskPausedHolder(t *testing.T) {
	s, advance := readinessFixture(t, "n1", "n2")
	s.LeadOf = func(company, team string) string { return "producer" }
	ctx := context.Background()
	co := DefaultCompany
	sessionHeldBy(t, s, advance, "n1")
	beat(t, s, "n1", 1, diskHealth(4.9))
	bs, err := s.SweepThroughput(ctx, co, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 1 || bs[0].Key != "affinity:n1" || bs[0].Due || bs[0].Count != 1 || bs[0].Tasks[0] != "t" || bs[0].FreeSlots != 2 {
		t.Fatalf("bottlenecks %+v", bs)
	}
	if !strings.Contains(bs[0].Summary, "1 run wait for mini-n1 (n1): paused (critical disk), disk critical, 4.9 GiB free (critical 5 GiB); released by itself at") ||
		!strings.Contains(bs[0].Fix, "free disk on mini-n1 (n1)") || !strings.Contains(bs[0].Fix, "Release now") {
		t.Fatalf("summary %q fix %q", bs[0].Summary, bs[0].Fix)
	}
	if len(throughputAlerts(t, s)) != 0 || len(notices(t, s, "producer")) != 0 {
		t.Fatal("said something within the threshold")
	}
	advance(31 * time.Minute)
	for i := 0; i < 3; i++ {
		if bs, err = s.SweepThroughput(ctx, co, nil); err != nil || len(bs) != 1 || !bs[0].Due {
			t.Fatalf("sweep %d: %+v %v", i, bs, err)
		}
		advance(time.Minute)
	}
	alerts := throughputAlerts(t, s)
	if len(alerts) != 1 || !strings.Contains(alerts["game/affinity:n1"], "4.9 GiB free") {
		t.Fatalf("alerts %+v", alerts)
	}
	ns := notices(t, s, "producer")
	if len(ns) != 1 || !strings.Contains(ns[0], "mini-n1 (n1)") || !strings.Contains(ns[0], "released by itself at") || !strings.Contains(ns[0], "free disk on mini-n1 (n1)") {
		t.Fatalf("notices %q", ns)
	}
	var attention int
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM feed_events WHERE company_id=? AND kind='task.alert.throughput' AND attention=1", co).Scan(&attention); err != nil || attention != 1 {
		t.Fatalf("attention items %d %v", attention, err)
	}
	// The read-only view says the same without changing anything.
	if bs, err = s.Bottlenecks(ctx, co, nil); err != nil || len(bs) != 1 || !bs[0].Due || bs[0].Since == 0 {
		t.Fatalf("bottlenecks %+v %v", bs, err)
	}
	// Disk freed: n1 takes the run, the cause is gone and the alert clears.
	beat(t, s, "n1", 1, diskHealth(30))
	if _, err = claimV2(s, "r2", "n1", 1, "c2"); err != nil {
		t.Fatal(err)
	}
	if bs, err = s.SweepThroughput(ctx, co, nil); err != nil || len(bs) != 0 {
		t.Fatalf("after the claim %+v %v", bs, err)
	}
	if len(throughputAlerts(t, s)) != 0 {
		t.Fatal("alert kept after its cause went")
	}
	var causes int
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM throughput_causes WHERE company_id=?", co).Scan(&causes); err != nil || causes != 0 {
		t.Fatalf("episodes left %d %v", causes, err)
	}
}

// TestThroughputOwnerPausedNodes (throughput-signals 1.1): with every node paused by the owner there is no idle slot,
// yet the paused capacity is the cause: it is named with the pause and its fix. A capacity-bound reason (all slots
// held) is not a cause while no slot is idle.
func TestThroughputOwnerPausedNodes(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	for _, p := range []string{"a", "b"} {
		if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "app-" + p, Name: "App " + p, Workspace: "/tmp/throughput-app-" + p}); err != nil {
			t.Fatal(err)
		}
	}
	register(t, s, "n1", 1, func(r *NodeRegistration) { r.Slots = 1 })
	if err := s.SetPause(ctx, co, Pause{Scope: "node", Target: "n1", Reason: "moving the Mac", Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if _, err := s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "app-" + id, Title: id, Assignee: "dev-" + id, Ready: true, RequestID: id}); err != nil {
			t.Fatal(err)
		}
		v, _ := s.Task(ctx, co, id)
		if _, err := s.Dispatch(ctx, co, id, v.Version, protocol.RunSpec{ID: "r" + id, Project: "app-" + id, Agent: "dev-" + id, Provider: protocol.RuntimeCodex}); err != nil {
			t.Fatal(err)
		}
	}
	bs, err := s.Bottlenecks(ctx, co, nil)
	if err != nil || len(bs) != 2 || bs[0].Key != "node_paused:n1" || bs[0].Project != "app-a" || bs[1].Project != "app-b" || bs[0].Count != 1 || bs[0].FreeSlots != 0 {
		t.Fatalf("bottlenecks %+v %v", bs, err)
	}
	if bs[0].Summary != "1 run wait (node paused): mini-n1 (n1): paused by the owner: moving the Mac (tasks a)" || bs[0].Fix != "resume mini-n1 (n1), or Release now its tied runs" {
		t.Fatalf("summary %q fix %q", bs[0].Summary, bs[0].Fix)
	}
	// Resumed: one run takes the only slot, the other waits for it: capacity in use, no cause.
	if err = s.ClearPause(ctx, co, "node", "n1", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err = claimV2(s, "ra", "n1", 1, "c1"); err != nil {
		t.Fatal(err)
	}
	if q, _ := s.QueuedWhy(ctx, co, "rb"); q == nil || q.Reason != QueuedNoSlot {
		t.Fatalf("queued why %+v", q)
	}
	if bs, err = s.Bottlenecks(ctx, co, nil); err != nil || len(bs) != 0 {
		t.Fatalf("a full node is not a bottleneck: %+v %v", bs, err)
	}
}

// TestThroughputAgentLimitAndGate (throughput-signals 1.1): task capacity waits for the agent's limit and queued runs
// of the same cause are one cause while slots are idle; open tasks with GateMin released tasks behind them are gates,
// named with every task behind them.
func TestThroughputAgentLimitAndGate(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "game", Name: "Game", Workspace: "/tmp/throughput-game"}); err != nil {
		t.Fatal(err)
	}
	register(t, s, "n1", 1, func(r *NodeRegistration) { r.Slots = 4 })
	create := func(id, assignee string, ready bool, deps ...string) {
		t.Helper()
		if _, err := s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "game", Title: id, Assignee: assignee, Ready: ready, Dependencies: deps, RequestID: id}); err != nil {
			t.Fatal(err)
		}
	}
	create("verify", "game-qa", true)
	for _, id := range []string{"spec1", "spec2", "spec3"} {
		create(id, "game-designer", true, "verify")
	}
	create("impl", "network-dev", false, "spec1") // in the backlog behind spec1: counted in all, not as waiting
	waits := []TaskWait{}
	for _, id := range []string{"n2", "n3", "n4", "n5"} {
		waits = append(waits, TaskWait{Project: "game", TaskID: id, Wait: tasks.Wait{Kind: tasks.WaitAgent, Agent: "network-dev", Live: 1, Limit: 1}})
	}
	bs, err := s.Bottlenecks(ctx, co, waits)
	if err != nil || len(bs) != 2 {
		t.Fatalf("bottlenecks %+v %v", bs, err)
	}
	if b := bs[0]; b.Key != "agent_busy:network-dev" || b.Summary != "network-dev: 4 queued, limit 1, 4 free slots (tasks n2, n3, n4, n5)" || !strings.Contains(b.Fix, "raise the agent's limit") {
		t.Fatalf("agent limit %+v", b)
	}
	if b := bs[1]; b.Key != "gate:verify" || b.Summary != `3 tasks wait behind verify "verify" (READY, game-qa), 4 in all (tasks spec1, spec2, spec3)` || b.Tasks[0] != "verify" || !strings.Contains(b.Fix, "not on verifications") {
		t.Fatalf("gate %+v", b)
	}
	// No idle slot: the agent's limit and the gate are capacity in use, not a bottleneck.
	register(t, s, "n1", 2, func(r *NodeRegistration) { r.Slots = 1 })
	create("busy", "other", true)
	v, _ := s.Task(ctx, co, "busy")
	if _, err = s.Dispatch(ctx, co, "busy", v.Version, protocol.RunSpec{ID: "rbusy", Project: "game", Agent: "other", Provider: protocol.RuntimeCodex}); err != nil {
		t.Fatal(err)
	}
	if _, err = claimV2(s, "rbusy", "n1", 2, "cb"); err != nil {
		t.Fatal(err)
	}
	if bs, err = s.Bottlenecks(ctx, co, waits); err != nil || len(bs) != 0 {
		t.Fatalf("full nodes %+v %v", bs, err)
	}
}

// TestTaskResourcesExclusive (throughput-signals 2.2): two integration-gate tasks declare mac-build: 1 and the Mac
// offers one: one runs, the other stays queued with resource_busy naming the first.
func TestTaskResourcesExclusive(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	for _, p := range []string{"gate1", "gate2"} {
		if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "game-" + p, Name: "Game " + p, Workspace: "/tmp/throughput-res-" + p}); err != nil {
			t.Fatal(err)
		}
	}
	register(t, s, "mac", 1, func(r *NodeRegistration) { r.Slots = 4; r.Resources = map[string]int{"mac-build": 1} })
	for _, id := range []string{"gate1", "gate2"} {
		if _, err := s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "game-" + id, Title: id, Assignee: "dev-" + id, Ready: true, RequestID: id, Resources: map[string]int{"mac-build": 1}}); err != nil {
			t.Fatal(err)
		}
		v, _ := s.Task(ctx, co, id)
		if v.Resources["mac-build"] != 1 {
			t.Fatalf("task resources %+v", v.Resources)
		}
		spec, err := s.Dispatch(ctx, co, id, v.Version, protocol.RunSpec{ID: "r" + id, Project: "game-" + id, Agent: "dev-" + id, Provider: protocol.RuntimeCodex})
		if err != nil || spec.Resources["mac-build"] != 1 {
			t.Fatalf("run resources %+v %v", spec.Resources, err)
		}
	}
	if _, err := claimV2(s, "rgate1", "mac", 1, "c1"); err != nil {
		t.Fatal(err)
	}
	refused(t, func() error { _, err := claimV2(s, "rgate2", "mac", 1, "c2"); return err }(), true, "capacity")
	q, err := s.QueuedWhy(ctx, co, "rgate2")
	if err != nil || q == nil || q.Reason != QueuedResourceBusy || !strings.Contains(q.Detail, "resource mac-build: 1 of 1 held, the run needs 1; held by task gate1 (dev-gate1)") {
		t.Fatalf("queued why %+v %v", q, err)
	}
	// Bad resources are refused; {} clears them.
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "bad", ProjectID: "game-gate1", Title: "bad", RequestID: "bad", Resources: map[string]int{"Mac Build": 1}}); err == nil {
		t.Fatal("invalid class accepted")
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "later", ProjectID: "game-gate1", Title: "later", RequestID: "later", Resources: map[string]int{"mac-build": 1}}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Task(ctx, co, "later")
	none := map[string]int{}
	if v, err = s.UpdateTask(ctx, co, "later", tasks.Update{Version: v.Version, Resources: &none}); err != nil || v.Resources != nil {
		t.Fatalf("cleared %+v %v", v.Resources, err)
	}
}

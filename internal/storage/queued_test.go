package storage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestQueuedWhy (owner-ui 6.1): a QUEUED run says why no node claims it: a pause with its reason, the
// agent's cap, and each node's lack (offline, owner pause, critical disk, a disabled driver with its
// hold-off, labels, slots, resource classes); the node closest to taking it gives the reason.
func TestQueuedWhy(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	why := func(id string) *QueuedReason {
		t.Helper()
		q, err := s.QueuedWhy(ctx, co, id)
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	want := func(id, reason, detail string) *QueuedReason {
		t.Helper()
		q := why(id)
		if q == nil || q.Reason != reason || !strings.Contains(q.Detail, detail) {
			t.Fatalf("want %s %q, got %+v", reason, detail, q)
		}
		return q
	}
	queue(t, s, "r1", "p1", "dev", `{"spec":{"labels":["gpu"],"resources":{"simulator":1}}}`)
	want("r1", QueuedNoNode, "no registered node")

	register(t, s, "n1", 1)
	want("r1", QueuedLabels, "lacks labels gpu")
	register(t, s, "n1", 2, func(r *NodeRegistration) { r.Labels = []string{"gpu"} })
	q := want("r1", QueuedUnclaimed, "has room")
	if len(q.Nodes) != 1 || q.Nodes[0].Reason != "" {
		t.Fatalf("%+v", q.Nodes)
	}

	// An agent pause with the owner's reason comes first.
	if err := s.SetPause(ctx, co, Pause{Scope: "agent", Target: "dev", Reason: "style guide"}); err != nil {
		t.Fatal(err)
	}
	want("r1", QueuedPaused, "agent dev paused: style guide")
	if err := s.ClearPause(ctx, co, "agent", "dev", "owner"); err != nil {
		t.Fatal(err)
	}

	// The driver disabled by an auth failure until its hold-off ends.
	until := s.Now().Add(10 * time.Minute).Unix()
	beat := func(health string, drivers ...NodeDriver) {
		t.Helper()
		if _, err := s.NodeHeartbeat(ctx, co, "n1", NodeHeartbeat{Epoch: 2, Health: json.RawMessage(health), Drivers: drivers}); err != nil {
			t.Fatal(err)
		}
	}
	beat(`{}`, NodeDriver{Name: "codex", OK: false, Check: "auth", Detail: "401 from the provider", Until: until})
	q = want("r1", QueuedDriverDisabled, "driver codex disabled: auth check failed: 401 from the provider (until ")
	if q.Until != until {
		t.Fatal("until", q.Until)
	}
	// A transient failure count does not disable it.
	beat(`{}`, NodeDriver{Name: "codex", OK: true, TransientFailures: 1})
	want("r1", QueuedUnclaimed, "has room")

	// Disk below critical, then an owner pause of the node.
	beat(`{"disk":"critical","free_disk_bytes":1073741824,"disk_critical_bytes":2147483648}`, NodeDriver{Name: "codex", OK: true})
	want("r1", QueuedNodePaused, "disk 1.0 GiB free, critical below 2.0 GiB")
	beat(`{"disk":"ok"}`, NodeDriver{Name: "codex", OK: true})
	if err := s.SetPause(ctx, co, Pause{Scope: "node", Target: "n1", Reason: "maintenance"}); err != nil {
		t.Fatal(err)
	}
	want("r1", QueuedNodePaused, "paused by the owner: maintenance")
	if err := s.ClearPause(ctx, co, "node", "n1", "owner"); err != nil {
		t.Fatal(err)
	}

	// The resource class held by another agent's run, then every slot held; then the agent's own cap.
	queue(t, s, "sim", "p2", "web", `{"spec":{"resources":{"simulator":1}}}`)
	if _, err := claimV2(s, "sim", "n1", 2, "2-0-1"); err != nil {
		t.Fatal(err)
	}
	want("r1", QueuedResourceBusy, "resource simulator: 1 of 1 held")
	queue(t, s, "other", "p3", "ops", "")
	if _, err := claimV2(s, "other", "n1", 2, "2-0-2"); err != nil {
		t.Fatal(err)
	}
	want("r1", QueuedNoSlot, "all 2 slots held")
	queue(t, s, "r2", "p4", "web", "")
	want("r2", QueuedAgentBusy, "agent web runs 1 of 1 at a time")

	// A second node offline: the closest node (n1, full) gives the reason; both are listed.
	register(t, s, "n2", 1)
	if _, err := s.DB.Exec("UPDATE nodes SET online=0 WHERE id='n2'"); err != nil {
		t.Fatal(err)
	}
	q = want("r1", QueuedNoSlot, "(and 1 other nodes cannot take it)")
	if len(q.Nodes) != 2 || q.Nodes[1].Reason != QueuedNodeOffline {
		t.Fatalf("%+v", q.Nodes)
	}

	// Not QUEUED: no reason; all of them in one read.
	if why("sim") != nil {
		t.Fatal("a claimed run has a queued reason")
	}
	all, err := s.QueuedWhyAll(ctx, co, []string{"r1", "sim", "gone"})
	if err != nil || len(all) != 1 || all["r1"] == nil {
		t.Fatal(all, err)
	}
}

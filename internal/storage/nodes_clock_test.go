package storage

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestClockRefusedHeartbeats (node-wake-recovery 4.4): after a wake Core refuses a node's heartbeats for the
// signature time window for 30 s, past the node's TTL, then a heartbeat signed with the corrected time lands: the
// node stays online and its run is not lost. Refusals alone keep a node for at most one more TTL.
func TestClockRefusedHeartbeats(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	register(t, s, "n1", 1)
	queue(t, s, "r1", "p1", "dev", "")
	c, err := claimV2(s, "r1", "n1", 1, "1-0-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRun(ctx, DefaultCompany, "r1", c.Lease); err != nil {
		t.Fatal(err)
	}
	running := []InventoryRun{{RunID: "r1", State: "running"}}
	heartbeat := func() {
		t.Helper()
		if _, err := s.NodeHeartbeat(ctx, DefaultCompany, "n1", NodeHeartbeat{Epoch: 1, Inventory: running}); err != nil {
			t.Fatal(err)
		}
	}
	refused := func() {
		t.Helper()
		if err := s.NodeClockRefused(ctx, DefaultCompany, "n1"); err != nil {
			t.Fatal(err)
		}
	}
	expire := func() {
		t.Helper()
		if err := s.Expire(ctx, DefaultCompany); err != nil {
			t.Fatal(err)
		}
		if err := s.ExpireNodes(ctx, DefaultCompany); err != nil {
			t.Fatal(err)
		}
	}
	alive := func(want bool, when string) {
		t.Helper()
		nodes, err := s.Nodes(ctx, DefaultCompany)
		if err != nil || len(nodes) != 1 {
			t.Fatal(nodes, err)
		}
		st, res, _, _ := runRow(t, s, "r1")
		if nodes[0].Online != want || (st == "RUNNING") != want {
			t.Fatalf("%s: node online %v, run %s %s; want alive %v", when, nodes[0].Online, st, res, want)
		}
	}
	heartbeat()
	// The host slept; on wake 50 s passed since the last heartbeat and the clocks stay apart for 30 s.
	fake.Advance(50 * time.Second)
	for range 4 {
		refused()
		expire()
		alive(true, "while heartbeats are refused for the window")
		fake.Advance(10 * time.Second)
	}
	heartbeat() // the corrected heartbeat, 90 s after the last accepted one
	expire()
	alive(true, "after the corrected heartbeat")
	if got := nodeEvents(t, s, "node.offline"); len(got) != 0 {
		t.Fatalf("node went offline: %+v", got)
	}

	// Refusals alone keep the node for at most one more TTL past its last accepted heartbeat.
	for elapsed := 10 * time.Second; elapsed <= 3*NodeOffline; elapsed += 10 * time.Second {
		fake.Advance(10 * time.Second)
		refused()
		expire()
		switch {
		case elapsed < 2*NodeOffline:
			alive(true, "within one extra TTL")
		case elapsed > 2*NodeOffline:
			alive(false, "past one extra TTL")
		}
	}
	if _, res, _, _ := runRow(t, s, "r1"); !strings.Contains(res, "lost") {
		t.Fatal("run not lost after the extra TTL:", res)
	}
	// An offline node is not revived by a refused heartbeat.
	refused()
	if nodes, _ := s.Nodes(ctx, DefaultCompany); nodes[0].Online {
		t.Fatal("a refused heartbeat revived an offline node")
	}
}

// TestNodeClockSkew (node-wake-recovery 4.3): a heartbeat reporting an offset to Core's clock above the signature
// window raises node.clock_skew once per episode; back within the window it is re-armed (node.clock_ok).
func TestNodeClockSkew(t *testing.T) {
	s, _ := clockStore(t)
	register(t, s, "n1", 1)
	beat(t, s, "n1", 1, `{"disk":"ok","clock_offset_ms":60000}`)
	if got := nodeEvents(t, s, "node.clock_skew"); len(got) != 0 {
		t.Fatalf("an offset within the window alerted: %+v", got)
	}
	for range 3 {
		beat(t, s, "n1", 1, `{"disk":"ok","clock_offset_ms":-300000}`)
	}
	got := nodeEvents(t, s, "node.clock_skew")
	if len(got) != 1 || !got[0].Attention || !strings.Contains(got[0].Summary, "5m0s behind") {
		t.Fatalf("want one node.clock_skew with attention, got %+v", got)
	}
	beat(t, s, "n1", 1, `{"disk":"ok"}`)
	beat(t, s, "n1", 1, `{"disk":"ok"}`)
	if ok := nodeEvents(t, s, "node.clock_ok"); len(ok) != 1 || ok[0].Attention {
		t.Fatalf("want one node.clock_ok, got %+v", ok)
	}
	beat(t, s, "n1", 1, `{"disk":"ok","clock_offset_ms":200000}`)
	if got = nodeEvents(t, s, "node.clock_skew"); len(got) != 2 || !strings.Contains(got[len(got)-1].Summary, "ahead of") {
		t.Fatalf("a new episode did not alert again: %+v", got)
	}
}

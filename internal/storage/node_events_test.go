package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// nodeEvents returns the feed events of kind (all node.* kinds when empty) in order.
func nodeEvents(t *testing.T, s *Store, kind string) []FeedItem {
	t.Helper()
	page, err := s.Feed(context.Background(), DefaultCompany, FeedFilter{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []FeedItem
	for _, item := range page.Items {
		if kind == "" && strings.HasPrefix(item.Type, "node.") || item.Type == kind {
			out = append(out, item)
		}
	}
	return out
}

func beat(t *testing.T, s *Store, node string, epoch int64, health string) HeartbeatAnswer {
	t.Helper()
	a, err := s.NodeHeartbeat(context.Background(), DefaultCompany, node, NodeHeartbeat{Epoch: epoch, Health: json.RawMessage(health)})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestNodeRestartLoop: four process restarts within 15 minutes raise exactly one node.restart_loop; re-registers
// for a lost node do not count (driver changes ride the heartbeat, reason drivers is refused); a calm window
// re-arms the alert.
func TestNodeRestartLoop(t *testing.T) {
	s, fake := clockStore(t)
	register(t, s, "n1", 1) // first start: not a restart
	for epoch := int64(2); epoch <= 4; epoch++ {
		fake.Advance(2 * time.Minute)
		register(t, s, "n1", epoch)
	}
	if got := nodeEvents(t, s, "node.restart_loop"); len(got) != 0 {
		t.Fatalf("three restarts alerted: %+v", got)
	}
	// Re-registers that are not process starts do not count.
	fake.Advance(time.Minute)
	register(t, s, "n1", 5, func(r *NodeRegistration) { r.Reason = RegisterReregister })
	register(t, s, "n1", 6, func(r *NodeRegistration) { r.Reason = RegisterReregister })
	if got := nodeEvents(t, s, "node.restart_loop"); len(got) != 0 {
		t.Fatalf("re-registers counted as restarts: %+v", got)
	}
	if _, err := s.RegisterNode(context.Background(), DefaultCompany, NodeRegistration{NodeID: "n1", Name: "mini-n1", Epoch: 7, ProtocolVersion: 2, Slots: 2, Reason: "drivers"}); !errors.Is(err, ErrInput) {
		t.Fatalf("reason drivers accepted: %v", err)
	}
	for epoch := int64(7); epoch <= 9; epoch++ { // the fourth restart alerts, the next ones do not
		fake.Advance(time.Minute)
		register(t, s, "n1", epoch)
	}
	got := nodeEvents(t, s, "node.restart_loop")
	if len(got) != 1 || !got[0].Attention || got[0].EntityID != "node:n1" || got[0].ProjectID != "" || !strings.Contains(got[0].Summary, "4 restarts within 15m0s") {
		t.Fatalf("%+v", got)
	}
	// Restarts spread wider than the window never alert again until a calm window re-arms it.
	fake.Advance(RestartLoopWindow + time.Second)
	for epoch := int64(10); epoch <= 13; epoch++ {
		register(t, s, "n1", epoch)
		fake.Advance(time.Minute)
	}
	if got = nodeEvents(t, s, "node.restart_loop"); len(got) != 2 {
		t.Fatalf("a calm window did not re-arm: %d alerts", len(got))
	}
	if _, err := s.RegisterNode(context.Background(), DefaultCompany, NodeRegistration{NodeID: "n1", Name: "x", Epoch: 20, ProtocolVersion: 2, Slots: 1, Reason: "boot"}); err == nil {
		t.Fatal("unknown reason accepted")
	}
}

// TestNodeLowDiskOnceAndPause: a fake disk-usage provider (health.disk) going low then critical produces one
// node.low_disk, one node.disk_critical and the pause answer; recovery re-arms both.
func TestNodeLowDiskOnceAndPause(t *testing.T) {
	s, _ := clockStore(t)
	register(t, s, "n1", 1)
	for _, level := range []string{"ok", "low", "low", "critical", "critical", "low", "critical"} {
		a := beat(t, s, "n1", 1, `{"disk":"`+level+`"}`)
		if a.Pause != (level == "critical") {
			t.Fatalf("disk %s: pause %v", level, a.Pause)
		}
	}
	if low, crit := nodeEvents(t, s, "node.low_disk"), nodeEvents(t, s, "node.disk_critical"); len(low) != 1 || !low[0].Attention || len(crit) != 1 || !crit[0].Attention {
		t.Fatalf("low %+v critical %+v", low, crit)
	}
	if p, r := nodeEvents(t, s, "node.paused"), nodeEvents(t, s, "node.resumed"); len(p) != 2 || len(r) != 1 {
		t.Fatalf("paused %d resumed %d", len(p), len(r))
	}
	// Unreadable disk keeps the last answer.
	if a := beat(t, s, "n1", 1, `{}`); !a.Pause {
		t.Fatal("unreadable disk lifted the pause")
	}
	beat(t, s, "n1", 1, `{"disk":"ok"}`)
	beat(t, s, "n1", 1, `{"disk":"low"}`)
	if got := nodeEvents(t, s, "node.low_disk"); len(got) != 2 {
		t.Fatalf("recovery did not re-arm node.low_disk: %d", len(got))
	}
	if got := nodeEvents(t, s, "node.disk_ok"); len(got) != 1 || got[0].Attention {
		t.Fatalf("%+v", got)
	}
}

// TestNodeCheckFailedOncePerFailure: a failing self-check raises one node.check_failed{check}; it passing again
// is node.check_passed; an omitted checks list keeps the state.
func TestNodeCheckFailedOncePerFailure(t *testing.T) {
	s, _ := clockStore(t)
	register(t, s, "n1", 1)
	beat(t, s, "n1", 1, `{"checks":[{"check":"codex.auth","status":"fail","detail":"not logged in\nrun codex login"}]}`)
	beat(t, s, "n1", 1, `{"checks":[{"check":"codex.auth","status":"fail","detail":"not logged in"},{"check":"git.user","status":"pass"}]}`)
	beat(t, s, "n1", 1, `{}`)
	register(t, s, "n1", 2) // a restart that still fails does not raise it again
	beat(t, s, "n1", 2, `{"checks":[{"check":"codex.auth","status":"fail"},{"check":"stack.simctl","status":"fail","detail":"xcrun: error"}]}`)
	failed := nodeEvents(t, s, "node.check_failed")
	if len(failed) != 2 || failed[0].Ref != "codex.auth" || failed[1].Ref != "stack.simctl" || !failed[0].Attention || !strings.HasSuffix(failed[0].Summary, "check codex.auth failed: not logged in") {
		t.Fatalf("%+v", failed)
	}
	beat(t, s, "n1", 2, `{"checks":[]}`)
	passed := nodeEvents(t, s, "node.check_passed")
	if len(passed) != 2 || passed[0].Attention {
		t.Fatalf("%+v", passed)
	}
	beat(t, s, "n1", 2, `{"checks":[{"check":"codex.auth","status":"fail"}]}`)
	if got := nodeEvents(t, s, "node.check_failed"); len(got) != 3 {
		t.Fatalf("a new failure after recovery: %d events", len(got))
	}
}

// TestNodeHeartbeatStalled: the node's report of a stalled heartbeat becomes one attention event.
func TestNodeHeartbeatStalled(t *testing.T) {
	s, _ := clockStore(t)
	register(t, s, "n1", 1)
	beat(t, s, "n1", 1, `{"stalled_seconds":420}`)
	beat(t, s, "n1", 1, `{}`)
	got := nodeEvents(t, s, "node.heartbeat_stalled")
	if len(got) != 1 || !got[0].Attention || !strings.Contains(got[0].Summary, "no successful heartbeat for 7m0s") {
		t.Fatalf("%+v", got)
	}
}

// TestNodeOfflineOnline: a silent node is node.offline (no attention), after 10 more minutes node.offline with
// attention once, and node.online when it is back.
func TestNodeOfflineOnline(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	register(t, s, "n1", 1)
	if got := nodeEvents(t, s, "node.registered"); len(got) != 1 || got[0].Attention {
		t.Fatalf("%+v", got)
	}
	fake.Advance(NodeOffline + time.Second)
	if err := s.ExpireNodes(ctx, DefaultCompany); err != nil {
		t.Fatal(err)
	}
	off := nodeEvents(t, s, "node.offline")
	if len(off) != 1 || off[0].Attention {
		t.Fatalf("%+v", off)
	}
	fake.Advance(5 * time.Minute)
	_ = s.ExpireNodes(ctx, DefaultCompany)
	if got := nodeEvents(t, s, "node.offline"); len(got) != 1 {
		t.Fatal("attention before 10 min", got)
	}
	fake.Advance(5 * time.Minute)
	for range 2 {
		if err := s.ExpireNodes(ctx, DefaultCompany); err != nil {
			t.Fatal(err)
		}
	}
	off = nodeEvents(t, s, "node.offline")
	if len(off) != 2 || !off[1].Attention || !strings.Contains(off[1].Summary, "offline for more than 10m0s") {
		t.Fatalf("%+v", off)
	}
	attention, err := s.Feed(ctx, DefaultCompany, FeedFilter{Attention: true}, 0, 100)
	if err != nil || len(attention.Items) != 1 || attention.Items[0].Type != "node.offline" {
		t.Fatalf("%+v %v", attention.Items, err)
	}
	beat(t, s, "n1", 1, `{"disk":"ok"}`)
	beat(t, s, "n1", 1, `{"disk":"ok"}`)
	if got := nodeEvents(t, s, "node.online"); len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	// Offline again, back by a restart: node.online from the register.
	fake.Advance(NodeOffline + time.Second)
	_ = s.ExpireNodes(ctx, DefaultCompany)
	register(t, s, "n1", 2)
	if got := nodeEvents(t, s, "node.online"); len(got) != 2 {
		t.Fatalf("%+v", got)
	}
}

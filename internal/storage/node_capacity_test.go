package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// TestNodeTokens: a node token is shown once, stored hashed, bound to one node_id (at issue or by its first
// register), and stops working when revoked or when its node is removed.
func TestNodeTokens(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateNodeToken(ctx, co, " ", ""); !errors.Is(err, ErrInput) {
		t.Fatal("unnamed token issued", err)
	}
	if _, err := s.CreateNodeToken(ctx, co, "mini", "bad id!"); !errors.Is(err, ErrInput) {
		t.Fatal("token for an invalid node_id issued", err)
	}
	a, err := s.CreateNodeToken(ctx, co, "mini-1", "")
	if err != nil || !strings.HasPrefix(a.Token, NodeTokenPrefix) || a.NodeID != "" {
		t.Fatal(a, err)
	}
	var stored string
	if err = s.DB.QueryRow("SELECT hash FROM node_tokens WHERE id=?", a.ID).Scan(&stored); err != nil || strings.Contains(stored, a.Token) {
		t.Fatal("token stored in the clear", stored, err)
	}
	got, err := s.AuthenticateNodeToken(ctx, a.Token)
	if err != nil || got.ID != a.ID || got.Company != co || got.NodeID != "" || got.LastUsedAt != fake.Now().Unix() {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range []string{"", "worker-token", a.Token + "x", NodeTokenPrefix + "unknown"} {
		if _, err = s.AuthenticateNodeToken(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Fatal(bad, err)
		}
	}
	if err = s.BindNodeToken(ctx, co, a.ID, "n1"); err != nil {
		t.Fatal(err)
	}
	if err = s.BindNodeToken(ctx, co, a.ID, "n1"); err != nil {
		t.Fatal("binding again to the same node", err)
	}
	if err = s.BindNodeToken(ctx, co, a.ID, "n2"); !errors.Is(err, ErrNodeTokenBound) {
		t.Fatal("rebound to another node", err)
	}
	if got, _ = s.AuthenticateNodeToken(ctx, a.Token); got.NodeID != "n1" {
		t.Fatalf("%+v", got)
	}
	b, err := s.CreateNodeToken(ctx, co, "mini-2", "n2")
	if err != nil || b.NodeID != "n2" {
		t.Fatal(b, err)
	}
	list, err := s.NodeTokens(ctx, co)
	if err != nil || len(list) != 2 || list[0].NodeID != "n1" || list[1].NodeID != "n2" {
		t.Fatalf("%+v %v", list, err)
	}
	if raw, _ := json.Marshal(list); strings.Contains(string(raw), a.Token) || strings.Contains(string(raw), b.Token) {
		t.Fatal("list shows secrets")
	}
	r, err := s.RevokeNodeToken(ctx, co, a.ID)
	if err != nil || r.RevokedAt == 0 {
		t.Fatal(r, err)
	}
	if _, err = s.AuthenticateNodeToken(ctx, a.Token); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked token works", err)
	}
	if _, err = s.RevokeNodeToken(ctx, co, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// Removing a node revokes its tokens; an online node is not removed.
	register(t, s, "n2", 1)
	if err = s.RemoveNode(ctx, co, "n2"); !errors.Is(err, ErrConflict) {
		t.Fatal("online node removed", err)
	}
	fake.Advance(2 * NodeOffline)
	if err = s.ExpireNodes(ctx, co); err != nil {
		t.Fatal(err)
	}
	if err = s.RemoveNode(ctx, co, "n2"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateNodeToken(ctx, b.Token); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed node's token works", err)
	}
	if nodes, _ := s.Nodes(ctx, co); len(nodes) != 0 {
		t.Fatalf("%+v", nodes)
	}
}

// TestClaimLabels: an agent's spec.labels must all be node labels; a shortfall is retry/capacity with the
// missing labels, never drop.
func TestClaimLabels(t *testing.T) {
	s, _ := clockStore(t)
	register(t, s, "linux", 1, func(r *NodeRegistration) { r.Labels = []string{"linux"} })
	register(t, s, "mac", 1, func(r *NodeRegistration) { r.Labels = []string{"macos", "xcode"} })
	queue(t, s, "r1", "p1", "ios", `{"spec":{"labels":["macos","xcode"]}}`)
	_, err := claimV2(s, "r1", "linux", 1, "1-0-1")
	refused(t, err, true, "capacity")
	var r *Refusal
	if errors.As(err, &r); r.Detail != "node lacks labels macos, xcode" {
		t.Fatal(r.Detail)
	}
	if _, err = claimV2(s, "r1", "mac", 1, "1-0-1"); err != nil {
		t.Fatal(err)
	}
	// No labels needed: any node.
	queue(t, s, "r2", "p2", "web", "")
	if _, err = claimV2(s, "r2", "linux", 1, "1-0-2"); err != nil {
		t.Fatal(err)
	}
}

// TestCapacityHeldUntilSealed: a run holds its node slot and resource classes from its claim until it has
// ended in Core and the node no longer lists it unended (the node lists a run ended_unacked only after its
// seal); a new epoch releases what the old process held.
func TestCapacityHeldUntilSealed(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	register(t, s, "n1", 1, func(r *NodeRegistration) { r.Slots = 4 })
	sim := `{"spec":{"resources":{"simulator":1}}}`
	queue(t, s, "r1", "p1", "ios-a", sim)
	c, err := claimV2(s, "r1", "n1", 1, "1-0-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRun(ctx, co, "r1", c.Lease); err != nil {
		t.Fatal(err)
	}
	beat := func(epoch int64, inv ...InventoryRun) {
		t.Helper()
		if _, err := s.NodeHeartbeat(ctx, co, "n1", NodeHeartbeat{Epoch: epoch, Inventory: inv}); err != nil {
			t.Fatal(err)
		}
	}
	queue(t, s, "r2", "p2", "ios-b", sim)
	short := func(claim string) string {
		t.Helper()
		_, err := claimV2(s, "r2", "n1", 1, claim)
		var r *Refusal
		if err == nil {
			return ""
		}
		if !errors.As(err, &r) || !r.Retry || r.Reason != "capacity" {
			t.Fatal(err)
		}
		return r.Detail
	}
	if d := short("1-1-1"); d != "resource simulator: 1 of 1 held, the run needs 1" {
		t.Fatal(d)
	}
	// r1 ends in Core, but the node still seals it: it is listed running, so the simulator stays held.
	beat(1, InventoryRun{RunID: "r1", State: "running"})
	if err = end(t, s, "r1", c.Lease, protocol.Event{Status: "ENDED", Outcome: "succeeded", Result: "done"}); err != nil {
		t.Fatal(err)
	}
	if d := short("1-1-2"); d == "" {
		t.Fatal("an ended but unsealed run released its class")
	}
	beat(1, InventoryRun{RunID: "r1", State: "running"})
	if d := short("1-1-3"); d == "" {
		t.Fatal("released while still listed running")
	}
	// Sealed: the node lists it ended_unacked.
	beat(1, InventoryRun{RunID: "r1", State: "ended_unacked"})
	c2, err := claimV2(s, "r2", "n1", 1, "1-1-4")
	if err != nil {
		t.Fatal("sealed run still holds its class", err)
	}
	// Snapshots see the node's load.
	nodes := mustNodes(t, s)
	if nodes[0].Held != 1 || nodes[0].Used["simulator"] != 1 {
		t.Fatalf("%+v", nodes[0])
	}
	// r2 ends in Core while the node's last listing had it running; the node restarts (new epoch): the old
	// process is gone, so nothing is held any more.
	_, _ = s.StartRun(ctx, co, "r2", c2.Lease)
	beat(1, InventoryRun{RunID: "r2", State: "running"})
	if err = end(t, s, "r2", c2.Lease, protocol.Event{Status: "ENDED", Outcome: "succeeded", Result: "done"}); err != nil {
		t.Fatal(err)
	}
	register(t, s, "n1", 2, func(r *NodeRegistration) { r.Slots = 4 })
	queue(t, s, "r3", "p3", "ios-c", sim)
	if _, err = claimV2(s, "r3", "n1", 2, "2-0-1"); err != nil {
		t.Fatal("old epoch still holds", err)
	}
}

func mustNodes(t *testing.T, s *Store) []tasks.Node {
	t.Helper()
	tx, err := s.begin(context.Background(), DefaultCompany)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	nodes, err := nodesTx(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	return nodes
}

// TestDriverTransitions: register and heartbeat drivers update the node's drivers without a new epoch; each
// change between usable and not usable is one feed event (driver_unsupported with attention, naming the
// check), repeated declarations of the same state none; claims follow the heartbeat's drivers.
func TestDriverTransitions(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	ok := NodeDriver{Name: "codex", Version: "codex-cli 0.162.1", OK: true}
	authFail := NodeDriver{Name: "claude", Version: "2.1.284", OK: false, Check: "probe", Detail: "claude.auth: not logged in"}
	register(t, s, "n1", 1, func(r *NodeRegistration) { r.Drivers = []NodeDriver{ok, authFail} })
	events := func() []FeedItem {
		t.Helper()
		p, err := s.Feed(ctx, co, FeedFilter{}, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		var out []FeedItem
		for _, f := range p.Items {
			if strings.HasPrefix(f.Type, "driver_") {
				out = append(out, f)
			}
		}
		return out
	}
	e := events()
	if len(e) != 1 || e[0].Type != "driver_unsupported" || !e[0].Attention || e[0].Ref != "claude" || !strings.Contains(e[0].Summary, "probe check failed: claude.auth: not logged in") {
		t.Fatalf("%+v", e)
	}
	register(t, s, "n1", 2, func(r *NodeRegistration) { r.Drivers = []NodeDriver{ok, authFail} })
	if len(events()) != 1 {
		t.Fatal("the same failure declared again made an event")
	}
	queue(t, s, "r1", "p1", "writer", "")
	if _, err := s.DB.Exec("UPDATE runs SET spec=(spec::jsonb||jsonb_build_object('provider','claude'))::json WHERE id='r1'"); err != nil {
		t.Fatal(err)
	}
	_, err := claimV2(s, "r1", "n1", 2, "2-0-1")
	refused(t, err, true, "capacity")
	beat := func(drivers ...NodeDriver) {
		t.Helper()
		if _, err := s.NodeHeartbeat(ctx, co, "n1", NodeHeartbeat{Epoch: 2, Drivers: drivers}); err != nil {
			t.Fatal(err)
		}
	}
	claudeOK := NodeDriver{Name: "claude", Version: "2.1.284", OK: true}
	beat(ok, claudeOK)
	if e = events(); len(e) != 2 || e[1].Type != "driver_restored" || e[1].Attention {
		t.Fatalf("%+v", e)
	}
	if _, err = claimV2(s, "r1", "n1", 2, "2-0-2"); err != nil {
		t.Fatal("heartbeat drivers not used at claim", err)
	}
	beat(ok, claudeOK)
	if _, err := s.NodeHeartbeat(ctx, co, "n1", NodeHeartbeat{Epoch: 2}); err != nil { // no drivers: unchanged
		t.Fatal(err)
	}
	beat(NodeDriver{Name: "codex", Version: "9.9.9", OK: false, Check: "version", Detail: "unsupported version 9.9.9"}, claudeOK)
	beat(NodeDriver{Name: "codex", Version: "9.9.9", OK: false, Check: "version"}, claudeOK)
	if e = events(); len(e) != 3 || e[2].Type != "driver_unsupported" || e[2].Ref != "codex" || !strings.Contains(e[2].Summary, "version check failed") {
		t.Fatalf("%+v", e)
	}
	nodes, _ := s.Nodes(ctx, co)
	if nodes[0].Epoch != 2 || nodes[0].Drivers[0].OK || !nodes[0].Drivers[1].OK {
		t.Fatalf("%+v", nodes[0])
	}
}

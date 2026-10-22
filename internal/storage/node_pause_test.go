package storage

import (
	"context"
	"testing"
)

// TestNodePause (waits-events-cost-cli 5.2): a paused node claims no runs and its heartbeat answers pause;
// its live runs continue and other nodes keep claiming; resuming lifts it.
func TestNodePause(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	register(t, s, "n1", 1)
	register(t, s, "n2", 1)
	queue(t, s, "live", "p1", "dev", "")
	if _, err := claimV2(s, "live", "n1", 1, "1-0-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPause(ctx, co, Pause{Scope: "node", Target: "n1", Reason: "maintenance", Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	queue(t, s, "next", "p2", "web", "")
	refused(t, func() error { _, err := claimV2(s, "next", "n1", 1, "1-0-2"); return err }(), true, "paused")
	hb, err := s.NodeHeartbeat(ctx, co, "n1", NodeHeartbeat{Epoch: 1, Inventory: []InventoryRun{{RunID: "live", State: "claimed"}}})
	if err != nil || !hb.Pause || len(hb.Stop) != 0 {
		t.Fatalf("%+v %v", hb, err)
	}
	if st, _, _, _ := runRow(t, s, "live"); st != "CLAIMED" {
		t.Fatal("the paused node's live run was touched", st)
	}
	if _, err = claimV2(s, "next", "n2", 1, "1-0-1"); err != nil {
		t.Fatal("another node stopped claiming", err)
	}
	if hb, _ = s.NodeHeartbeat(ctx, co, "n2", NodeHeartbeat{Epoch: 1, Inventory: []InventoryRun{{RunID: "next", State: "claimed"}}}); hb.Pause {
		t.Fatal("n2 told to pause")
	}
	if err = s.ClearPause(ctx, co, "node", "n1", "owner"); err != nil {
		t.Fatal(err)
	}
	if hb, _ = s.NodeHeartbeat(ctx, co, "n1", NodeHeartbeat{Epoch: 1, Inventory: []InventoryRun{{RunID: "live", State: "claimed"}}}); hb.Pause {
		t.Fatal("still paused after resume")
	}
	page, _ := s.Feed(ctx, co, FeedFilter{}, 0, 100)
	paused := false
	for _, f := range page.Items {
		if f.Type == "owner.paused" && f.EntityID == "node:n1" {
			paused = true
		}
	}
	if !paused {
		t.Fatal("no owner.paused event for the node")
	}
}

// TestNodePauseByName: pausing a node by its registered name stores the pause under the node ID, so claims
// stop; resuming by name lifts it.
func TestNodePauseByName(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	register(t, s, "n1", 1)
	if err := s.SetPause(ctx, DefaultCompany, Pause{Scope: "node", Target: "mini-n1", Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	queue(t, s, "r1", "p1", "dev", "")
	refused(t, func() error { _, err := claimV2(s, "r1", "n1", 1, "1-0-1"); return err }(), true, "paused")
	if err := s.ClearPause(ctx, DefaultCompany, "node", "mini-n1", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := claimV2(s, "r1", "n1", 1, "1-0-2"); err != nil {
		t.Fatal("still paused after resume by name", err)
	}
}

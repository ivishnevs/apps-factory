package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"appfactory/internal/protocol"
)

// claimNodeID is the node claimStarted uses in every company of a test's store (testnode.Claim for the tests of this
// package, which cannot import testnode).
const claimNodeID = "test-claim-node"

// registerClaimNode serializes the first use of the claim node by tests that claim in parallel.
var registerClaimNode sync.Mutex

// claimNode registers the company's claim node on first use: every driver tests use, as many slots as a node may have,
// and no worktrees (a writer holds its project's workspace).
func claimNode(t testing.TB, s *Store, company string) {
	t.Helper()
	registerClaimNode.Lock()
	defer registerClaimNode.Unlock()
	ctx := context.Background()
	var n int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM nodes WHERE company_id=? AND id=?", company, claimNodeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n > 0 {
		return
	}
	r := NodeRegistration{NodeID: claimNodeID, Name: claimNodeID, Epoch: 1, ProtocolVersion: 2, Slots: 64, Resources: map[string]int{}, Labels: []string{}}
	for _, d := range []string{"codex", "claude", "fake"} {
		r.Drivers = append(r.Drivers, NodeDriver{Name: d, Version: "test", OK: true})
	}
	if _, err := s.RegisterNode(ctx, company, r); err != nil {
		t.Fatal(err)
	}
}

// claimStarted claims run as the claim node and starts it, so the test holds a RUNNING run with its lease. A refusal
// is ErrConflict and *Refusal.
func claimStarted(t testing.TB, s *Store, company, run string) (Claimed, error) {
	t.Helper()
	claimNode(t, s, company)
	ctx := context.Background()
	c, err := s.ClaimV2(ctx, company, run, ClaimRequest{NodeID: claimNodeID, Epoch: 1, ClaimID: protocol.ID()})
	var r *Refusal
	if errors.As(err, &r) {
		return c, fmt.Errorf("%w: %w", ErrConflict, r)
	}
	if err != nil {
		return c, err
	}
	if _, err = s.StartRun(ctx, company, run, c.Lease); err != nil {
		return c, err
	}
	return c, nil
}

// claimLease is claimStarted for tests that need only the lease.
func claimLease(t testing.TB, s *Store, company, run string) (string, error) {
	t.Helper()
	c, err := claimStarted(t, s, company, run)
	return c.Lease, err
}

// beatClaimNode sends the claim node's inventory heartbeat: its live runs, as running, and any other runs listed.
func beatClaimNode(t testing.TB, s *Store, company string, runs ...string) HeartbeatAnswer {
	t.Helper()
	ctx := context.Background()
	rows, err := s.DB.QueryContext(ctx, "SELECT id FROM runs WHERE company_id=? AND node_id=? AND status IN ('CLAIMED','RUNNING','CANCELLING')", company, claimNodeID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(runs, id) {
			runs = append(runs, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	hb := NodeHeartbeat{Epoch: 1, Health: json.RawMessage(`{"disk":"ok"}`)}
	for _, id := range runs {
		hb.Inventory = append(hb.Inventory, InventoryRun{RunID: id, State: "running"})
	}
	a, err := s.NodeHeartbeat(ctx, company, claimNodeID, hb)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// stopsRun says whether a heartbeat answer stops run, with any reason.
func stopsRun(a HeartbeatAnswer, run string) bool {
	return slices.ContainsFunc(a.Stop, func(s Stop) bool { return s.RunID == run })
}

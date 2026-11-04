package testnode

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// ClaimNodeID is the node Claim uses in every company of a store. It registers without enrolling: the store takes a
// claim from any registered node, and tests that call signed routes have an enrolled node of their own (Node.OwnRun).
const ClaimNodeID = "test-claim-node"

// registerClaimNode serializes the first use of the claim node by tests that claim in parallel.
var registerClaimNode sync.Mutex

// ensureClaimNode registers the company's claim node on first use: every driver tests use, as many slots as a node may
// have, and no worktrees (a writer holds its project's workspace).
func ensureClaimNode(t testing.TB, s *storage.Store, company string) {
	t.Helper()
	registerClaimNode.Lock()
	defer registerClaimNode.Unlock()
	ctx := context.Background()
	var n int
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM nodes WHERE company_id=$1 AND id=$2", company, ClaimNodeID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n > 0 {
		return
	}
	r := storage.NodeRegistration{NodeID: ClaimNodeID, Name: ClaimNodeID, Epoch: 1, ProtocolVersion: 2, Slots: 64, Resources: map[string]int{}, Labels: []string{}}
	for _, d := range []string{"codex", "claude", "fake"} {
		r.Drivers = append(r.Drivers, storage.NodeDriver{Name: d, Version: "test", OK: true})
	}
	if _, err := s.RegisterNode(ctx, company, r); err != nil {
		t.Fatal(err)
	}
}

// ClaimStarted claims run as the claim node and starts it, so the test holds a RUNNING run with its lease. A refusal
// is ErrConflict and *storage.Refusal.
func ClaimStarted(t testing.TB, s *storage.Store, company, run string) (storage.Claimed, error) {
	t.Helper()
	ensureClaimNode(t, s, company)
	ctx := context.Background()
	c, err := s.ClaimV2(ctx, company, run, storage.ClaimRequest{NodeID: ClaimNodeID, Epoch: 1, ClaimID: protocol.ID()})
	var r *storage.Refusal
	if errors.As(err, &r) {
		return c, fmt.Errorf("%w: %w", storage.ErrConflict, r)
	}
	if err != nil {
		return c, err
	}
	if _, err = s.StartRun(ctx, company, run, c.Lease); err != nil {
		return c, err
	}
	return c, nil
}

// Claim is ClaimStarted for tests that need only the lease.
func Claim(t testing.TB, s *storage.Store, company, run string) (string, error) {
	t.Helper()
	c, err := ClaimStarted(t, s, company, run)
	return c.Lease, err
}

// Beat sends the claim node's inventory heartbeat: its live runs, as running.
func Beat(t testing.TB, s *storage.Store, company string) storage.HeartbeatAnswer {
	t.Helper()
	ensureClaimNode(t, s, company)
	ctx := context.Background()
	rows, err := s.DB.QueryContext(ctx, "SELECT id FROM runs WHERE company_id=$1 AND node_id=$2 AND status IN ('CLAIMED','RUNNING','CANCELLING')", company, ClaimNodeID)
	if err != nil {
		t.Fatal(err)
	}
	hb := storage.NodeHeartbeat{Epoch: 1}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		hb.Inventory = append(hb.Inventory, storage.InventoryRun{RunID: id, State: "running"})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.NodeHeartbeat(ctx, company, ClaimNodeID, hb)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

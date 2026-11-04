package core

import (
	"context"
	"slices"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
)

func TestSleepGapDoesNotExpireLeasesUntilOneTTLOfContinuousTicks(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	s.Continuity = clock.NewContinuity(3 * tickInterval)
	c := &Core{Store: s}
	for _, id := range []string{"quiet", "renewed"} {
		if err = s.Create(ctx, storage.DefaultCompany, protocol.RunSpec{ID: id, Project: id, Agent: id, Provider: protocol.RuntimeCodex, Workspace: "/tmp/continuity-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	// One node per run: only one of them goes on beating.
	for _, id := range []string{"quiet", "renewed"} {
		if _, err = s.RegisterNode(ctx, storage.DefaultCompany, registration(id, 1)); err != nil {
			t.Fatal(err)
		}
		claim, err := s.ClaimV2(ctx, storage.DefaultCompany, id, storage.ClaimRequest{NodeID: id, Epoch: 1, ClaimID: "1-0-1"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.StartRun(ctx, storage.DefaultCompany, id, claim.Lease); err != nil {
			t.Fatal(err)
		}
	}
	beat := func(id string) storage.HeartbeatAnswer {
		t.Helper()
		a, err := s.NodeHeartbeat(ctx, storage.DefaultCompany, id, storage.NodeHeartbeat{Epoch: 1, Inventory: []storage.InventoryRun{{RunID: id, State: "running"}}})
		if err != nil {
			t.Fatal("heartbeat refused", err)
		}
		return a
	}
	status := func(id string) string {
		r, err := s.Get(ctx, storage.DefaultCompany, id)
		if err != nil {
			t.Fatal(err)
		}
		return r.Status
	}
	tick := func(d time.Duration) {
		fake.Advance(d)
		if err := c.maintain(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 10; i++ {
		tick(time.Second)
	}
	tick(2 * time.Hour) // the laptop slept; both leases are far past lease_until
	if status("quiet") != "RUNNING" || status("renewed") != "RUNNING" {
		t.Fatal("leases expired in the tick right after the wake")
	}
	beat("renewed")
	for elapsed := time.Second; elapsed <= storage.NodeOffline; elapsed += time.Second {
		tick(time.Second)
		if elapsed%(10*time.Second) == 0 {
			beat("renewed")
		}
		if status("quiet") != "RUNNING" {
			t.Fatalf("quiet lease expired %v after wake, before one TTL", elapsed)
		}
	}
	tick(time.Second)
	if status("quiet") != "ENDED" {
		t.Fatal("silent worker never declared lost", status("quiet"))
	}
	if status("renewed") != "RUNNING" {
		t.Fatal("renewed lease expired", status("renewed"))
	}
	if a := beat("quiet"); !slices.Contains(a.Stop, storage.Stop{RunID: "quiet", Reason: "terminal"}) {
		t.Fatal("lost lease revived", a.Stop)
	}
}

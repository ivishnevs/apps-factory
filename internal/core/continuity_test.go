package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

func TestSleepGapDoesNotExpireLeasesUntilOneTTLOfContinuousTicks(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	s.Continuity = clock.NewContinuity(3 * tickInterval)
	c := &Core{Store: s}
	for _, id := range []string{"quiet", "renewed"} {
		if err = s.Create(ctx, storage.DefaultCompany, protocol.RunSpec{ID: id, Project: id, Agent: id, Workspace: "/tmp/continuity-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	quietLease, err := s.Claim(ctx, storage.DefaultCompany, "quiet")
	if err != nil {
		t.Fatal(err)
	}
	renewedLease, err := s.Claim(ctx, storage.DefaultCompany, "renewed")
	if err != nil {
		t.Fatal(err)
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
	if _, err = s.Heartbeat(ctx, storage.DefaultCompany, "renewed", renewedLease); err != nil {
		t.Fatal("heartbeat after wake refused", err)
	}
	for elapsed := time.Second; elapsed <= storage.LeaseTTL; elapsed += time.Second {
		tick(time.Second)
		if elapsed%(10*time.Second) == 0 {
			if _, err = s.Heartbeat(ctx, storage.DefaultCompany, "renewed", renewedLease); err != nil {
				t.Fatal("heartbeat refused", err)
			}
		}
		if status("quiet") != "RUNNING" {
			t.Fatalf("quiet lease expired %v after wake, before one TTL", elapsed)
		}
	}
	tick(time.Second)
	if status("quiet") != "UNKNOWN" {
		t.Fatal("silent worker never declared lost", status("quiet"))
	}
	if status("renewed") != "RUNNING" {
		t.Fatal("renewed lease expired", status("renewed"))
	}
	if _, err = s.Heartbeat(ctx, storage.DefaultCompany, "quiet", quietLease); err == nil {
		t.Fatal("lost lease revived")
	}
}

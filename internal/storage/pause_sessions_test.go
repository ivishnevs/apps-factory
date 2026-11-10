package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"appfactory/internal/protocol"
)

// sessionHeldBy: task t's first run on node ended with session sess-1; its second run continues it, queued.
func sessionHeldBy(t *testing.T, s *Store, advance func(time.Duration), node string) {
	t.Helper()
	dispatchRun(t, s, "r1")
	c, err := claimV2(s, "r1", node, 1, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRun(context.Background(), DefaultCompany, "r1", c.Lease); err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, "r1", c.Lease, protocol.Event{Status: "ENDED", Outcome: "failed", SessionID: "sess-1", Usage: []protocol.Usage{{Model: "m", Input: 1}}}); err != nil {
		t.Fatal(err)
	}
	advance(time.Hour)
	dispatchRun(t, s, "r2")
}

// TestPauseNodeSessions (node-readiness 4.1; 2026-11-07: pausing MacBook-Air-Juliya held network-dev's run for 90
// minutes): the pause shows the held sessions and waiting runs, notices once, keeps them until the timeout or releases
// them at once; a released lock stays released at every later look.
func TestPauseNodeSessions(t *testing.T) {
	s, advance := readinessFixture(t, "n1", "n2")
	ctx := context.Background()
	co := DefaultCompany
	sessionHeldBy(t, s, advance, "n1")
	ns, err := s.NodeSessions(ctx, co, "n1")
	if err != nil || len(ns.Held) != 1 || ns.Held[0].SessionID != "sess-1" || ns.Held[0].TaskID != "t" || len(ns.Waiting) != 1 || ns.Waiting[0].RunID != "r2" || ns.ReleaseSeconds != int64(DefaultSessionAffinity/time.Second) {
		t.Fatalf("node sessions %+v %v", ns, err)
	}
	// Release now is refused while the holder is active: it takes the run itself.
	if err = s.ReleaseRunSession(ctx, co, "r2", "owner"); err == nil {
		t.Fatal("released the session of an active holder")
	}
	// Pause, keep: one notice naming the task and the release time; the run waits for n1.
	for i := 0; i < 2; i++ {
		if err = s.SetPause(ctx, co, Pause{Scope: "node", Target: "n1", Sessions: PauseSessionsKeep}); err != nil {
			t.Fatal(err)
		}
	}
	var notices int
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM feed_events WHERE company_id=? AND kind='node.paused_sessions' AND summary LIKE '%t (dev)%until%'", co).Scan(&notices); err != nil || notices != 1 {
		t.Fatalf("pause notices %d %v", notices, err)
	}
	refused(t, func() error { _, err := claimV2(s, "r2", "n2", 1, "c2"); return err }(), true, "affinity")
	if q, _ := s.QueuedWhy(ctx, co, "r2"); q == nil || q.Reason != QueuedAffinity || q.Until == 0 || !strings.Contains(q.Detail, "paused") {
		t.Fatalf("queued why %+v", q)
	}
	// Release now: the run goes to n2 with a fresh session, also after more looks at the lock.
	if err = s.ReleaseRunSession(ctx, co, "r2", "owner"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if q, _ := s.QueuedWhy(ctx, co, "r2"); q != nil && q.Reason == QueuedAffinity {
			t.Fatalf("look %d: released session locked again: %+v", i, q)
		}
	}
	if !offered(t, s, "n2", "r2") {
		t.Fatal("released run not offered to another node")
	}
	if _, err = claimV2(s, "r2", "n2", 1, "c3"); err != nil {
		t.Fatal(err)
	}
}

// TestPauseNodeReleasesSessions: the pause's "release now" answer frees every waiting run at once.
func TestPauseNodeReleasesSessions(t *testing.T) {
	s, advance := readinessFixture(t, "n1", "n2")
	ctx := context.Background()
	sessionHeldBy(t, s, advance, "n1")
	if err := s.SetPause(ctx, DefaultCompany, Pause{Scope: "node", Target: "n1", Sessions: PauseSessionsRelease}); err != nil {
		t.Fatal(err)
	}
	if ns, _ := s.NodeSessions(ctx, DefaultCompany, "n1"); len(ns.Held) != 1 || !ns.Held[0].Released {
		t.Fatalf("held %+v", ns.Held)
	}
	if _, err := claimV2(s, "r2", "n2", 1, "c2"); err != nil {
		t.Fatal("released session still holds the run:", err)
	}
	if err := s.SetPause(ctx, DefaultCompany, Pause{Scope: "project", Target: "game", Sessions: PauseSessionsRelease}); err == nil {
		t.Fatal("sessions accepted on a project pause")
	}
}

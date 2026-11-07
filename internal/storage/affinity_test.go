package storage

import (
	"context"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// TestSessionAffinity (session-affinity 2.2–2.4): a run continuing a session is taken only by the session's node
// while that node is active, however busy; an inactive holder starts the release timer, a holder back in time
// keeps the lock, and after the timeout another node takes the run.
func TestSessionAffinity(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "app", Name: "app", Workspace: "/tmp/affinity-app"}); err != nil {
		t.Fatal(err)
	}
	register(t, s, "n1", 1)
	register(t, s, "n2", 1)
	v, _ := s.CreateTask(ctx, co, tasks.Create{ID: "t", ProjectID: "app", Title: "t", Assignee: "dev", Ready: true, RequestID: "c"})
	first, err := s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r1", Project: "app", Agent: "dev", Provider: protocol.RuntimeCodex})
	if err != nil {
		t.Fatal(err)
	}
	c, err := claimV2(s, first.ID, "n1", 1, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRun(ctx, co, first.ID, c.Lease); err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, first.ID, c.Lease, protocol.Event{Status: "ENDED", Outcome: "failed", SessionID: "sess-1"}); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Hour) // past the retry backoff
	v, _ = s.Task(ctx, co, "t")
	second, err := s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r2", Project: "app", Agent: "dev", Provider: protocol.RuntimeCodex})
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionID != "sess-1" {
		t.Fatalf("second run does not continue the session: %q", second.SessionID)
	}
	offered := func(node string) bool {
		t.Helper()
		offers, err := s.Offers(ctx, co, node, 1, map[string]int{"codex": 4}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range offers {
			if o.RunID == second.ID {
				return true
			}
		}
		return false
	}
	// The holder is active: only it gets the run.
	if offered("n2") || !offered("n1") {
		t.Fatal("offers ignore the session's node")
	}
	refused(t, func() error { _, err := claimV2(s, second.ID, "n2", 1, "c2"); return err }(), true, "affinity")
	if q, err := s.QueuedWhy(ctx, co, second.ID); err != nil || q == nil || q.Reason != QueuedAffinity || q.Until != 0 {
		t.Fatalf("queued why %+v %v", q, err)
	}

	// The holder goes offline: the timer runs, the lock holds until it ends.
	fake.Advance(NodeOffline + time.Second)
	if err = s.ExpireNodes(ctx, co); err != nil {
		t.Fatal(err)
	}
	refused(t, func() error { _, err := claimV2(s, second.ID, "n2", 1, "c3"); return err }(), true, "affinity")
	if q, _ := s.QueuedWhy(ctx, co, second.ID); q == nil || q.Reason != QueuedAffinity || q.Until == 0 {
		t.Fatalf("queued why while the holder is offline %+v", q)
	}
	// Back before the timeout: the timer is cleared.
	register(t, s, "n1", 2)
	refused(t, func() error { _, err := claimV2(s, second.ID, "n2", 1, "c4"); return err }(), true, "affinity")
	var timers int
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM session_locks WHERE company_id=?", co).Scan(&timers); err != nil || timers != 0 {
		t.Fatal("timer kept for an active holder", timers, err)
	}

	// Offline again, past the release: another node takes the run.
	fake.Advance(NodeOffline + time.Second)
	if err = s.ExpireNodes(ctx, co); err != nil {
		t.Fatal(err)
	}
	refused(t, func() error { _, err := claimV2(s, second.ID, "n2", 1, "c5"); return err }(), true, "affinity")
	fake.Advance(DefaultSessionAffinity)
	if _, err = claimV2(s, second.ID, "n2", 1, "c6"); err != nil {
		t.Fatal("lock not released after the timeout", err)
	}

	// The company's timeout is a setting.
	if err = s.SetSessionAffinity(ctx, co, time.Minute); err == nil {
		t.Fatal("a one-minute release accepted")
	}
	if err = s.SetSessionAffinity(ctx, co, 3*time.Hour); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.SessionAffinity(ctx, co); d != 3*time.Hour {
		t.Fatal(d)
	}
}

// TestEscalationReopens: an escalation cleared and opened again with the same words asks a new question (the
// unique question index once crashed Core's tick on it).
func TestEscalationReopens(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "app", Name: "app", Workspace: "/tmp/reopen-app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "dep", ProjectID: "app", Title: "dep", RequestID: "d"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "t", ProjectID: "app", Title: "t", Assignee: "dev", Dependencies: []string{"dep"}, Ready: true, RequestID: "t"}); err != nil {
		t.Fatal(err)
	}
	want := []EscalationWant{{TaskID: "t", RefID: "dep", Summary: "waits on dep, which is in BACKLOG"}}
	for i := 0; i < 3; i++ {
		if err := s.SyncEscalations(ctx, co, "stalled", want); err != nil {
			t.Fatal(i, err)
		}
		if err := s.SyncEscalations(ctx, co, "stalled", nil); err != nil {
			t.Fatal(i, err)
		}
	}
}

// TestAcceptNodeAccount (node-runtime-accounts 3.3): an acceptance reaches the node once, in its poll work.
func TestAcceptNodeAccount(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	register(t, s, "n1", 1)
	if err := s.AcceptNodeAccount(ctx, DefaultCompany, "n1", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptNodeAccount(ctx, DefaultCompany, "n1", "basic"); err == nil {
		t.Fatal("an unknown runtime accepted")
	}
	w, err := s.NodeWork(ctx, DefaultCompany, "n1", 1)
	if err != nil || len(w.AcceptAccounts) != 1 || w.AcceptAccounts[0] != "claude" {
		t.Fatal(w.AcceptAccounts, err)
	}
	if w, _ = s.NodeWork(ctx, DefaultCompany, "n1", 1); len(w.AcceptAccounts) != 0 {
		t.Fatal("delivered twice")
	}
}

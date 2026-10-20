package storage

import (
	"appfactory/internal/protocol"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestDurableLifecycle(t *testing.T) {
	ctx := context.Background()
	path := testURL(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	spec := protocol.RunSpec{ID: "one", Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex, Workspace: "/tmp/p"}
	if err = s.Create(ctx, DefaultCompany, spec); err != nil {
		t.Fatal(err)
	}
	other := spec
	other.ID = "two"
	if err = s.Create(ctx, DefaultCompany, other); err == nil {
		t.Fatal("duplicate writer accepted")
	}
	out, err := s.Pending(ctx)
	if err != nil || len(out) != 1 {
		t.Fatalf("outbox not atomic: %v %v", out, err)
	}
	token, err := s.Claim(ctx, DefaultCompany, "one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(ctx, DefaultCompany, "one"); err == nil {
		t.Fatal("duplicate claim accepted")
	}
	bad := protocol.Wrap("completed", "one", "", protocol.Event{RunID: "one", Lease: "wrong", Status: "ENDED", Outcome: "succeeded"})
	if err = s.Apply(ctx, DefaultCompany, bad); err == nil {
		t.Fatal("bad lease accepted")
	}
	event := protocol.Wrap("completed", "one", "", protocol.Event{RunID: "one", Lease: token, SessionID: "thread-1", Status: "ENDED", Outcome: "succeeded", Result: "ok"})
	for i := 0; i < 2; i++ {
		if err = s.Apply(ctx, DefaultCompany, event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.Events(ctx, DefaultCompany, "one")
	if err != nil || len(events) != 1 {
		t.Fatal("duplicate event persisted", err)
	}
	if err = s.Apply(ctx, DefaultCompany, protocol.Wrap("progress", "one", "", protocol.Event{RunID: "one", Lease: token})); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	run, err := s.Get(ctx, DefaultCompany, "one")
	if err != nil || run.Status != "ENDED" || run.Outcome != "succeeded" || run.Result != "ok" || run.SessionID != "thread-1" {
		t.Fatalf("restart persistence: %+v %v", run, err)
	}
	if err = s.Create(ctx, DefaultCompany, other); err != nil {
		t.Fatal(err)
	}
}

// TestLeaseLossEndsTheRunLost: an expired v1 lease ends the run lost; it is neither revived nor claimed
// again, and there is nothing to reconcile (the v1 worker stops a run whose lease it lost).
func TestLeaseLossEndsTheRunLost(t *testing.T) {
	ctx := context.Background()
	s, err := Open(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	spec := protocol.RunSpec{ID: "r", Project: "p", Agent: "demo"}
	if err = s.Create(ctx, DefaultCompany, spec); err != nil {
		t.Fatal(err)
	}
	token, err := s.Claim(ctx, DefaultCompany, "r")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE runs SET lease_until=?", time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if err = s.Expire(ctx, DefaultCompany); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Heartbeat(ctx, DefaultCompany, "r", token); err == nil {
		t.Fatal("revived lost lease")
	}
	if _, err = s.Claim(ctx, DefaultCompany, "r"); err == nil {
		t.Fatal("blind retry allowed")
	}
	run, _ := s.Get(ctx, DefaultCompany, "r")
	if run.Status != "ENDED" || run.Outcome != "lost" {
		t.Fatal(run.Status, run.Outcome)
	}
	if err = s.Resolve(ctx, DefaultCompany, "r", "retry", "Inspected workspace; safe to start a new run"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestCancelQueued(t *testing.T) {
	s, err := Open(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	_ = s.Create(ctx, DefaultCompany, protocol.RunSpec{ID: "r", Project: "p", Agent: "a"})
	if err = s.Cancel(ctx, DefaultCompany, "r"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(ctx, DefaultCompany, "r"); err == nil {
		t.Fatal("cancelled run claimed")
	}
}
func TestEnvelopeRoundTrip(t *testing.T) {
	e := protocol.Wrap("x", "r", "", protocol.Event{RunID: "r"})
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var out protocol.Envelope
	if json.Unmarshal(b, &out) != nil || out.Version != 1 {
		t.Fatal("bad envelope")
	}
}
func TestSessionBelongsToProvider(t *testing.T) {
	ctx := context.Background()
	s, err := Open(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	spec := protocol.RunSpec{ID: "codex", Project: "p", Agent: "a", Provider: protocol.RuntimeCodex, Workspace: "/tmp/p"}
	if err = s.Create(ctx, DefaultCompany, spec); err != nil {
		t.Fatal(err)
	}
	token, err := s.Claim(ctx, DefaultCompany, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Apply(ctx, DefaultCompany, protocol.Wrap("completed", "codex", "", protocol.Event{RunID: "codex", Lease: token, SessionID: "thread-1", Status: "ENDED", Outcome: "succeeded"})); err != nil {
		t.Fatal(err)
	}
	direct := protocol.RunSpec{ID: "direct", Project: "p", Agent: "a", Provider: protocol.RuntimeClaude, Workspace: "/tmp/p", SessionID: "thread-1"}
	if err = s.Create(ctx, DefaultCompany, direct); !errors.Is(err, ErrInput) {
		t.Fatal("cross-provider resume accepted", err)
	}
	continuation := direct
	continuation.ID, continuation.MessageID = "message", "m1"
	if err = s.Create(ctx, DefaultCompany, continuation); err != nil {
		t.Fatal(err)
	}
	if run, err := s.Get(ctx, DefaultCompany, "message"); err != nil || run.SessionID != "" || run.Spec.SessionID != "" {
		t.Fatalf("continuation kept foreign session: %+v %v", run, err)
	}
}

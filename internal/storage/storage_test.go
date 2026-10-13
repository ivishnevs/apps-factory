package storage

import (
	"appfactory/internal/protocol"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestDurableLifecycle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	spec := protocol.RunSpec{ID: "one", Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex, Workspace: "/tmp/p"}
	if err = s.Create(ctx, spec); err != nil {
		t.Fatal(err)
	}
	other := spec
	other.ID = "two"
	if err = s.Create(ctx, other); err == nil {
		t.Fatal("duplicate writer accepted")
	}
	out, err := s.Pending(ctx)
	if err != nil || len(out) != 1 {
		t.Fatalf("outbox not atomic: %v %v", out, err)
	}
	token, err := s.Claim(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(ctx, "one"); err == nil {
		t.Fatal("duplicate claim accepted")
	}
	bad := protocol.Wrap("completed", "one", "", protocol.Event{RunID: "one", Lease: "wrong", Status: "COMPLETED"})
	if err = s.Apply(ctx, bad); err == nil {
		t.Fatal("bad lease accepted")
	}
	event := protocol.Wrap("completed", "one", "", protocol.Event{RunID: "one", Lease: token, SessionID: "thread-1", Status: "COMPLETED", Result: "ok"})
	for i := 0; i < 2; i++ {
		if err = s.Apply(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.Events(ctx, "one")
	if err != nil || len(events) != 1 {
		t.Fatal("duplicate event persisted", err)
	}
	if err = s.Apply(ctx, protocol.Wrap("progress", "one", "", protocol.Event{RunID: "one", Lease: token})); err != nil {
		t.Fatal(err)
	}
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	run, err := s.Get(ctx, "one")
	if err != nil || run.Status != "COMPLETED" || run.Result != "ok" || run.SessionID != "thread-1" {
		t.Fatalf("restart persistence: %+v %v", run, err)
	}
	if err = s.Create(ctx, other); err != nil {
		t.Fatal(err)
	}
}
func TestLeaseLossRequiresReconciliation(t *testing.T) {
	ctx := context.Background()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	spec := protocol.RunSpec{ID: "r", Project: "p", Agent: "demo"}
	if err = s.Create(ctx, spec); err != nil {
		t.Fatal(err)
	}
	token, err := s.Claim(ctx, "r")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE runs SET lease_until=?", time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if err = s.Expire(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Heartbeat(ctx, "r", token); err == nil {
		t.Fatal("revived lost lease")
	}
	if _, err = s.Claim(ctx, "r"); err == nil {
		t.Fatal("blind retry allowed")
	}
	run, _ := s.Get(ctx, "r")
	if run.Status != "UNKNOWN" {
		t.Fatal(run.Status)
	}
	if err = s.Resolve(ctx, "r", "FAILED", "Inspected workspace; safe to start a new run"); err != nil {
		t.Fatal(err)
	}
}
func TestCancelQueued(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx := context.Background()
	_ = s.Create(ctx, protocol.RunSpec{ID: "r", Project: "p", Agent: "a"})
	if err = s.Cancel(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(ctx, "r"); err == nil {
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

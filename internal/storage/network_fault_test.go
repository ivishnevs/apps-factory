package storage

import (
	"context"
	"testing"

	"appfactory/internal/protocol"
)

// TestNetworkFaultKeepsAttempts (setup-assistant 4.2, 5.2; 2026-11-11: two of three attempts burnt on mac2's broken
// DNS): a run that ends with ENOTFOUND for the API host is the node's fault even after its agent used tokens: the
// task keeps its attempts and goes to another node; the faulted node gets none of the project's runs for now.
func TestNetworkFaultKeepsAttempts(t *testing.T) {
	s, _ := readinessFixture(t, "n1", "n2")
	ctx := context.Background()
	dispatchRun(t, s, "r1")
	c, err := claimV2(s, "r1", "n1", 1, "c1")
	if err != nil {
		t.Fatal(err)
	}
	// An older node reports the text only; Core classifies it.
	if err = end(t, s, "r1", c.Lease, protocol.Event{Status: "ENDED", Outcome: "failed", Result: "API Error: Connection error. getaddrinfo ENOTFOUND api.anthropic.com",
		Usage: []protocol.Usage{{Model: "m", Input: 1200, Output: 30}}}); err != nil {
		t.Fatal(err)
	}
	var outcome, fault string
	if err = s.DB.QueryRowContext(ctx, "SELECT outcome,node_fault FROM runs WHERE id='r1'").Scan(&outcome, &fault); err != nil || outcome != "setup_failed" || fault != protocol.FaultNetwork {
		t.Fatalf("run r1: %s %q %v", outcome, fault, err)
	}
	v, _ := s.Task(ctx, DefaultCompany, "t")
	if v.Status != "READY" || v.AttemptsNoProgress != 0 || v.FailStreak != 0 || v.NotBefore != 0 {
		t.Fatalf("a network fault counted against the task: %s attempts %d streak %d not_before %d", v.Status, v.AttemptsNoProgress, v.FailStreak, v.NotBefore)
	}
	dispatchRun(t, s, "r2")
	if offered(t, s, "n1", "r2") || !offered(t, s, "n2", "r2") {
		t.Fatal("the retry is not offered to another node")
	}
	// A network error to another host (the agent's own npm install) is the task's failure.
	c, err = claimV2(s, "r2", "n2", 1, "c2")
	if err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, "r2", c.Lease, protocol.Event{Status: "ENDED", Outcome: "failed", Result: "npm ERR! getaddrinfo ENOTFOUND registry.npmjs.org",
		Usage: []protocol.Usage{{Model: "m", Input: 10, Output: 5}}}); err != nil {
		t.Fatal(err)
	}
	if v, _ = s.Task(ctx, DefaultCompany, "t"); v.AttemptsNoProgress != 1 {
		t.Fatalf("the task's own failure not counted: %d", v.AttemptsNoProgress)
	}
}

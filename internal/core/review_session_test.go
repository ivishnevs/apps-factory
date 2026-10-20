package core

import (
	"context"
	"encoding/json"
	"testing"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// TestReviewKeepsImplementerSession: sessions are keyed by (task, agent). After the developer submits and
// the reviewer (another runtime) approves, the developer's next run resumes the developer's session,
// not the reviewer's (which Core used to take from the task's last run and then drop as foreign).
func TestReviewKeepsImplementerSession(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	if h.c.Agents["claude-demo"].Spec.Runtime.Provider == h.c.Agents["demo"].Spec.Runtime.Provider {
		t.Fatal("the test needs developer and reviewer on different runtimes")
	}
	started := func(r exitRun, session string) {
		t.Helper()
		if err := h.s.Apply(ctx, storage.DefaultCompany, protocol.Wrap("agent.started", r.id, "", protocol.Event{RunID: r.id, Lease: r.lease, SessionID: session})); err != nil {
			t.Fatal(err)
		}
	}
	exit := func(r exitRun, kind string, args map[string]any, facts *storage.ExitFacts) {
		t.Helper()
		var brief struct {
			AckSeq int64 `json:"ack_seq"`
		}
		_ = json.Unmarshal(h.do(r.token, "GET", "/agent/brief", nil, nil).Body.Bytes(), &brief)
		if w := h.do(r.token, "POST", "/agent/exit", map[string]any{"kind": kind, "arguments": args, "ack_seq": brief.AckSeq, "facts": facts}, nil); w.Code != 200 {
			t.Fatal(kind, w.Code, w.Body.String())
		}
	}
	h.create("s", "code", "claude-demo")
	dev := h.start("s")
	if run, _ := h.s.Get(ctx, storage.DefaultCompany, dev.id); run.Spec.SessionID != "" {
		t.Fatalf("first run resumes %q", run.Spec.SessionID)
	}
	started(dev, "dev-session")
	exit(dev, "submit", map[string]any{"summary": "s", "checks": []string{"go test ./..."}, "reviewer": "demo"}, clean("h1", nil))
	h.end(dev, "succeeded", "accepted")

	rv := h.start("s")
	if run, _ := h.s.Get(ctx, storage.DefaultCompany, rv.id); run.Spec.Agent != "demo" || run.Spec.SessionID != "" {
		t.Fatalf("reviewer run: agent %s session %q", run.Spec.Agent, run.Spec.SessionID)
	}
	started(rv, "reviewer-thread")
	exit(rv, "handoff", map[string]any{"to": "claude-demo", "note": "ok", "verdict": "approved"}, clean("h1", &storage.PRFacts{Number: 1, State: "open", HeadSHA: "h1"}))
	h.end(rv, "succeeded", "accepted")

	again := h.start("s")
	if run, _ := h.s.Get(ctx, storage.DefaultCompany, again.id); run.Spec.Agent != "claude-demo" || run.Spec.SessionID != "dev-session" {
		t.Fatalf("developer run after approval: agent %s session %q, want dev-session", run.Spec.Agent, run.Spec.SessionID)
	}
}

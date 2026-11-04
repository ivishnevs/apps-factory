package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/testnode"
)

type pollResult struct {
	code int
	body string
	took time.Duration
}

// pollAsync starts a signed poll of n and returns its result channel.
func pollAsync(t *testing.T, c *Core, n testnode.Node, req PollRequest) <-chan pollResult {
	t.Helper()
	if req.HooksHash == "" { // the node applied the document its register answer delivered
		req.HooksHash = config.DeliverNodeHooks(c.cat(storage.DefaultCompany).NodeHooks, "").Hash
	}
	out := make(chan pollResult, 1)
	go func() {
		start := time.Now()
		code, body := signedCall(t, c, n, "POST", "/nodes/"+n.ID+"/poll", req)
		out <- pollResult{code, body, time.Since(start)}
	}()
	return out
}

func answerOf(t *testing.T, r pollResult) PollAnswer {
	t.Helper()
	if r.code != 200 {
		t.Fatalf("poll: %d %s", r.code, r.body)
	}
	var a PollAnswer
	if err := json.Unmarshal([]byte(r.body), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func waitResult(t *testing.T, ch <-chan pollResult, within time.Duration) pollResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(within):
		t.Fatalf("no poll answer within %s", within)
	}
	return pollResult{}
}

func queuedRun(t *testing.T, s *storage.Store, company, id string) {
	t.Helper()
	spec := protocol.RunSpec{ID: id, Project: "poll-project-" + id, Agent: "dev", Provider: protocol.RuntimeCodex, Workspace: t.TempDir(), Prompt: "work", TimeoutSeconds: 60}
	if err := s.Create(context.Background(), company, spec); err != nil {
		t.Fatal(err)
	}
}

// TestNodePoll (http-node-transport 3.1–3.3): a poll answers at once when work is there, on the commit that queues
// a run while it waits, empty after its wait; a newer poll ends the older one; a paused node gets no offers; another
// company's runs are never offered; an urgent stop wakes the run's node; a revoked node's poll ends with 401.
func TestNodePoll(t *testing.T) {
	c, s, _ := nodeCore(t)
	saved := pollRecheck
	pollRecheck = 50 * time.Millisecond
	t.Cleanup(func() { pollRecheck = saved })
	ctx := context.Background()
	co := storage.DefaultCompany
	a := testnode.Enroll(t, s, co)
	if code, out := signedCall(t, c, a, "POST", "/nodes/register", registration(a.ID, 1)); code != 200 {
		t.Fatal(code, out)
	}
	free := map[string]int{"codex": 2, "claude": 1}

	// Nothing to do: empty after the wait.
	r := waitResult(t, pollAsync(t, c, a, PollRequest{Epoch: 1, Free: free, WaitSeconds: 1}), 5*time.Second)
	if got := answerOf(t, r); len(got.Offers) != 0 || r.took < 900*time.Millisecond {
		t.Fatalf("idle poll %+v after %s", got, r.took)
	}

	// A run queued while the poll waits is offered at once (the commit wakes the poll).
	waiting := pollAsync(t, c, a, PollRequest{Epoch: 1, Free: free, WaitSeconds: 20})
	time.Sleep(200 * time.Millisecond)
	queuedRun(t, s, co, "poll-run-1")
	r = waitResult(t, waiting, 3*time.Second)
	if got := answerOf(t, r); len(got.Offers) != 1 || got.Offers[0].RunID != "poll-run-1" || got.Offers[0].Provider != "codex" || r.took > 2*time.Second {
		t.Fatalf("offer %+v after %s", got, r.took)
	}
	// Queued work is answered at once; no more than the free slots, never a skipped run.
	queuedRun(t, s, co, "poll-run-2")
	if got := answerOf(t, waitResult(t, pollAsync(t, c, a, PollRequest{Epoch: 1, Free: map[string]int{"codex": 1}, WaitSeconds: 5}), time.Second)); len(got.Offers) != 1 || got.Offers[0].RunID != "poll-run-1" {
		t.Fatalf("one free slot: %+v", got.Offers)
	}
	if got := answerOf(t, waitResult(t, pollAsync(t, c, a, PollRequest{Epoch: 1, Free: free, Skip: []string{"poll-run-1"}, WaitSeconds: 5}), time.Second)); len(got.Offers) != 1 || got.Offers[0].RunID != "poll-run-2" {
		t.Fatalf("skip: %+v", got.Offers)
	}
	// No driver with free slots, no offers.
	if got := answerOf(t, waitResult(t, pollAsync(t, c, a, PollRequest{Epoch: 1, Free: map[string]int{"claude": 3}, WaitSeconds: 1}), 3*time.Second)); len(got.Offers) != 0 {
		t.Fatalf("claude only: %+v", got.Offers)
	}

	// A newer poll of the node ends the older one with an empty answer.
	older := pollAsync(t, c, a, PollRequest{Epoch: 1, Free: map[string]int{}, WaitSeconds: 20})
	time.Sleep(200 * time.Millisecond)
	newer := pollAsync(t, c, a, PollRequest{Epoch: 1, Free: map[string]int{}, WaitSeconds: 1})
	if r := waitResult(t, older, 3*time.Second); r.took > 3*time.Second || len(answerOf(t, r).Offers) != 0 {
		t.Fatal("older poll", r)
	}
	waitResult(t, newer, 3*time.Second)

	// Another company's queued run is never offered.
	if err := s.EnsureCompany(ctx, "other-co"); err != nil {
		t.Fatal(err)
	}
	queuedRun(t, s, "other-co", "poll-other-run")
	if got := answerOf(t, waitResult(t, pollAsync(t, c, a, PollRequest{Epoch: 1, Free: map[string]int{"codex": 10}, WaitSeconds: 1}), 3*time.Second)); len(got.Offers) != 2 {
		t.Fatalf("offers %+v", got.Offers)
	} else {
		for _, o := range got.Offers {
			if o.RunID == "poll-other-run" {
				t.Fatal("offered another company's run")
			}
		}
	}

	// A paused node gets no offers.
	if err := s.SetPause(ctx, co, storage.Pause{Scope: "node", Target: a.ID, Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	if got := answerOf(t, waitResult(t, pollAsync(t, c, a, PollRequest{Epoch: 1, Free: free, WaitSeconds: 1}), 3*time.Second)); len(got.Offers) != 0 {
		t.Fatalf("paused node got %+v", got.Offers)
	}
	if err := s.ClearPause(ctx, co, "node", a.ID, "owner"); err != nil {
		t.Fatal(err)
	}

	// An older epoch is refused like the heartbeat refuses it.
	if r := waitResult(t, pollAsync(t, c, a, PollRequest{Epoch: 2, Free: free, WaitSeconds: 1}), 3*time.Second); r.code != 404 {
		t.Fatalf("unknown epoch: %+v", r)
	}

	// An urgent stop of a claimed run wakes the node's waiting poll with the stop.
	claim := struct {
		Lease string `json:"lease"`
	}{}
	code, out := signedCall(t, c, a, "POST", "/runs/poll-run-1/claim", storage.ClaimRequest{NodeID: a.ID, Epoch: 1, ClaimID: "c1"})
	if code != 200 {
		t.Fatal(code, out)
	}
	_ = json.Unmarshal([]byte(out), &claim)
	if code, out = signedCall(t, c, a, "POST", "/runs/poll-run-1/start", map[string]string{"lease": claim.Lease}); code != 200 {
		t.Fatal(code, out)
	}
	waiting = pollAsync(t, c, a, PollRequest{Epoch: 1, Free: map[string]int{}, WaitSeconds: 20})
	time.Sleep(200 * time.Millisecond)
	if err := s.Cancel(ctx, co, "poll-run-1"); err != nil {
		t.Fatal(err)
	}
	r = waitResult(t, waiting, 3*time.Second)
	if got := answerOf(t, r); len(got.Stop) != 1 || got.Stop[0].RunID != "poll-run-1" || got.Stop[0].Reason != "revoked" {
		t.Fatalf("stop %+v", got)
	}
	// The same stop is not answered again; the poll waits (the heartbeat keeps listing it).
	if r = waitResult(t, pollAsync(t, c, a, PollRequest{Epoch: 1, Free: map[string]int{}, WaitSeconds: 1}), 3*time.Second); len(answerOf(t, r).Stop) != 0 || r.took < 900*time.Millisecond {
		t.Fatalf("stop answered twice: %+v", r)
	}

	// Revoking the node ends its waiting poll with 401.
	waiting = pollAsync(t, c, a, PollRequest{Epoch: 1, Free: map[string]int{}, WaitSeconds: 20})
	time.Sleep(200 * time.Millisecond)
	if err := s.RevokeNode(ctx, co, a.ID); err != nil {
		t.Fatal(err)
	}
	if r = waitResult(t, waiting, 3*time.Second); r.code != 401 {
		t.Fatalf("revoked poll: %+v", r)
	}
}

// TestRunEvents (http-node-transport 3.4): batches apply in sequence order, once; a gap names the expected sequence;
// another lease is refused; another node's run is not found.
func TestRunEvents(t *testing.T) {
	c, s, _ := nodeCore(t)
	co := storage.DefaultCompany
	a, b := testnode.Enroll(t, s, co), testnode.Enroll(t, s, co)
	for _, n := range []testnode.Node{a, b} {
		if code, out := signedCall(t, c, n, "POST", "/nodes/register", registration(n.ID, 1)); code != 200 {
			t.Fatal(code, out)
		}
	}
	queuedRun(t, s, co, "events-run")
	var claim struct {
		Lease string `json:"lease"`
	}
	code, out := signedCall(t, c, a, "POST", "/runs/events-run/claim", storage.ClaimRequest{NodeID: a.ID, Epoch: 1, ClaimID: "c1"})
	if code != 200 {
		t.Fatal(code, out)
	}
	_ = json.Unmarshal([]byte(out), &claim)
	if code, out = signedCall(t, c, a, "POST", "/runs/events-run/start", map[string]string{"lease": claim.Lease}); code != 200 {
		t.Fatal(code, out)
	}
	event := func(seq int64, kind string, p protocol.Event) storage.SeqEvent {
		p.RunID, p.Lease = "events-run", claim.Lease
		return storage.SeqEvent{Seq: seq, Event: protocol.Wrap("agent."+kind, "events-run", "", p)}
	}
	e1, e2 := event(1, "started", protocol.Event{SessionID: "s1"}), event(2, "progress", protocol.Event{})
	e3 := event(3, "completed", protocol.Event{Status: "ENDED", Outcome: "succeeded", Result: "ok"})
	post := func(n testnode.Node, lease string, events ...storage.SeqEvent) (int, string) {
		return signedCall(t, c, n, "POST", "/runs/events-run/events", EventBatch{Lease: lease, Events: events})
	}
	if code, out = post(a, claim.Lease, e1, e2); code != 200 || !strings.Contains(out, `"applied_seq":2`) {
		t.Fatal(code, out)
	}
	// Sent twice after a timeout: applied once, same answer.
	if code, out = post(a, claim.Lease, e1, e2); code != 200 || !strings.Contains(out, `"applied_seq":2`) {
		t.Fatal(code, out)
	}
	// A gap names the expected sequence.
	if code, out = post(a, claim.Lease, event(4, "progress", protocol.Event{})); code != 409 || !strings.Contains(out, `"expected_seq":3`) {
		t.Fatal(code, out)
	}
	// Another node's run is not its business; another lease is refused.
	if code, out = post(b, claim.Lease, e3); code != 403 {
		t.Fatal(code, out)
	}
	stale := event(3, "completed", protocol.Event{Status: "ENDED", Outcome: "succeeded"})
	if code, out = post(a, "other-lease", stale); code != 409 || !strings.Contains(out, `"reason":"lease"`) {
		t.Fatal(code, out)
	}
	if code, out = post(a, claim.Lease, e2, e3); code != 200 || !strings.Contains(out, `"applied_seq":3`) {
		t.Fatal(code, out)
	}
	events, err := s.Events(context.Background(), co, "events-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("%d events stored", len(events))
	}
	run, err := s.Get(context.Background(), co, "events-run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "ENDED" || run.Outcome != "succeeded" || run.SessionID != "s1" {
		t.Fatalf("%+v", run)
	}
	// A run Core does not know is not found.
	if code, out = signedCall(t, c, a, "POST", "/runs/no-such-run/events", EventBatch{Lease: "x", Events: []storage.SeqEvent{e1}}); code != 404 {
		t.Fatal(code, out)
	}
}

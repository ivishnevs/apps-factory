package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// TestReplayConversationBehindWork (work-and-conversation-runs 4.1) replays 2026-11-08 without a sandbox:
// network-dev with work limit 1 and one live work run answers a producer question at once, in the conversation slot
// of a node whose work slot is full; the chat (the conversation task's clearer) says what happens meanwhile; the
// producer's work runs keep the model's window while its conversation compacts at 200k.
func TestReplayConversationBehindWork(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	net := h.c.Agents["demo"] // codex, max_concurrent_runs 1
	net.Metadata.Name = "network-dev"
	h.c.Agents["network-dev"] = net
	producer := h.c.Agents["claude-demo"]
	producer.Metadata.Name = "producer"
	producer.Spec.Runtime.ContextWindow = config.ContextWindow{Work: "max", Conversation: "200k"}
	h.c.Agents["producer"] = producer
	if _, err := h.s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "plans", Name: "plans", Workspace: "/tmp/exits-plans"}); err != nil {
		t.Fatal(err)
	}
	// The Mac: one work slot, one conversation slot.
	if _, err := h.s.RegisterNode(ctx, co, storage.NodeRegistration{NodeID: "mac", Name: "mac", Epoch: 1, ProtocolVersion: 2, Slots: 1, ConversationSlots: 1,
		Drivers: []storage.NodeDriver{{Name: "codex", OK: true}, {Name: "claude", OK: true}}, Resources: map[string]int{}, Labels: []string{}}); err != nil {
		t.Fatal(err)
	}
	claim := func(run string, slot int) {
		t.Helper()
		c, err := h.s.ClaimV2(ctx, co, run, storage.ClaimRequest{NodeID: "mac", Epoch: 1, ClaimID: "c-" + run, Slot: slot})
		if err != nil {
			t.Fatalf("claim %s in slot %d: %v", run, slot, err)
		}
		if _, err = h.s.StartRun(ctx, co, run, c.Lease); err != nil {
			t.Fatal(err)
		}
	}
	schedule := func() {
		t.Helper()
		if err := h.c.Schedule(ctx); err != nil {
			t.Fatal(err)
		}
	}
	h.create("w1", "notes", "network-dev")
	schedule()
	claim(h.task("w1").CurrentRunID, 0)
	h.create("w2", "notes", "network-dev")
	schedule()
	if v := h.verdict("w2"); v.Reason != tasks.ReasonCapacity || v.Wait == nil || v.Wait.Kind != tasks.WaitAgent {
		t.Fatalf("second work task: %+v", v)
	}

	// The producer asks network-dev.
	if _, err := h.s.SendMessage(ctx, co, messaging.Send{ProjectID: "notes", Sender: "producer", Recipient: "network-dev", MessageType: "question", Body: "Which port does the relay use?", RequestID: "q1"}); err != nil {
		t.Fatal(err)
	}
	conv := storage.ConversationID("notes", "network-dev")
	schedule()
	run := h.task(conv).CurrentRunID
	if run == "" {
		t.Fatalf("the conversation waits behind work: %+v", h.verdict(conv))
	}
	// While it waits for its claim the chat says so (the task's clearer, as the overview serves it).
	var view struct {
		Reason  string        `json:"reason"`
		Clearer tasks.Clearer `json:"clearer"`
	}
	if w := h.do(h.c.Token, "GET", "/tasks/"+conv, nil, nil); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || !strings.HasPrefix(view.Clearer.Action, "starting: ") {
		t.Fatalf("waiting clearer: %d %s", w.Code, w.Body.String())
	}
	offers, err := h.s.OffersFor(ctx, co, "mac", 1, map[string]int{}, map[string]int{"codex": 1}, nil)
	if err != nil || len(offers) != 1 || offers[0].RunID != run || !offers[0].Conversation {
		t.Fatalf("offers for the conversation slot %+v %v", offers, err)
	}
	claim(run, 1) // the work slot is full: the conversation slot answers, at once (the clock did not move)
	if v := h.verdict(conv); v.Reason != tasks.ReasonLiveRun || v.Clearer.Action != "answering now: messages from producer (started just now)" {
		t.Fatalf("answering clearer: %+v", v)
	}

	// The producer's windows per run kind (on a second node: the Mac is full).
	if _, err = h.s.RegisterNode(ctx, co, storage.NodeRegistration{NodeID: "hetzner", Name: "hetzner", Epoch: 1, ProtocolVersion: 2, Slots: 4,
		Drivers: []storage.NodeDriver{{Name: "claude", OK: true}}, Resources: map[string]int{}, Labels: []string{}}); err != nil {
		t.Fatal(err)
	}
	h.create("spec", "plans", "producer")
	if _, err = h.s.SendMessage(ctx, co, messaging.Send{ProjectID: "notes", Sender: "owner", Recipient: "producer", MessageType: "notification", Body: "How is the plan going?", RequestID: "o1"}); err != nil {
		t.Fatal(err)
	}
	schedule()
	window := func(task string) any {
		t.Helper()
		r, err := h.s.Get(ctx, co, h.task(task).CurrentRunID)
		if err != nil {
			t.Fatalf("%s: %v %+v", task, err, h.verdict(task))
		}
		return r.Spec.RuntimeOptions["context_window"]
	}
	if w := window("spec"); w != nil {
		t.Fatalf("the producer's spec run has window %v, want the model's maximum", w)
	}
	if w := window(storage.ConversationID("notes", "producer")); w != float64(200000) {
		t.Fatalf("the producer's conversation run has window %v, want 200000", w)
	}
}

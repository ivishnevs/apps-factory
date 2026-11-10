package tasks

import (
	"strings"
	"testing"
	"time"
)

// work-and-conversation-runs 1.2: a conversation task waits for its own cap only; a work task for the work cap; with
// a shared limit both kinds count together.
func TestEvaluateRunKinds(t *testing.T) {
	work := &Run{ID: "rw", Status: "RUNNING", TaskID: "w"}
	conv := &Run{ID: "rc", Status: "RUNNING", TaskID: "conv-dev-tools"}
	convTask := func() Snapshot {
		s := ready()
		s.Task.Kind, s.Task.ID = KindConversation, "conv-dev-app"
		return s
	}
	// Work limit 1 with a live work run: the conversation is runnable, a work task waits.
	s := convTask()
	s.AgentRun, s.AgentLive, s.AgentCap = work, 1, 1
	if v := Evaluate(s); v.Reason != ReasonRunnable {
		t.Fatalf("conversation behind work: %+v", v)
	}
	w := ready()
	w.AgentRun, w.AgentLive, w.AgentCap = work, 1, 1
	w.AgentConversationRun, w.AgentConversationLive = conv, 1
	if v := Evaluate(w); v.Reason != ReasonCapacity || v.Wait.Kind != WaitAgent {
		t.Fatalf("work task: %+v", v)
	}
	// Work limit 2, two live work runs and a live conversation: a third work task waits, the conversation does not
	// count against the work limit.
	w.AgentLive, w.AgentCap = 2, 2
	if v := Evaluate(w); v.Reason != ReasonCapacity || v.Wait.Live != 2 {
		t.Fatalf("third work task: %+v", v)
	}
	w.AgentLive = 1
	if v := Evaluate(w); v.Reason != ReasonRunnable {
		t.Fatalf("a conversation counted against the work limit: %+v", v)
	}
	// The conversation cap: busy in other projects.
	s = convTask()
	s.AgentConversationRun, s.AgentConversationLive, s.AgentConversationCap, s.AgentConversationProjects = conv, 2, 2, []string{"Gnome Arena 2", "Tools"}
	v := Evaluate(s)
	if v.Reason != ReasonCapacity || v.Wait.Kind != WaitAgentConversation || v.Clearer.Action != "waiting for a free conversation run: busy in Gnome Arena 2 and Tools" {
		t.Fatalf("conversation cap: %+v", v)
	}
	s.AgentConversationCap = 0
	if v := Evaluate(s); v.Reason != ReasonRunnable {
		t.Fatalf("no cap by default: %+v", v)
	}
	// Shared limit: the conversation waits for the work run, and a work task counts conversations.
	s = convTask()
	s.AgentRun, s.AgentLive, s.AgentCap, s.AgentSharedLimit = work, 1, 1, true
	if v := Evaluate(s); v.Reason != ReasonCapacity || !strings.HasPrefix(v.Clearer.Action, "waiting for dev's work run") {
		t.Fatalf("shared limit: %+v", v)
	}
	w = ready()
	w.AgentConversationRun, w.AgentConversationLive, w.AgentCap, w.AgentSharedLimit = conv, 1, 1, true
	if v := Evaluate(w); v.Reason != ReasonCapacity {
		t.Fatalf("shared limit, work task behind a conversation: %+v", v)
	}
}

// work-and-conversation-runs 3.1: the clearer of a conversation task names, in plain words, what its run does or
// why it waits.
func TestConversationClearer(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := ready()
	s.Now = now
	s.Task.Kind, s.Task.Status = KindConversation, "IN_PROGRESS"
	s.CurrentRun = &Run{ID: "r1", Status: "RUNNING", TaskID: "t"}
	s.Conversation = &ConversationState{Senders: []string{"producer", "gameplay-dev"}, Started: now.Add(-2 * time.Minute).Unix(), BriefTo: 2}
	v := Evaluate(s)
	want := "answering now: messages from producer and gameplay-dev (started 2 min ago); newer messages are next"
	if v.Reason != ReasonLiveRun || v.Clearer.Action != want || v.Detail != want {
		t.Fatalf("%+v", v)
	}
	s.Conversation.BriefTo = 3
	if v := Evaluate(s); strings.Contains(v.Clearer.Action, "next") {
		t.Fatalf("no newer message: %+v", v)
	}
	s.CurrentRun = &Run{ID: "r1", Status: "QUEUED", TaskID: "t"}
	s.Conversation = &ConversationState{Waiting: "waiting for a node: hetzner-hel1 and mac are full"}
	if v := Evaluate(s); v.Clearer.Action != "waiting for a node: hetzner-hel1 and mac are full" {
		t.Fatalf("queued: %+v", v)
	}
	// A work task keeps the existing wording.
	s.Task.Kind = ""
	if v := Evaluate(s); v.Clearer.Action == "waiting for a node: hetzner-hel1 and mac are full" {
		t.Fatalf("work task: %+v", v)
	}
}

// work-and-conversation-runs 1.3: a conversation run fits a free conversation slot of a node whose work slots are
// full; a work run never does.
func TestNodeShortConversationSlots(t *testing.T) {
	n := Node{Slots: 2, Held: 2, ConversationSlots: 1}
	if n.Short(false, nil) == "" {
		t.Fatal("a work run fits a full node")
	}
	if s := n.Short(true, nil); s != "" {
		t.Fatal("a conversation run does not fit the free conversation slot:", s)
	}
	n.ConversationHeld = 1
	if s := n.Short(true, nil); !strings.Contains(s, "conversation slots held") {
		t.Fatal(s)
	}
	n.Held = 1
	if s := n.Short(true, nil); s != "" {
		t.Fatal("a conversation run does not take a free work slot:", s)
	}
}

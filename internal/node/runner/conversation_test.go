package runner

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"

	"appfactory/internal/node"
)

// work-and-conversation-runs 1.3: conversation slots take conversation runs only, a conversation run takes a
// conversation slot first and a work slot when none is free; Free and FreeConversation count them apart.
func TestPoolConversationSlots(t *testing.T) {
	f := newRuns()
	p := &Pool{Slots: 1, ConversationSlots: 1, Sources: []*Source{{Name: "codex", Process: f.process}}}
	startPool(t, p)
	if free, conv := p.Free(), p.FreeConversation(); free["codex"] != 1 || conv["codex"] != 1 {
		t.Fatalf("free %v conversation %v", free, conv)
	}
	offer(t, p, "work-1")
	eventually(t, "work run busy", func() bool { return busy(p) == 1 })
	if p.Offer(Offer{RunID: "work-2", Provider: "codex"}) {
		t.Fatal("a work run took the conversation slot")
	}
	if free, conv := p.Free(), p.FreeConversation(); free["codex"] != 0 || conv["codex"] != 1 {
		t.Fatalf("full work slots: free %v conversation %v", free, conv)
	}
	// The node is full of work and still answers.
	eventually(t, "conversation slot takes conv-1", func() bool { return p.Offer(Offer{RunID: "conv-1", Provider: "codex", Conversation: true}) })
	eventually(t, "conversation run busy", func() bool { return busy(p) == 2 })
	if s := p.Status(); !s[1].Busy || s[1].Run != "conv-1" {
		t.Fatalf("conversation slot %+v", s)
	}
	if p.Offer(Offer{RunID: "conv-2", Provider: "codex", Conversation: true}) {
		t.Fatal("a second conversation run was taken without a free slot")
	}
}

// A conversation run takes a free work slot when the conversation slots are busy; the claim names that slot.
func TestPoolConversationFallsBackToWorkSlot(t *testing.T) {
	f := newRuns()
	slots := make(chan int, 4)
	p := &Pool{Slots: 1, ConversationSlots: 1, Sources: []*Source{{Name: "codex", Process: func(ctx context.Context, run string, running func(string, context.CancelFunc)) bool {
		slots <- node.SlotOf(ctx)
		return f.process(ctx, run, running)
	}}}}
	startPool(t, p)
	eventually(t, "conv-1 taken", func() bool { return p.Offer(Offer{RunID: "conv-1", Provider: "codex", Conversation: true}) })
	if s := <-slots; s != 1 {
		t.Fatalf("first conversation run in slot %d, want the conversation slot 1", s)
	}
	eventually(t, "conv-2 taken", func() bool { return p.Offer(Offer{RunID: "conv-2", Provider: "codex", Conversation: true}) })
	if s := <-slots; s != 0 {
		t.Fatalf("second conversation run in slot %d, want the work slot 0", s)
	}
}

// disk-pause-conversations 1.1: while work claims are paused (Core's pause_work, a critical disk) the pool offers
// no work slot for work, counts every idle slot for conversations, skips a work offer that slipped through and runs
// conversations in work slots too; resumed, work runs again.
func TestPoolWorkPausedTakesConversations(t *testing.T) {
	f := newRuns()
	var paused atomic.Bool
	paused.Store(true)
	p := &Pool{Slots: 2, ConversationSlots: 1, Sources: []*Source{{Name: "codex", Process: f.process}}, WorkClaimable: func() bool { return !paused.Load() }}
	startPool(t, p)
	if free, conv := p.Free(), p.FreeConversation(); len(free) != 0 || conv["codex"] != 3 {
		t.Fatalf("work paused: free %v conversation %v", free, conv)
	}
	offer(t, p, "work-1") // a slot takes the offer and drops it: the run stays queued at Core
	for _, run := range []string{"conv-1", "conv-2", "conv-3"} {
		eventually(t, run+" taken", func() bool { return p.Offer(Offer{RunID: run, Provider: "codex", Conversation: true}) })
	}
	eventually(t, "three conversations run", func() bool { return busy(p) == 3 })
	f.mu.Lock()
	ran := f.running["work-1"]
	f.mu.Unlock()
	if ran || slices.Contains(f.endedRuns(), "work-1") {
		t.Fatal("a work run started while work claims are paused")
	}
	paused.Store(false)
	for range 3 {
		f.release <- struct{}{}
	}
	eventually(t, "slots idle", func() bool { return busy(p) == 0 })
	if free, conv := p.Free(), p.FreeConversation(); free["codex"] != 2 || conv["codex"] != 1 {
		t.Fatalf("resumed: free %v conversation %v", free, conv)
	}
	offer(t, p, "work-2")
	eventually(t, "work-2 runs", func() bool { return busy(p) == 1 })
}

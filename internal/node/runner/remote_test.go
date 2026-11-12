package runner

import (
	"context"
	"testing"

	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
)

// TestPoolResize (node-packaging-updates B): settings from the web resize the pool live. New slots take offers at
// once; removed idle slots stop; removed busy slots finish their run and then stop, taking nothing new; a later
// resize reuses them.
func TestPoolResize(t *testing.T) {
	f := newRuns()
	p := &Pool{Slots: 1, ConversationSlots: 1, Sources: []*Source{{Name: "codex", Process: f.process}}}
	startPool(t, p)
	p.Resize(3, 0)
	eventually(t, "three work slots, no conversation slot", func() bool {
		return len(p.Status()) == 3 && p.Free()["codex"] == 3 && p.FreeConversation()["codex"] == 0
	})
	for _, r := range []string{"r1", "r2", "r3"} {
		offer(t, p, r)
	}
	eventually(t, "three runs", func() bool { return busy(p) == 3 })
	p.Resize(1, 1)
	eventually(t, "the conversation slot is back", func() bool { return p.FreeConversation()["codex"] == 1 })
	if busy(p) != 3 || p.Free()["codex"] != 0 || len(p.Status()) != 4 {
		t.Fatalf("busy %d free %v status %+v", busy(p), p.Free(), p.Status())
	}
	for range 3 {
		f.release <- struct{}{}
	}
	eventually(t, "retired slots stop after their runs", func() bool {
		return busy(p) == 0 && len(p.Status()) == 2 && p.Free()["codex"] == 1
	})
	offer(t, p, "r4")
	eventually(t, "r4 runs", func() bool { return busy(p) == 1 })
	if p.Offer(Offer{RunID: "r5", Provider: "codex"}) {
		t.Fatal("a retired slot took r5")
	}
	p.Resize(2, 1)
	eventually(t, "a retired slot is reused", func() bool { return len(p.Status()) == 3 && p.Free()["codex"] == 1 })
	f.release <- struct{}{}
	eventually(t, "r4 ended", func() bool { return busy(p) == 0 })
}

// TestDriverSetEnabled: a driver turned off claims nothing and leaves the heartbeat's drivers without a probe; turned
// on it is probed at once (only it) and claims again; changed is called for each change.
func TestDriverSetEnabled(t *testing.T) {
	codex, claude := &fake.Driver{}, &fake.Driver{Provider: "claude"}
	h := newDriverHealth([]driver.Driver{codex, claude}, node.Probes{})
	h.turnOff("claude")
	h.check(context.Background(), true)
	changes := 0
	h.changed = func() { changes++ }
	if !h.usable("codex") || h.usable("claude") || len(h.infos()) != 1 {
		t.Fatalf("%+v", h.infos())
	}
	if !h.setEnabled(context.Background(), "claude", true) || !h.usable("claude") || len(h.infos()) != 2 || changes != 1 {
		t.Fatalf("%+v %d", h.infos(), changes)
	}
	if h.setEnabled(context.Background(), "claude", true) || changes != 1 {
		t.Fatal("no change reported as a change")
	}
	if !h.setEnabled(context.Background(), "codex", false) || h.usable("codex") || !h.anyUsable() || changes != 2 {
		t.Fatalf("%+v", h.infos())
	}
}

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// diskHealth is a heartbeat health at free GiB with the default watermarks (low 20, critical 5, floor 1).
func diskHealth(free float64) string {
	level := "ok"
	switch {
	case free < 5:
		level = "critical"
	case free < 20:
		level = "low"
	}
	return fmt.Sprintf(`{"disk":%q,"free_disk_bytes":%d,"disk_low_bytes":%d,"disk_critical_bytes":%d,"disk_conversation_floor_bytes":%d}`,
		level, uint64(free*(1<<30)), uint64(20)<<30, uint64(5)<<30, uint64(1)<<30)
}

// TestDiskPauseConversations replays 2026-11-11 (disk-pause-conversations 1.1-1.3, 2.1, 3.2): the only node,
// tvorog-mac, is at 3.6 GiB free (critical 5 GiB) and the owner writes to the lead. Core answers pause_work, not
// pause; the conversation is offered and claimed and its brief says the disk is critical; a work task stays queued
// with node_disk_critical and the fix "free disk on tvorog-mac"; below the conversation floor the conversation
// waits too; an owner pause still stops conversations.
func TestDiskPauseConversations(t *testing.T) {
	s, _ := clockStore(t)
	f := newConvFixture(t, s)
	co := DefaultCompany
	register(t, s, "n1", 1, func(r *NodeRegistration) { r.Name = "tvorog-mac"; r.ConversationSlots = 1 })
	a := beat(t, s, "n1", 1, diskHealth(3.6))
	if !a.PauseWork || a.Pause {
		t.Fatalf("critical disk: pause_work %v pause %v", a.PauseWork, a.Pause)
	}
	crit := nodeEvents(t, s, "node.disk_critical")
	if len(crit) != 1 || !crit[0].Attention || !strings.Contains(crit[0].Summary, "tvorog-mac") || !strings.Contains(crit[0].Summary, "3.6 GiB free (critical 5 GiB)") ||
		!strings.Contains(crit[0].Summary, "free disk on tvorog-mac, it resumes by itself above 5 GiB") {
		t.Fatalf("node.disk_critical %+v", crit)
	}

	// A work task: dispatched, its run is neither offered nor claimed, and it says why.
	f.work("w1", "app")
	if _, err := f.dispatch("w1", "rw1", ""); err != nil {
		t.Fatal(err)
	}
	// The owner writes to the lead: the conversation run is offered and claimed on the disk-paused node.
	f.say("app", "owner", "Stop Android for now, free the disk first.", "m1")
	if _, err := f.dispatch(f.conv, "rc1", ""); err != nil {
		t.Fatal(err)
	}
	offers, err := s.OffersFor(f.ctx, co, "n1", 1, map[string]int{}, map[string]int{"codex": 3}, nil)
	if err != nil || len(offers) != 1 || offers[0].RunID != "rc1" {
		t.Fatalf("offers on a disk-paused node %+v %v", offers, err)
	}
	if offers, _ = s.OffersFor(f.ctx, co, "n1", 1, map[string]int{"codex": 2}, nil, nil); slices.ContainsFunc(offers, func(o Offer) bool { return o.RunID == "rw1" }) {
		t.Fatalf("work offered to a disk-paused node %+v", offers)
	}
	refused(t, func() error { _, err := f.claim("rw1", "n1", 0); return err }(), true, "disk_critical")
	q, err := s.QueuedWhy(f.ctx, co, "rw1")
	if err != nil || q == nil || q.Reason != QueuedNodeDiskCritical || q.Detail != "tvorog-mac (n1): disk critical, 3.6 GiB free (critical 5 GiB)" {
		t.Fatalf("queued why %+v %v", q, err)
	}
	if q, _ = s.QueuedWhy(f.ctx, co, "rc1"); q == nil || q.Reason != QueuedUnclaimed {
		t.Fatalf("conversation queued why %+v", q)
	}
	verdict := func(id string) tasks.Verdict {
		t.Helper()
		snap, err := s.TaskSnapshot(f.ctx, co, id)
		if err != nil {
			t.Fatal(err)
		}
		snap.AgentConfigured, snap.Needs = true, tasks.NodeNeeds{Provider: "codex"}
		return tasks.Evaluate(snap)
	}
	if v := verdict("w1"); v.Reason != tasks.ReasonNodeDiskCritical || !strings.Contains(v.Detail, "tvorog-mac: disk critical, 3.6 GiB free (critical 5 GiB)") ||
		v.Clearer.Action != "free disk on tvorog-mac; it resumes by itself above 5 GiB" {
		t.Fatalf("work verdict %+v", v)
	}
	c := f.claimStart("rc1", "n1", 2)
	if !strings.HasPrefix(c.RunSpec.Prompt, "Node status: the disk of node tvorog-mac is critical (3.6 GiB free, critical watermark 5 GiB).") ||
		!strings.Contains(c.RunSpec.Prompt, "new work waits until disk space is freed on tvorog-mac") {
		t.Fatalf("conversation brief %q", c.RunSpec.Prompt[:min(400, len(c.RunSpec.Prompt))])
	}

	// Below the conversation floor nothing is taken: the next conversation run waits with the disk as its reason,
	// and the chat's waiting line says so.
	beat(t, s, "n1", 1, diskHealth(0.5))
	if err := s.Create(f.ctx, co, protocol.RunSpec{ID: "rc2", Project: "tools", Agent: "net", Provider: protocol.RuntimeCodex, Workspace: "/tmp/convruns-tools", Conversation: true}); err != nil {
		t.Fatal(err)
	}
	refused(t, func() error { _, err := f.claim("rc2", "n1", 3); return err }(), true, "disk_critical")
	if q, _ = s.QueuedWhy(f.ctx, co, "rc2"); q == nil || q.Reason != QueuedNodeDiskCritical || !strings.Contains(q.Detail, "below the conversation floor 1 GiB") {
		t.Fatalf("conversation below the floor %+v", q)
	}
	if w := conversationWaiting(q, "net"); !strings.Contains(w, "free disk on tvorog-mac") {
		t.Fatalf("waiting line %q", w)
	}

	// An owner pause of the node stops conversations too, and the heartbeat says pause.
	beat(t, s, "n1", 1, diskHealth(3.6))
	if err := s.SetPause(f.ctx, co, Pause{Scope: "node", Target: "n1", Reason: "moving the Mac", Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	if a = beat(t, s, "n1", 1, diskHealth(3.6)); !a.Pause || !a.PauseWork {
		t.Fatalf("owner pause on a critical disk: %+v", a)
	}
	refused(t, func() error { _, err := f.claim("rc2", "n1", 3); return err }(), true, "paused")
	if offers, _ = s.OffersFor(f.ctx, co, "n1", 1, nil, map[string]int{"codex": 3}, nil); len(offers) != 0 {
		t.Fatalf("offers to an owner-paused node %+v", offers)
	}
	if err := s.ClearPause(f.ctx, co, "node", "n1", "owner"); err != nil {
		t.Fatal(err)
	}

	// Space freed: the node resumes by itself and the work run is claimed.
	if a = beat(t, s, "n1", 1, diskHealth(12)); a.PauseWork || a.Pause {
		t.Fatalf("space freed: %+v", a)
	}
	if _, err := f.claim("rw1", "n1", 0); err != nil {
		t.Fatal("work not claimed after space was freed:", err)
	}
}

// TestDiskPauseVerdictOwnerPause (disk-pause-conversations 2.1): an owner pause of the only capable node gives
// "resume node" with the owner's reason; an offline node keeps "start or fix a node".
func TestDiskPauseVerdictOwnerPause(t *testing.T) {
	s, _ := clockStore(t)
	f := newConvFixture(t, s)
	co := DefaultCompany
	register(t, s, "n1", 1, func(r *NodeRegistration) { r.Name = "tvorog-mac" })
	f.work("w1", "app")
	if err := s.SetPause(f.ctx, co, Pause{Scope: "node", Target: "n1", Reason: "moving the Mac", Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.TaskSnapshot(f.ctx, co, "w1")
	if err != nil {
		t.Fatal(err)
	}
	snap.AgentConfigured, snap.Needs = true, tasks.NodeNeeds{Provider: "codex"}
	if v := tasks.Evaluate(snap); v.Reason != tasks.ReasonNoCapableNode || v.Clearer.Action != "resume node tvorog-mac (paused by the owner: moving the Mac)" {
		t.Fatalf("owner pause verdict %+v", v)
	}
}

// TestDiskPauseSessionAffinity: a disk-paused holder is inactive for work runs (their sessions time out, b7fe936)
// but active for conversations, which it still takes: a conversation continuing its session waits for that node
// (at most the conversation wait) and is claimed there; below the conversation floor it is inactive for them too.
func TestDiskPauseSessionAffinity(t *testing.T) {
	s, _ := clockStore(t)
	f := newConvFixture(t, s)
	co := DefaultCompany
	register(t, s, "n1", 1)
	register(t, s, "n2", 1)
	f.say("app", "owner", "hello", "m1")
	if _, err := f.dispatch(f.conv, "rc1", ""); err != nil {
		t.Fatal(err)
	}
	c := f.claimStart("rc1", "n1", 2)
	if err := end(t, s, "rc1", c.Lease, protocol.Event{Status: "ENDED", Outcome: "succeeded", SessionID: "sess-conv"}); err != nil {
		t.Fatal(err)
	}
	f.say("app", "owner", "and now?", "m2")
	second, err := f.dispatch(f.conv, "rc2", "")
	if err != nil || second.SessionID != "sess-conv" {
		t.Fatalf("second conversation run %+v %v", second, err)
	}
	beat(t, s, "n1", 1, diskHealth(3.6))
	refused(t, func() error { _, err := f.claim("rc2", "n2", 2); return err }(), true, "affinity")
	if q, _ := s.QueuedWhy(f.ctx, co, "rc2"); q == nil || q.Reason != QueuedAffinity || q.Inactive != "" {
		t.Fatalf("conversation tied to its disk-paused holder %+v", q)
	}
	// Below the floor the holder takes no conversation: it is inactive for it.
	beat(t, s, "n1", 1, diskHealth(0.5))
	if q, _ := s.QueuedWhy(f.ctx, co, "rc2"); q == nil || q.Reason != QueuedAffinity || q.Inactive != "paused (critical disk)" {
		t.Fatalf("conversation tied to a full holder %+v", q)
	}
	beat(t, s, "n1", 1, diskHealth(3.6))
	if _, err = f.claim("rc2", "n1", 2); err != nil {
		t.Fatal("the disk-paused holder did not take its conversation:", err)
	}
}

// TestDiskPauseOlderNode: a node that reports no conversation floor (older than disk-pause-conversations) stops
// every claim at critical itself, so Core treats it as paused for conversations too.
func TestDiskPauseOlderNode(t *testing.T) {
	s, _ := clockStore(t)
	register(t, s, "n1", 1)
	queue(t, s, "conv", "p1", "dev", "")
	if _, err := s.DB.Exec("UPDATE runs SET spec=jsonb_set(spec::jsonb,'{conversation}','true') WHERE id='conv'"); err != nil {
		t.Fatal(err)
	}
	health := json.RawMessage(`{"disk":"critical","free_disk_bytes":3865470566,"disk_critical_bytes":5368709120}`)
	if _, err := s.NodeHeartbeat(context.Background(), DefaultCompany, "n1", NodeHeartbeat{Epoch: 1, Health: health}); err != nil {
		t.Fatal(err)
	}
	refused(t, func() error { _, err := claimV2(s, "conv", "n1", 1, "c1"); return err }(), true, "disk_critical")
}

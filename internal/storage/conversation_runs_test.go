package storage

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// convFixture is a project app with agent net's conversation task, nodes registered by the test.
type convFixture struct {
	t    *testing.T
	s    *Store
	ctx  context.Context
	conv string
}

func newConvFixture(t *testing.T, s *Store) *convFixture {
	t.Helper()
	ctx := context.Background()
	for _, p := range []string{"app", "tools"} {
		if _, err := s.CreateProject(ctx, DefaultCompany, tasks.ProjectCreate{ID: p, Name: strings.ToUpper(p[:1]) + p[1:], Workspace: "/tmp/convruns-" + p}); err != nil {
			t.Fatal(err)
		}
	}
	return &convFixture{t: t, s: s, ctx: ctx, conv: ConversationID("app", "net")}
}

// say sends a notification from sender to net in project.
func (f *convFixture) say(project, sender, body, key string) {
	f.t.Helper()
	if _, err := f.s.SendMessage(f.ctx, DefaultCompany, messaging.Send{ProjectID: project, Sender: sender, Recipient: "net", MessageType: "notification", Body: body, RequestID: key}); err != nil {
		f.t.Fatal(err)
	}
}

// dispatch dispatches task id's next run with the agent config snapshot; ErrConflict when a cap holds it.
func (f *convFixture) dispatch(id, run, snapshot string) (protocol.RunSpec, error) {
	f.t.Helper()
	v, err := f.s.Task(f.ctx, DefaultCompany, id)
	if err != nil {
		f.t.Fatal(err)
	}
	return f.s.Dispatch(f.ctx, DefaultCompany, id, v.Version, protocol.RunSpec{ID: run, Project: v.ProjectID, Agent: "net", Provider: protocol.RuntimeCodex, ConfigSnapshot: json.RawMessage(snapshot)})
}

func (f *convFixture) work(id, project string) {
	f.t.Helper()
	if _, err := f.s.CreateTask(f.ctx, DefaultCompany, tasks.Create{ID: id, ProjectID: project, Title: id, Assignee: "net", Ready: true, RequestID: "create-" + id}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *convFixture) claim(run, node string, slot int) (Claimed, error) {
	return f.s.ClaimV2(f.ctx, DefaultCompany, run, ClaimRequest{NodeID: node, Epoch: 1, ClaimID: "c-" + run + "-" + node, Slot: slot})
}

func (f *convFixture) claimStart(run, node string, slot int) Claimed {
	f.t.Helper()
	c, err := f.claim(run, node, slot)
	if err != nil {
		f.t.Fatalf("claim %s on %s/%d: %v", run, node, slot, err)
	}
	if _, err = f.s.StartRun(f.ctx, DefaultCompany, run, c.Lease); err != nil {
		f.t.Fatal(err)
	}
	return c
}

// TestConversationNotBehindWork (work-and-conversation-runs 1.2): the work limit counts work runs only; a
// conversation is dispatched and claimed while the agent's work runs fill it, and does not count against it.
func TestConversationNotBehindWork(t *testing.T) {
	s, _ := clockStore(t)
	f := newConvFixture(t, s)
	register(t, s, "n1", 1, func(r *NodeRegistration) { r.Slots = 8 })
	limit1 := `{"spec":{"execution":{"max_concurrent_runs":1}}}`
	f.work("w1", "app")
	f.work("w2", "tools") // its own project workspace
	if _, err := f.dispatch("w1", "rw1", limit1); err != nil {
		t.Fatal(err)
	}
	f.claimStart("rw1", "n1", 0)
	if _, err := f.dispatch("w2", "rw2", limit1); !errors.Is(err, ErrConflict) {
		t.Fatal("a second work run passed the work limit:", err)
	}
	f.say("app", "producer", "Which port does the server use?", "q1")
	spec, err := f.dispatch(f.conv, "rc1", limit1)
	if err != nil {
		t.Fatal("the conversation waits behind work:", err)
	}
	if !spec.Conversation {
		t.Fatal("conversation run not flagged")
	}
	f.claimStart("rc1", "n1", 1)
	// Work limit 2: two work runs and a conversation run; a third work run waits for the work limit.
	limit2 := `{"spec":{"execution":{"max_concurrent_runs":2}}}`
	if _, err = f.dispatch("w2", "rw2", limit2); err != nil {
		t.Fatal("the conversation counted against the work limit:", err)
	}
	f.claimStart("rw2", "n1", 2)
	f.work("w3", "app")
	if _, err = f.dispatch("w3", "rw3", limit2); !errors.Is(err, ErrConflict) {
		t.Fatal("a third work run passed the work limit:", err)
	}
	snap, err := s.TaskSnapshot(f.ctx, DefaultCompany, "w3")
	if err != nil || snap.AgentLive != 2 || snap.AgentConversationLive != 1 || len(snap.AgentConversationProjects) != 1 || snap.AgentConversationProjects[0] != "App" {
		t.Fatalf("snapshot %+v %v", snap, err)
	}
	// The claim counts by kind too: a queued work run beyond the limit is refused, a conversation is not.
	queueSpec := func(id string, conversation bool) {
		t.Helper()
		if err := s.Create(f.ctx, DefaultCompany, protocol.RunSpec{ID: id, Project: "tools", Agent: "net", Provider: protocol.RuntimeCodex, Workspace: "/tmp/convruns-" + id, Conversation: conversation, ConfigSnapshot: json.RawMessage(limit2)}); err != nil {
			t.Fatal(err)
		}
	}
	queueSpec("rx-work", false)
	queueSpec("rx-conv", true)
	refused(t, func() error { _, err := f.claim("rx-work", "n1", 3); return err }(), true, "agent_busy")
	if q, _ := s.QueuedWhy(f.ctx, DefaultCompany, "rx-work"); q == nil || q.Reason != QueuedAgentBusy || !strings.Contains(q.Detail, "2 of 2 work runs") {
		t.Fatalf("queued why %+v", q)
	}
	f.claimStart("rx-conv", "n1", 3)
}

// TestConversationLimits (work-and-conversation-runs 1.1, 1.2): execution.conversation.max_concurrent_runs caps
// conversation runs across projects; share_work_limit counts them against the work limit as before.
func TestConversationLimits(t *testing.T) {
	s, _ := clockStore(t)
	f := newConvFixture(t, s)
	register(t, s, "n1", 1, func(r *NodeRegistration) { r.Slots = 8 })
	capped := `{"spec":{"execution":{"max_concurrent_runs":2,"conversation":{"max_concurrent_runs":1}}}}`
	f.say("tools", "producer", "Status of the tools?", "q1")
	if _, err := f.dispatch(ConversationID("tools", "net"), "rc-tools", capped); err != nil {
		t.Fatal(err)
	}
	f.claimStart("rc-tools", "n1", 0)
	f.say("app", "producer", "Status of the app?", "q2")
	if _, err := f.dispatch(f.conv, "rc-app", capped); !errors.Is(err, ErrConflict) {
		t.Fatal("the conversation cap did not hold:", err)
	}
	snap, err := s.TaskSnapshot(f.ctx, DefaultCompany, f.conv)
	if err != nil || snap.AgentConversationLive != 1 || snap.AgentConversationProjects[0] != "Tools" {
		t.Fatalf("snapshot %+v %v", snap, err)
	}
	snap.AgentConversationCap, snap.AgentConfigured = 1, true
	if v := tasks.Evaluate(snap); v.Clearer.Action != "waiting for a free conversation run: busy in Tools" {
		t.Fatalf("verdict %+v", v)
	}
	// A work run still fits the work limit.
	f.work("w1", "app")
	if _, err = f.dispatch("w1", "rw1", capped); err != nil {
		t.Fatal(err)
	}
	f.claimStart("rw1", "n1", 1)
	// Shared: the conversation waits for the agent's work run.
	shared := `{"spec":{"execution":{"max_concurrent_runs":2,"conversation":{"share_work_limit":true}}}}`
	if _, err = f.dispatch(f.conv, "rc-app", shared); !errors.Is(err, ErrConflict) {
		t.Fatal("a shared limit let the conversation pass:", err)
	}
	if caps := AgentRunCaps([]byte(shared)); !caps.Shared || caps.Work != 2 || caps.Conversation != 0 {
		t.Fatalf("caps %+v", caps)
	}
}

// TestConversationSlots (work-and-conversation-runs 1.3): a conversation run claims a conversation slot of a node
// whose work slots are full; work runs never take one; a node without conversation slots (older than the change)
// keeps today's single pool; offers fill conversation slots with conversation runs only.
func TestConversationSlots(t *testing.T) {
	s, _ := clockStore(t)
	f := newConvFixture(t, s)
	register(t, s, "n1", 1, func(r *NodeRegistration) { r.Slots, r.ConversationSlots = 1, 1 })
	register(t, s, "old", 1, func(r *NodeRegistration) { r.Slots = 1 })
	queueSpec := func(id string, conversation bool) {
		t.Helper()
		if err := s.Create(f.ctx, DefaultCompany, protocol.RunSpec{ID: id, Project: "app", Agent: "a-" + id, Provider: protocol.RuntimeCodex, Workspace: "/tmp/convruns-" + id, Conversation: conversation}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"w1", "w2", "w3", "w4"} {
		queueSpec(id, false)
	}
	for _, id := range []string{"c1", "c2", "c3"} {
		queueSpec(id, true)
	}
	f.claimStart("w1", "n1", 0)
	f.claimStart("w3", "old", 0)
	refused(t, func() error { _, err := f.claim("w2", "n1", 0); return err }(), true, "capacity")
	refused(t, func() error { _, err := f.claim("w2", "n1", 1); return err }(), true, "capacity") // a work run in the conversation slot
	if q, _ := s.QueuedWhy(f.ctx, DefaultCompany, "c1"); q == nil || q.Reason != QueuedUnclaimed || !strings.Contains(q.Detail, "mini-n1") {
		t.Fatalf("conversation run on a full node: %+v", q)
	}
	offers, err := s.OffersFor(f.ctx, DefaultCompany, "n1", 1, map[string]int{}, map[string]int{"codex": 1}, nil)
	if err != nil || len(offers) != 1 || offers[0].RunID != "c1" || !offers[0].Conversation {
		t.Fatalf("offers for a free conversation slot %+v %v", offers, err)
	}
	f.claimStart("c1", "n1", 1)
	refused(t, func() error { _, err := f.claim("c2", "n1", 1); return err }(), true, "capacity")
	// The old node: its one slot is held, conversation slot indexes do not exist there.
	refused(t, func() error { _, err := f.claim("c2", "old", 1); return err }(), true, "capacity")
	if q, _ := s.QueuedWhy(f.ctx, DefaultCompany, "c2"); q == nil || q.Reason != QueuedNoSlot {
		t.Fatalf("every slot held: %+v", q)
	}
	nodes, err := s.Nodes(f.ctx, DefaultCompany)
	if err != nil || len(nodes) != 2 || nodes[0].ConversationSlots != 1 || nodes[1].ConversationSlots != 0 {
		t.Fatalf("nodes %+v %v", nodes, err)
	}
	if _, err = s.RegisterNode(f.ctx, DefaultCompany, NodeRegistration{NodeID: "bad", Name: "bad", Epoch: 1, ProtocolVersion: 2, Slots: 1, ConversationSlots: -1}); !errors.Is(err, ErrInput) {
		t.Fatal("negative conversation slots accepted:", err)
	}
}

// TestConversationAffinityWait (work-and-conversation-runs 1.4): a queued conversation run waits for its session's
// node at most the company's conversation wait, also while that node is active; then another node takes it with a
// brief that carries the recent history. The wait is a company setting.
func TestConversationAffinityWait(t *testing.T) {
	s, fake := clockStore(t)
	f := newConvFixture(t, s)
	register(t, s, "n1", 1)
	register(t, s, "n2", 1)
	snapshot := `{"spec":{"execution":{"max_concurrent_runs":1}}}`
	f.say("app", "producer", "Which port does the server use?", "q1")
	if _, err := f.dispatch(f.conv, "rc1", snapshot); err != nil {
		t.Fatal(err)
	}
	c := f.claimStart("rc1", "n1", 0)
	if err := end(t, s, "rc1", c.Lease, protocol.Event{Status: "ENDED", Outcome: "succeeded", SessionID: "sess-net", Result: "Answered the producer: port 7777"}); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Hour)
	if v, _ := s.Task(f.ctx, DefaultCompany, f.conv); v.Status != "READY" {
		t.Fatalf("conversation task %s after its run", v.Status)
	}
	f.say("app", "owner", "Please also open the UDP port", "q2")
	second, err := f.dispatch(f.conv, "rc2", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionID != "sess-net" {
		t.Fatalf("second conversation run does not continue the session: %q", second.SessionID)
	}
	// n1 is active: the run waits for it, with the time another node starts it fresh.
	refused(t, func() error { _, err := f.claim("rc2", "n2", 0); return err }(), true, "affinity")
	q, err := s.QueuedWhy(f.ctx, DefaultCompany, "rc2")
	if err != nil || q == nil || q.Reason != QueuedAffinity || q.Until != s.Now().Unix()+int64(DefaultConversationWait/time.Second) || q.Holder != "mini-n1 (n1)" {
		t.Fatalf("queued why %+v %v", q, err)
	}
	if w := conversationWaiting(q, "net"); !strings.HasPrefix(w, "waiting for a node: its session is on mini-n1 (n1), which has no free slot; another node starts it fresh at ") {
		t.Fatal(w)
	}
	snap, err := s.TaskSnapshot(f.ctx, DefaultCompany, f.conv)
	if err != nil || snap.Conversation == nil || !strings.Contains(snap.Conversation.Waiting, "mini-n1") {
		t.Fatalf("conversation state %+v %v", snap.Conversation, err)
	}
	// A paused holder reads as such.
	if _, err = s.DB.ExecContext(f.ctx, "INSERT INTO pauses(company_id,scope,target,reason,actor,created_at,until) VALUES(?,?,?,?,?,?,0)", DefaultCompany, "node", "n1", "maintenance", "owner", s.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if q, _ = s.QueuedWhy(f.ctx, DefaultCompany, "rc2"); q == nil || q.Inactive != "paused" || q.Until != s.Now().Unix()+int64(DefaultConversationWait/time.Second) {
		t.Fatalf("paused holder %+v", q)
	}
	if w := conversationWaiting(q, "net"); !strings.Contains(w, "its session is on the paused node mini-n1 (n1)") {
		t.Fatal(w)
	}
	fake.Advance(DefaultConversationWait)
	claimed, err := f.claim("rc2", "n2", 0)
	if err != nil {
		t.Fatal("the conversation still waits after the conversation wait:", err)
	}
	p := claimed.RunSpec.Prompt
	if !strings.HasPrefix(p, "Your conversation continues in a new session") || !strings.Contains(p, "Which port does the server use?") || !strings.Contains(p, "Answered the producer: port 7777") || !strings.Contains(p, "open the UDP port") {
		t.Fatalf("brief without the recent history:\n%s", p)
	}
	if strings.Index(p, "port 7777") > strings.Index(p, "Message ") {
		t.Fatalf("history after the current input:\n%s", p)
	}
	// The setting.
	if err = s.SetConversationWait(f.ctx, DefaultCompany, 30*time.Second); err == nil {
		t.Fatal("a 30s conversation wait accepted")
	}
	if err = s.SetConversationWait(f.ctx, DefaultCompany, 20*time.Minute); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.ConversationWait(f.ctx, DefaultCompany); d != 20*time.Minute {
		t.Fatal(d)
	}
}

// TestConversationAnsweringNow (work-and-conversation-runs 3.1): a started conversation run names the senders it
// handles; a message arriving meanwhile is next.
func TestConversationAnsweringNow(t *testing.T) {
	s, fake := clockStore(t)
	f := newConvFixture(t, s)
	register(t, s, "n1", 1)
	f.say("app", "producer", "Which port?", "q1")
	f.say("app", "gameplay-dev", "Which tick rate?", "q2")
	if _, err := f.dispatch(f.conv, "rc1", `{}`); err != nil {
		t.Fatal(err)
	}
	f.claimStart("rc1", "n1", 0)
	fake.Advance(2 * time.Minute)
	f.say("app", "owner", "And the lobby size?", "q3")
	snap, err := s.TaskSnapshot(f.ctx, DefaultCompany, f.conv)
	if err != nil {
		t.Fatal(err)
	}
	snap.AgentConfigured = true
	v := tasks.Evaluate(snap)
	if v.Clearer.Action != "answering now: messages from producer and gameplay-dev (started 2 min ago); newer messages are next" {
		t.Fatalf("%+v", v)
	}
}

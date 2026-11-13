package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"appfactory/internal/decisions"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// Event waits (decision-waits): a task parks on a decision or a message, without a timer.

func (h *exitHarness) propose(task, topic, value string) decisions.Decision {
	h.t.Helper()
	d, err := h.s.ProposeDecision(context.Background(), storage.DefaultCompany, "notes", decisions.Propose{Topic: topic, Value: value, Rationale: "looks better", TaskID: task, RequestID: "propose-" + topic, Actor: "demo"})
	if err != nil {
		h.t.Fatal(err)
	}
	return d
}

func (h *exitHarness) record(d decisions.Decision, status, why string) {
	h.t.Helper()
	cur, err := h.s.Decision(context.Background(), storage.DefaultCompany, d.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err = h.s.RecordDecision(context.Background(), storage.DefaultCompany, d.ID, decisions.Record{Version: cur.Version, Status: status, Rationale: why, RequestID: "record-" + d.ID + status, Actor: "owner"}); err != nil {
		h.t.Fatal(err)
	}
}

// backlog creates a task that is not released (the decision's origin; it takes no run).
func (h *exitHarness) backlog(id string) {
	h.t.Helper()
	if _, err := h.s.CreateTask(context.Background(), storage.DefaultCompany, tasks.Create{ID: id, ProjectID: "notes", Title: id, Assignee: "demo", RequestID: "create-" + id}); err != nil {
		h.t.Fatal(err)
	}
}

// idle runs the scheduler and the wait timers over a span with no event: the task gets no run.
func (h *exitHarness) idle(task, run string, span time.Duration) {
	h.t.Helper()
	ctx := context.Background()
	for range 4 {
		h.fake.Advance(span / 4)
		if err := h.s.ExpireWaits(ctx, storage.DefaultCompany); err != nil {
			h.t.Fatal(err)
		}
		if err := h.c.Schedule(ctx); err != nil {
			h.t.Fatal(err)
		}
		if got := h.task(task); got.CurrentRunID != run {
			h.t.Fatalf("%s got a run while parked: %s", task, got.CurrentRunID)
		}
	}
}

// TestDecisionWaitReplayVP2 replays VP-2 (decision-waits 3.1): the decision was proposed from another task; VP-2's
// timed wait naming it is refused with the hint, it parks on the decision (by its short id), gets no run for days,
// shows the owner's decision as its blocker (and its dependent shows the same root), the decision lists it, and the
// owner's approval wakes it once with the outcome in its brief.
func TestDecisionWaitReplayVP2(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.backlog("vp1")
	h.create("vp2", "notes", "demo")
	d := h.propose("vp1", "tonemapper", "Filmic instead of AgX")
	if _, err := h.s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ID: "vp4", ProjectID: "notes", Title: "vp4", Assignee: "demo", Ready: true, Dependencies: []string{"vp2"}, RequestID: "create-vp4"}); err != nil {
		t.Fatal(err)
	}
	r := h.start("vp2")
	code, ref := h.wait(r, map[string]any{"reason": "Owner decision " + d.ID[:8] + " is still proposed", "after": "4h", "deadline": h.at(48 * time.Hour)})
	if code != 409 || ref.Reason != "wait_on_decision" || !strings.Contains(ref.Hint, "decision_id "+d.ID) {
		t.Fatal(code, ref)
	}
	if code, ref = h.wait(r, map[string]any{"reason": "needs the tonemapper to merge", "decision_id": d.ID[:8]}); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")

	v := h.why("vp2")
	if v.Reason != tasks.ReasonParked+tasks.ParkDecision || v.Clearer.Who != "owner" || v.Awaits == nil || v.Awaits.DecisionID != d.ID || v.Awaits.MessageID != d.MessageID || v.Awaits.Topic != "tonemapper" {
		t.Fatalf("%+v %+v", v, v.Awaits)
	}
	if dep := h.why("vp4"); dep.Reason != tasks.ReasonDependencies || dep.Awaits == nil || dep.Awaits.TaskID != "vp2" || dep.Awaits.DecisionID != d.ID || dep.Clearer.Who != "task:vp2" || !strings.Contains(dep.Detail, "waits for decision") {
		t.Fatalf("dependent: %+v %+v", dep, dep.Awaits)
	}
	got, err := h.s.Decision(ctx, storage.DefaultCompany, d.ID)
	if err != nil || len(got.Waiting) != 1 || got.Waiting[0].TaskID != "vp2" || got.Waiting[0].Title != "vp2" {
		t.Fatalf("%+v %v", got.Waiting, err)
	}
	// The task view the board reads carries the wait.
	w := h.do("owner-secret", "GET", "/tasks/vp2", nil, nil)
	if !strings.Contains(w.Body.String(), `"awaits":{"kind":"decision","decision_id":"`+d.ID) {
		t.Fatal(w.Body.String())
	}

	h.idle("vp2", r.id, 48*time.Hour) // no runs while the owner has not decided
	if n := count(h.triggers("vp2"), "wait_ended"); n != 0 {
		t.Fatal("a timer woke it", h.triggers("vp2"))
	}

	h.record(d, "approved", "go with Filmic")
	if got := h.triggers("vp2"); count(got, "decision_recorded") != 1 {
		t.Fatal(got)
	}
	if v = h.why("vp2"); v.Reason != tasks.ReasonRunnable {
		t.Fatalf("%+v", v)
	}
	if got, _ = h.s.Decision(ctx, storage.DefaultCompany, d.ID); len(got.Waiting) != 0 {
		t.Fatalf("still listed: %+v", got.Waiting)
	}
	next := h.start("vp2")
	run, _ := h.s.Get(ctx, storage.DefaultCompany, next.id)
	if !strings.Contains(run.Spec.Prompt, "decision_recorded") || !strings.Contains(run.Spec.Prompt, "approved: Filmic instead of AgX") {
		t.Fatal(run.Spec.Prompt)
	}
	// Exactly one run for the decision.
	if code, ref = h.exit(next, "done", map[string]any{"summary": "merged"}, nil); code != 200 {
		t.Fatal(code, ref)
	}
}

// TestDecisionWaitRejected: a rejection wakes the task with the reason, so it reverts or replans (3.1).
func TestDecisionWaitRejected(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.backlog("vp1")
	h.create("vp2", "notes", "demo")
	d := h.propose("vp1", "tonemapper", "Filmic")
	r := h.start("vp2")
	if code, ref := h.wait(r, map[string]any{"reason": "needs it", "decision_id": d.ID}); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")
	h.record(d, "under_discussion", "let us talk")
	if v := h.why("vp2"); v.Reason != tasks.ReasonParked+tasks.ParkDecision {
		t.Fatalf("under discussion woke it: %+v", v)
	}
	h.record(d, "rejected", "keep AgX")
	next := h.start("vp2")
	run, _ := h.s.Get(ctx, storage.DefaultCompany, next.id)
	if !strings.Contains(run.Spec.Prompt, "rejected. Reason: keep AgX") {
		t.Fatal(run.Spec.Prompt)
	}
}

// TestEventWaitRefusals: what an event wait refuses, with a hint, changing nothing (1.1, 1.2).
func TestEventWaitRefusals(t *testing.T) {
	h := newExitHarness(t)
	h.backlog("vp1")
	h.create("w", "notes", "demo")
	open := h.propose("vp1", "palette", "warm")
	done := h.propose("vp1", "font", "Inter")
	h.record(done, "approved", "fine")
	r := h.start("w")
	for name, c := range map[string]struct {
		args   map[string]any
		reason string
		hint   string
	}{
		"unknown decision": {map[string]any{"reason": "x", "decision_id": "0123456789abcdef"}, "unknown_decision", "factory_decision_list"},
		"decided":          {map[string]any{"reason": "x", "decision_id": done.ID}, "decision_recorded", "approved: Inter"},
		"two things":       {map[string]any{"reason": "x", "decision_id": open.ID, "from": "manager"}, "invalid_exit", "one thing"},
		"time and event":   {map[string]any{"reason": "x", "from": "manager", "after": "1h", "deadline": h.at(2 * time.Hour)}, "invalid_exit", "one thing"},
		"from self":        {map[string]any{"reason": "x", "from": "demo"}, "invalid_exit", "other than you"},
		"from stranger":    {map[string]any{"reason": "x", "from": "nobody"}, "invalid_exit", "configured agent"},
		"past deadline":    {map[string]any{"reason": "x", "decision_id": open.ID, "deadline": h.at(-time.Hour)}, "invalid_exit", "future"},
		"far deadline":     {map[string]any{"reason": "x", "from": "owner", "deadline": h.at(15 * 24 * time.Hour)}, "wait_too_long", "14 days"},
	} {
		if code, ref := h.wait(r, c.args); code != 409 || ref.Reason != c.reason || !strings.Contains(ref.Hint, c.hint) {
			t.Errorf("%s: %d %+v", name, code, ref)
		}
	}
	if got := h.task("w"); got.Status != "IN_PROGRESS" {
		t.Fatalf("refusals changed the task: %+v", got)
	}
	// A timed wait whose reason names no open decision is a plain timed wait.
	if code, ref := h.wait(r, map[string]any{"reason": "decision " + done.ID[:8] + " is recorded; waiting for the build", "after": "30m", "deadline": h.at(time.Hour)}); code != 200 {
		t.Fatal(code, ref)
	}
}

// TestEventWaitDeadlineEscalates: the deadline of an event wait escalates once to the decision's addressee and
// never wakes the task; continue runs it now without the decision (1.1).
func TestEventWaitDeadlineEscalates(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.backlog("vp1")
	h.create("vp2", "notes", "demo")
	d := h.propose("vp1", "tonemapper", "Filmic")
	r := h.start("vp2")
	if code, ref := h.wait(r, map[string]any{"reason": "needs it", "decision_id": d.ID, "deadline": h.at(time.Hour)}); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")
	h.fake.Advance(2 * time.Hour)
	for range 3 {
		if err := h.s.ExpireWaits(ctx, storage.DefaultCompany); err != nil {
			t.Fatal(err)
		}
	}
	es, err := h.s.Escalations(ctx, storage.DefaultCompany, true)
	if err != nil {
		t.Fatal(err)
	}
	var esc *storage.Escalation
	for i := range es {
		if es[i].Reason == storage.EventDeadlineReason {
			if esc != nil {
				t.Fatal("escalated twice", es)
			}
			esc = &es[i]
		}
	}
	if esc == nil || esc.Target != "owner" || esc.RefID != d.ID {
		t.Fatalf("%+v", es)
	}
	if len(h.triggers("vp2")) != 0 || h.parkStatus("vp2", tasks.ParkDecision) != "active" {
		t.Fatal("the deadline woke it", h.triggers("vp2"))
	}
	m, err := h.s.Message(ctx, storage.DefaultCompany, esc.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.s.ReplyMessage(ctx, storage.DefaultCompany, m.ID, messaging.Reply{Version: m.Version, Sender: "owner", Body: "continue", Answers: map[string]any{storage.EscalationAnswerID: "continue"}, RequestID: "answer-esc"}); err != nil {
		t.Fatal(err)
	}
	if v := h.why("vp2"); v.Reason != tasks.ReasonRunnable || h.parkStatus("vp2", tasks.ParkDecision) != "overridden" {
		t.Fatalf("%+v %s", v, h.parkStatus("vp2", tasks.ParkDecision))
	}
}

// TestMessageWaitYourTurn: tasks parked on a message from the manager get no run until it writes; "your turn" about
// one task wakes only that task, with the message in its brief (1.2).
func TestMessageWaitYourTurn(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	runs := map[string]string{}
	for _, id := range []string{"t1", "t2", "t3"} {
		h.create(id, "notes", "demo")
		r := h.start(id)
		// The manager said it releases the tasks in turn: a wait on it without a request of ours is expected.
		if code, ref := h.wait(r, map[string]any{"reason": "the producer releases us in turn", "from": "manager", "topic": "your turn", "expects_unprompted": true}); code != 200 {
			t.Fatal(code, ref)
		}
		h.end(r, "succeeded", "accepted")
		runs[id] = r.id
	}
	v := h.why("t2")
	if v.Reason != tasks.ReasonParked+tasks.ParkMessage || v.Clearer.Who != "agent:manager" || v.Awaits == nil || v.Awaits.From != "manager" || v.Awaits.Topic != "your turn" {
		t.Fatalf("%+v %+v", v, v.Awaits)
	}
	for id, run := range runs {
		h.idle(id, run, 8*time.Hour)
	}
	// Someone else's message does not wake it.
	if _, err := h.s.SendMessage(ctx, storage.DefaultCompany, messaging.Send{ProjectID: "notes", TaskID: "t2", Sender: "reviewer", Recipient: "demo", MessageType: "notification", Body: "fyi", RequestID: "fyi"}); err != nil {
		t.Fatal(err)
	}
	if h.parkStatus("t2", tasks.ParkMessage) != "active" {
		t.Fatal("woken by another sender")
	}
	m, err := h.s.SendMessage(ctx, storage.DefaultCompany, messaging.Send{ProjectID: "notes", TaskID: "t2", Sender: "manager", Recipient: "demo", MessageType: "notification", Body: "your turn: take the HUD pass", RequestID: "turn-t2"})
	if err != nil {
		t.Fatal(err)
	}
	if h.parkStatus("t2", tasks.ParkMessage) != "cleared" || h.parkStatus("t1", tasks.ParkMessage) != "active" || h.parkStatus("t3", tasks.ParkMessage) != "active" {
		t.Fatal(h.parkStatus("t1", tasks.ParkMessage), h.parkStatus("t2", tasks.ParkMessage), h.parkStatus("t3", tasks.ParkMessage))
	}
	if got := h.triggers("t2"); count(got, "message_received") != 1 {
		t.Fatal(got)
	}
	next := h.start("t2")
	run, _ := h.s.Get(ctx, storage.DefaultCompany, next.id)
	if !strings.Contains(run.Spec.Prompt, "your turn: take the HUD pass") || !strings.Contains(run.Spec.Prompt, "Message "+m.ID) {
		t.Fatal(run.Spec.Prompt)
	}
	// A comment from the awaited sender is a message too.
	if _, err = h.s.CommentTask(ctx, storage.DefaultCompany, "t3", tasks.Comment{Actor: "manager", Text: "your turn", RequestID: "c-t3"}); err != nil {
		t.Fatal(err)
	}
	if h.parkStatus("t3", tasks.ParkMessage) != "cleared" || h.parkStatus("t1", tasks.ParkMessage) != "active" {
		t.Fatal("comment did not wake t3 alone")
	}
}

// TestParkOnOwnerDetails: the storage function inbox-badge-parity uses for "I'll send details": the task parks on
// the owner, counts as needing the owner, and the owner's next message to its agent in the project wakes it.
func TestParkOnOwnerDetails(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("q", "notes", "demo")
	h.cancel("q")
	h.create("q2", "notes", "demo")
	if err := h.s.ParkOnMessage(ctx, storage.DefaultCompany, "q", "owner", "details", "owner will send details"); err == nil {
		t.Fatal("parked a closed task")
	}
	if err := h.s.ParkOnMessage(ctx, storage.DefaultCompany, "q2", "owner", "the level list", "owner will send details"); err != nil {
		t.Fatal(err)
	}
	v := h.why("q2")
	if v.Reason != tasks.ReasonParked+tasks.ParkMessage || v.Clearer.Who != "owner" || v.Awaits == nil || v.Awaits.From != "owner" {
		t.Fatalf("%+v", v)
	}
	// In another project the owner's message does not reach it.
	if _, err := h.s.SendMessage(ctx, storage.DefaultCompany, messaging.Send{ProjectID: "code", Sender: "owner", Recipient: "demo", MessageType: "notification", Body: "unrelated", RequestID: "o1"}); err != nil {
		t.Fatal(err)
	}
	if h.parkStatus("q2", tasks.ParkMessage) != "active" {
		t.Fatal("woken from another project")
	}
	if _, err := h.s.SendMessage(ctx, storage.DefaultCompany, messaging.Send{ProjectID: "notes", Sender: "owner", Recipient: "demo", MessageType: "notification", Body: "levels: 1-3 forest, 4 cave", RequestID: "o2"}); err != nil {
		t.Fatal(err)
	}
	if h.parkStatus("q2", tasks.ParkMessage) != "cleared" || count(h.triggers("q2"), "message_received") != 1 {
		t.Fatal(h.parkStatus("q2", tasks.ParkMessage), h.triggers("q2"))
	}
}

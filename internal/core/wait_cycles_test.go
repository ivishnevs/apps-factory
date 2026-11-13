package core

import (
	"context"
	"strings"
	"testing"

	"appfactory/internal/approvals"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// Wait deadlocks (wait-deadlock-detection): agents that wait on each other are refused at the wait exit, messages
// reach the waiting agent, held approvals hear their delegate, and cycles formed later are swept.

func (h *exitHarness) send(task, from, to, body string) messaging.Message {
	h.t.Helper()
	m, err := h.s.SendMessage(context.Background(), storage.DefaultCompany, messaging.Send{ProjectID: "notes", TaskID: task, Sender: from, Recipient: to, MessageType: "notification", Body: body, RequestID: "msg-" + from + to + body})
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

// parkOn parks task (with no run) on the next message from `from`, as a wait exit would, without the exit's gate.
func (h *exitHarness) parkOn(task, from string) {
	h.t.Helper()
	if err := h.s.ParkOnMessage(context.Background(), storage.DefaultCompany, task, from, "", "waits for "+from); err != nil {
		h.t.Fatal(err)
	}
}

// TestReviewRequestFiledUnderSenderTaskWakesWaiter (1.1): a re-review request filed under the sender's own task wakes
// the reviewer's task that waits for the sender; a message filed under the recipient's own task still wakes only it.
func TestReviewRequestFiledUnderSenderTaskWakesWaiter(t *testing.T) {
	h := newExitHarness(t)
	h.create("b", "notes", "reviewer")
	h.create("b2", "notes", "reviewer")
	h.create("f", "notes", "demo")
	h.parkOn("b", "demo")
	h.parkOn("b2", "demo")
	// Filed under b2 (the reviewer's own task): only b2 wakes.
	h.send("b2", "demo", "reviewer", "b2 is ready")
	if h.parkStatus("b2", tasks.ParkMessage) != "cleared" || h.parkStatus("b", tasks.ParkMessage) != "active" {
		t.Fatal(h.parkStatus("b2", tasks.ParkMessage), h.parkStatus("b", tasks.ParkMessage))
	}
	// Filed under f, demo's own task: the reviewer's waiting task wakes with it.
	h.send("f", "demo", "reviewer", "PR #438 head df59fbd3: all four items done, please re-review")
	if h.parkStatus("b", tasks.ParkMessage) != "cleared" || count(h.triggers("b"), "message_received") != 1 {
		t.Fatal(h.parkStatus("b", tasks.ParkMessage), h.triggers("b"))
	}
}

// TestWaitCycleRefused (2.2): waiting on an agent that is asleep waiting on you is refused with what it waits for;
// answering it first wakes it and lets the wait through.
func TestWaitCycleRefused(t *testing.T) {
	h := newExitHarness(t)
	h.create("b", "notes", "reviewer")
	h.create("f", "notes", "demo")
	h.send("b", "reviewer", "demo", "changes requested: button faces")
	h.parkOn("b", "demo")
	r := h.start("f")
	code, ref := h.wait(r, map[string]any{"reason": "re-review", "from": "reviewer"})
	if code != 409 || ref.Reason != "wait_cycle" || !strings.Contains(ref.Hint, "reviewer's task b") || !strings.Contains(ref.Hint, "changes requested: button faces") || !strings.Contains(ref.Hint, "task_id=b") {
		t.Fatal(code, ref)
	}
	if h.task("f").Status != "IN_PROGRESS" {
		t.Fatal("the refusal moved the task")
	}
	h.send("b", "demo", "reviewer", "fixed, please re-review")
	if code, ref = h.wait(r, map[string]any{"reason": "re-review", "from": "reviewer"}); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")
	if h.parkStatus("b", tasks.ParkMessage) != "cleared" {
		t.Fatal("the answer did not wake b")
	}
}

// TestWaitUnasked (2.3): a wait on an agent with nothing pending from the waiter is refused; a request, or the
// override when the agent promised to write, lets it through and the park shows the override.
func TestWaitUnasked(t *testing.T) {
	h := newExitHarness(t)
	h.create("p", "notes", "demo")
	r := h.start("p")
	if code, ref := h.wait(r, map[string]any{"reason": "verdict", "from": "reviewer"}); code != 409 || ref.Reason != "wait_unasked" || !strings.Contains(ref.Hint, "no message") {
		t.Fatal(code, ref)
	}
	h.send("p", "demo", "reviewer", "please review head b8638c47")
	h.send("", "reviewer", "demo", "changes requested on head a1")
	code, ref := h.wait(r, map[string]any{"reason": "verdict", "from": "reviewer"})
	if code != 409 || ref.Reason != "wait_unasked" || !strings.Contains(ref.Hint, "changes requested on head a1") {
		t.Fatal(code, ref)
	}
	if code, ref = h.wait(r, map[string]any{"reason": "verdict", "from": "reviewer", "expects_unprompted": true}); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")
	if v := h.why("p"); !strings.Contains(v.Detail, "expects it unprompted") {
		t.Fatalf("%+v", v)
	}
	// The owner is a person: waits on the owner are not checked.
	h.create("q", "notes", "demo")
	rq := h.start("q")
	if code, ref = h.wait(rq, map[string]any{"reason": "details", "from": "owner"}); code != 200 {
		t.Fatal(code, ref)
	}
}

// TestHeldApprovalHearsDelegate (3.1, 3.2): the delegate waiting on the requester is refused with the hint to decide;
// the delegate's reviewer asking the held task to push wakes it for a turn, the hold stays.
func TestHeldApprovalHearsDelegate(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("d", "notes", "demo")
	r := h.start("d")
	req := h.produce("d", "notes", r, "class-family", "v1", "manager")
	if w := h.exitAt(r, "wait", map[string]any{"reason": "the art", "approval_id": req.ID}, h.ack(r), nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "succeeded", "accepted")

	// The delegate waits on the requester (no conversation of the requester is queued that could answer).
	h.create("m", "notes", "manager")
	rm := h.start("m")
	code, ref := h.wait(rm, map[string]any{"reason": "the push", "from": "demo"})
	if code != 409 || ref.Reason != "wait_cycle" || !strings.Contains(ref.Hint, "Decide approval "+req.ID) || !strings.Contains(ref.Hint, "changes requested") {
		t.Fatal(code, ref)
	}
	h.end(rm, "succeeded", "accepted")
	// A message about another task does not reach d; one filed under d from the delegate's reviewer does.
	h.send("m", "manager", "demo", "about m only")
	if h.parkStatus("d", tasks.ParkApproval) != "active" {
		t.Fatal("woken by a message about another task")
	}
	h.send("d", "manager", "reviewer", "please review the class family of d")
	h.send("d", "qa", "demo", "unrelated")
	if h.parkStatus("d", tasks.ParkApproval) != "active" {
		t.Fatal("woken by an agent outside the delegate's side")
	}
	h.send("d", "reviewer", "demo", "commit bf24a7ea is not on origin: push and send the SHA")
	if h.parkStatus("d", tasks.ParkApproval) != "overridden" || h.lastTrigger("d").kind != "message_received" || !strings.Contains(h.lastTrigger("d").body, "approval_id "+req.ID) {
		t.Fatal(h.parkStatus("d", tasks.ParkApproval), h.lastTrigger("d"))
	}
	next := h.start("d")
	if code, ref = h.exit(next, "done", map[string]any{"summary": "pushed"}, nil); code != 409 || ref.Reason != "awaiting_approval" {
		t.Fatal("the hold was lifted", code, ref)
	}
	if w := h.exitAt(next, "wait", map[string]any{"reason": "pushed, SHA sent", "approval_id": req.ID}, h.ack(next), nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(next, "succeeded", "accepted")
	if _, err := h.s.DecideApproval(ctx, storage.DefaultCompany, req.ID, approvals.Decision{Version: req.Version, Action: "approve", Reason: "fine", DecidedBy: "manager", RequestID: "ok"}); err != nil {
		t.Fatal(err)
	}
	if h.parkStatus("d", tasks.ParkApproval) != "cleared" {
		t.Fatal(h.parkStatus("d", tasks.ParkApproval))
	}
}

// TestWaitCycleSwept (4.1, 4.2): a cycle formed without the gate wakes both tasks once; the same cycle again within a
// day escalates once instead.
func TestWaitCycleSwept(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("b", "notes", "reviewer")
	h.create("f", "notes", "demo")
	h.parkOn("b", "demo")
	h.parkOn("f", "reviewer")
	if err := h.s.SweepWaitCycles(ctx, storage.DefaultCompany); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"b", "f"} {
		if h.parkStatus(id, tasks.ParkMessage) != "overridden" || h.lastTrigger(id).kind != storage.WaitCycleReason {
			t.Fatal(id, h.parkStatus(id, tasks.ParkMessage), h.lastTrigger(id))
		}
	}
	if !strings.Contains(h.lastTrigger("f").body, "reviewer's task b") {
		t.Fatal(h.lastTrigger("f").body)
	}
	// They fall asleep on each other again.
	h.parkOn("b", "demo")
	h.parkOn("f", "reviewer")
	for range 2 {
		if err := h.s.SweepWaitCycles(ctx, storage.DefaultCompany); err != nil {
			t.Fatal(err)
		}
	}
	es, err := h.s.Escalations(ctx, storage.DefaultCompany, true)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range es {
		if e.Reason == storage.WaitCycleReason {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d escalations: %+v", n, es)
	}
	// A working agent breaks the cycle: nothing is swept.
	h.create("c", "notes", "manager")
	h.create("e", "notes", "demo")
	h.parkOn("c", "demo")
	h.parkOn("e", "manager")
	h.create("busy", "notes", "manager")
	rb := h.start("busy")
	if err = h.s.SweepWaitCycles(ctx, storage.DefaultCompany); err != nil {
		t.Fatal(err)
	}
	if h.parkStatus("c", tasks.ParkMessage) != "active" || h.parkStatus("e", tasks.ParkMessage) != "active" {
		t.Fatal("swept while manager works")
	}
	h.end(rb, "succeeded", "accepted")
}

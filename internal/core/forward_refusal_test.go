package core

import (
	"context"
	"slices"
	"strings"
	"testing"

	"appfactory/internal/factorytools"

	"appfactory/internal/messaging"
	"appfactory/internal/storage"
)

// askManager sends demo's question about task to its manager, which reports to the owner, and returns it.
func askManager(t *testing.T, h *exitHarness, task, id string) messaging.Message {
	t.Helper()
	m, err := h.s.SendMessage(context.Background(), storage.DefaultCompany, messaging.Send{ProjectID: "notes", TaskID: task, Sender: "demo", Recipient: "manager", MessageType: "question", Body: "How should I continue " + id + "?", RequestID: id})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

var forwardBrief = map[string]any{"title": "How should the check continue?", "ask": "Pick how the integration check continues",
	"after": "Fix: the developer continues; Window: the emulator runs with a window", "recommendation": "Fix the host tool: one line"}

// TestForwardRefusalHoldsExit replays 2026-11-15 with fixtures (forward-refusal-guard): a lead whose manager is the
// owner forwards a question without a brief and exits in the same turn. The forward is refused with brief_required
// and the exit with unresolved_refusal, so the run goes on; after the forward is fixed (same request_id) the exit is
// accepted. ask stays allowed with an unresolved refusal.
func TestForwardRefusalHoldsExit(t *testing.T) {
	h := newExitHarness(t)
	h.create("check", "notes", "demo")
	q := askManager(t, h, "check", "q1")
	lead := storage.ConversationID("notes", "manager")
	r := h.start(lead)
	ack := h.ack(r)
	fwd := map[string]any{"message_id": q.ID, "version": q.Version, "request_id": "fwd-1", "note": "the owner's machine; fix the tool"}
	if code, _, body := h.callAt(r, "factory_question_forward", fwd, ack); code != 400 || !strings.Contains(body, "brief_required") {
		t.Fatal("forward without a brief", code, body)
	}
	code, ref, body := h.callAt(r, "factory_exit_done", map[string]any{"summary": "forwarded to the board"}, ack)
	if code != 409 || ref.Reason != "unresolved_refusal" || !strings.Contains(ref.Hint, "factory_question_forward") || !strings.Contains(ref.Hint, "brief_required") {
		t.Fatal("exit past the refused forward", code, body)
	}
	fwd["brief"] = forwardBrief
	if code, _, body := h.callAt(r, "factory_question_forward", fwd, ack); code != 200 {
		t.Fatal("fixed forward", code, body)
	}
	if code, _, body := h.callAt(r, "factory_exit_done", map[string]any{"summary": "forwarded to the owner"}, ack); code != 200 {
		t.Fatal("exit after the fix", code, body)
	}
	h.end(r, "succeeded", "accepted")
	if got, _ := h.s.Message(context.Background(), storage.DefaultCompany, q.ID); got.Recipient != "owner" {
		t.Fatal("the question did not reach the owner:", got.Recipient)
	}

	// ask hands the problem up: allowed with an unresolved refusal.
	q2 := askManager(t, h, "check", "q2")
	r = h.start(lead)
	ack = h.ack(r)
	if code, _, _ := h.callAt(r, "factory_question_forward", map[string]any{"message_id": q2.ID, "version": q2.Version, "request_id": "fwd-2", "note": "n"}, ack); code != 400 {
		t.Fatal(code)
	}
	if code, _, body := h.callAt(r, "factory_exit_ask", map[string]any{"question": "The forward keeps being refused; can you take it?", "brief": forwardBrief}, ack); code != 200 {
		t.Fatal("ask with an unresolved refusal", code, body)
	}
}

// TestPendingQuestionWakesRecipient (forward-refusal-guard): a run that ends without answering or forwarding a
// question addressed to it (a refused forward, then a crash) does not strand it: the recipient gets a new trigger
// naming the question and the refusal, a bounded number of times, then the question shows in the feed as stranded.
// A question answered or forwarded gets no extra trigger.
func TestPendingQuestionWakesRecipient(t *testing.T) {
	h := newExitHarness(t)
	h.create("check", "notes", "demo")
	q := askManager(t, h, "check", "q1")
	lead := storage.ConversationID("notes", "manager")
	triggers := func() int {
		t.Helper()
		var n int
		if err := h.s.DB.QueryRow("SELECT count(*) FROM task_triggers WHERE task_id=? AND kind='message' AND ref_id=?", lead, q.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if triggers() != 1 {
		t.Fatal("delivery", triggers())
	}
	for i := 1; i <= storage.MaxQuestionRewakes+1; i++ {
		r := h.start(lead)
		if code, _, _ := h.callAt(r, "factory_question_forward", map[string]any{"message_id": q.ID, "version": q.Version, "request_id": "fwd", "note": "n"}, h.ack(r)); code != 400 {
			t.Fatal(code)
		}
		h.end(r, "succeeded", "none") // the turn ends without fixing it (no exit: its triggers count as handled)
		want := min(i+1, storage.MaxQuestionRewakes+1)
		if got := triggers(); got != want {
			t.Fatalf("after run %d: %d triggers, want %d", i, got, want)
		}
		if i <= storage.MaxQuestionRewakes && !strings.Contains(h.lastTrigger(lead).body, "brief_required") {
			t.Fatalf("the trigger does not name the refusal: %q", h.lastTrigger(lead).body)
		}
		h.fake.Advance(30 * 60 * 1e9) // past the retry backoff
	}
	stranded := 0
	_ = h.s.DB.QueryRow("SELECT count(*) FROM feed_events WHERE kind='message.question_stranded' AND entity_id=? AND attention=1", q.ID).Scan(&stranded)
	if stranded != 1 {
		t.Fatal("stranded items:", stranded)
	}

	// Forwarded: no extra trigger.
	q2 := askManager(t, h, "check", "q2")
	before := 0
	_ = h.s.DB.QueryRow("SELECT count(*) FROM task_triggers WHERE task_id=? AND ref_id=?", lead, q2.ID).Scan(&before)
	r := h.start(lead)
	if code, _, body := h.callAt(r, "factory_question_forward", map[string]any{"message_id": q2.ID, "version": q2.Version, "request_id": "fwd-q2", "note": "n", "brief": forwardBrief}, h.ack(r)); code != 200 {
		t.Fatal(code, body)
	}
	h.end(r, "succeeded", "none")
	after := 0
	_ = h.s.DB.QueryRow("SELECT count(*) FROM task_triggers WHERE task_id=? AND ref_id=?", lead, q2.ID).Scan(&after)
	if after != before {
		t.Fatal("a forwarded question woke its old recipient", before, after)
	}
}

// TestExitAfterDecisionRefusalNotifiesManager (forward-refusal-guard): a refusal Core decided (the question limit)
// does not hold the exit; the exit is marked and the agent's manager is told: an agent manager gets a notification,
// a person manager an attention item and no inbox question.
func TestExitAfterDecisionRefusalNotifiesManager(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	for _, c := range []struct{ task, agent, manager string }{{"dev", "demo", "manager"}, {"lead", "demo", "owner"}} {
		demo := h.c.Agents["demo"]
		demo.Spec.ReportsTo = c.manager
		h.c.Agents["demo"] = demo
		h.create(c.task, "notes", c.agent)
		r := h.start(c.task)
		if err := h.s.RecordToolResult(ctx, storage.DefaultCompany, r.id, "factory_message_ask", "round-9", 409, []byte(`{"reason":"question_limit","hint":"decide within your authority"}`)); err != nil {
			t.Fatal(err)
		}
		if w := h.exitAt(r, "done", map[string]any{"summary": "decided myself"}, h.ack(r), nil); w.Code != 200 {
			t.Fatal(c.task, w.Code, w.Body.String())
		}
		h.end(r, "succeeded", "accepted")
		var attention []bool
		rows, err := h.s.DB.Query("SELECT attention FROM feed_events WHERE kind='task.exit_after_refusal' AND task_id=?", c.task)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var a bool
			_ = rows.Scan(&a)
			attention = append(attention, a)
		}
		rows.Close()
		if len(attention) != 1 || attention[0] != (c.manager == "owner") {
			t.Fatal(c.task, "exit mark", attention)
		}
		var notices int
		_ = h.s.DB.QueryRow("SELECT count(*) FROM messages WHERE task_id=? AND sender='factory' AND message_type='notification' AND recipient=? AND body->>'body' LIKE '%after a call Core refused%'", c.task, c.manager).Scan(&notices)
		if want := map[bool]int{true: 0, false: 1}[c.manager == "owner"]; notices != want {
			t.Fatal(c.task, "notices to", c.manager, notices)
		}
	}
}

// TestBriefRequiredForPersonManagedAgents (forward-refusal-guard): an agent managed by a person (the owner or a member)
// gets brief required on question_forward and exit ask; an agent managed by an agent does not; the shared definitions
// are not changed.
func TestBriefRequiredForPersonManagedAgents(t *testing.T) {
	h := newExitHarness(t)
	all := func(factorytools.Tool) bool { return true }
	required := func(manager, tool string) bool {
		t.Helper()
		a := h.c.Agents["demo"]
		a.Spec.ReportsTo = manager
		for _, x := range h.c.agentTools(storage.DefaultCompany, a, all) {
			if x.Name == tool {
				r, _ := x.InputSchema["required"].([]string)
				return slices.Contains(r, "brief")
			}
		}
		t.Fatal("no tool", tool)
		return false
	}
	for _, c := range []struct {
		manager string
		want    bool
	}{{"owner", true}, {"member:alice", true}, {"manager", false}, {"owner", true}} {
		for _, tool := range []string{"factory_question_forward", "factory_exit_ask"} {
			if got := required(c.manager, tool); got != c.want {
				t.Errorf("manager %s, %s: brief required %v, want %v", c.manager, tool, got, c.want)
			}
		}
	}
}

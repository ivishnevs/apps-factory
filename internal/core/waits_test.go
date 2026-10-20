package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/policies"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// Timer-only waits (waits-events-cost-cli 1.1-1.3).

func (h *exitHarness) wait(r exitRun, args map[string]any) (int, storage.ExitRefusal) {
	h.t.Helper()
	w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_exit_wait", Arguments: args}, nil)
	var ref storage.ExitRefusal
	_ = json.Unmarshal(w.Body.Bytes(), &ref)
	return w.Code, ref
}

func (h *exitHarness) at(d time.Duration) string {
	return h.fake.Now().Add(d).UTC().Format(time.RFC3339)
}

func (h *exitHarness) triggers(task string) []string {
	h.t.Helper()
	snap, err := h.s.TaskSnapshot(context.Background(), storage.DefaultCompany, task)
	if err != nil {
		h.t.Fatal(err)
	}
	out := []string{}
	for _, g := range snap.Pending {
		out = append(out, g.Kind)
	}
	return out
}

func (h *exitHarness) why(task string) tasks.Verdict {
	h.t.Helper()
	snap, err := h.s.TaskSnapshot(context.Background(), storage.DefaultCompany, task)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.c.evaluate(snap)
}

func count(xs []string, x string) int {
	n := 0
	for _, v := range xs {
		if v == x {
			n++
		}
	}
	return n
}

// TestWaitRefusals: every limit of factory_exit_wait refuses with a hint and changes nothing (1.1).
func TestWaitRefusals(t *testing.T) {
	h := newExitHarness(t)
	h.create("w", "notes", "demo")
	r := h.start("w")
	for name, c := range map[string]struct {
		args   map[string]any
		reason string
		hint   string
	}{
		"too short":          {map[string]any{"reason": "build", "after": "5m", "deadline": h.at(time.Hour)}, "wait_too_short", "15m0s"},
		"no deadline":        {map[string]any{"reason": "build", "after": "30m"}, "deadline_required", "deadline"},
		"bad deadline":       {map[string]any{"reason": "build", "after": "30m", "deadline": "tomorrow"}, "deadline_required", "RFC 3339"},
		"deadline too far":   {map[string]any{"reason": "build", "after": "30m", "deadline": h.at(15 * 24 * time.Hour)}, "wait_too_long", "14 days"},
		"deadline too early": {map[string]any{"reason": "build", "wake_at": h.at(time.Hour), "deadline": h.at(30 * time.Minute)}, "invalid_exit", "before the wake time"},
		"no wake":            {map[string]any{"reason": "build", "deadline": h.at(time.Hour)}, "invalid_exit", "wake_at"},
		"bad after":          {map[string]any{"reason": "build", "after": "soon", "deadline": h.at(time.Hour)}, "invalid_exit", "duration"},
	} {
		w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_exit_wait", Arguments: c.args}, nil)
		var ref storage.ExitRefusal
		_ = json.Unmarshal(w.Body.Bytes(), &ref)
		// A missing deadline is already refused by the tool schema (400).
		schema := w.Code == 400 && name == "no deadline" && strings.Contains(w.Body.String(), "deadline")
		if !schema && (w.Code != 409 || ref.Reason != c.reason || !strings.Contains(ref.Hint, c.hint)) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if got := h.task("w"); got.Status != "IN_PROGRESS" {
		t.Fatalf("refusals changed the task: %+v", got)
	}
}

// TestWaitPolicyAndEpisode: a wait policy rule sets the minimum; the first wait sets the episode's
// deadline, which later waits of the episode cannot pass (1.1).
func TestWaitPolicyAndEpisode(t *testing.T) {
	h := newExitHarness(t)
	a := h.c.Agents["demo"]
	a.Rules = append(append([]policies.Rule{}, a.Rules...), policies.Rule{Name: "slow-waits", Type: "wait", MinWaitInterval: "30m", Instruction: "wait at least 30 minutes"})
	h.c.Agents["demo"] = a
	h.create("w2", "notes", "demo")
	r2 := h.start("w2")
	if code, ref := h.wait(r2, map[string]any{"reason": "build", "after": "20m", "deadline": h.at(time.Hour)}); code != 409 || !strings.Contains(ref.Hint, "30m0s") {
		t.Fatal(code, ref)
	}
	// The first wait sets the episode's deadline; a later wait of the episode cannot pass it.
	if code, ref := h.wait(r2, map[string]any{"reason": "build", "after": "30m", "deadline": h.at(2 * time.Hour)}); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r2, "succeeded", "accepted")
	h.fake.Advance(30 * time.Minute)
	if err := h.s.ExpireWaits(context.Background(), storage.DefaultCompany); err != nil {
		t.Fatal(err)
	}
	r2 = h.start("w2")
	if code, ref := h.wait(r2, map[string]any{"reason": "build", "after": "30m", "deadline": h.at(5 * time.Hour)}); code != 409 || ref.Reason != "past_episode_deadline" {
		t.Fatal(code, ref)
	}
	if code, ref := h.wait(r2, map[string]any{"reason": "build", "after": "30m", "deadline": h.at(90 * time.Minute)}); code != 200 {
		t.Fatal(code, ref)
	}
}

// TestWaitTimerAfterGap: no run exists during a wait; the timer adds wait_ended once, also when Core was
// away for three hours past the wake time, and the next run is briefed on it (1.2).
func TestWaitTimerAfterGap(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("w", "notes", "demo")
	r := h.start("w")
	if code, ref := h.wait(r, map[string]any{"reason": "store processing", "after": "30m", "deadline": h.at(48 * time.Hour)}); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.task("w"); got.CurrentRunID != r.id || h.why("w").Reason != tasks.ReasonParked+tasks.ParkWait {
		t.Fatalf("a run was dispatched during the wait: %+v %+v", got, h.why("w"))
	}
	h.fake.Advance(10 * time.Minute)
	if err := h.s.ExpireWaits(ctx, storage.DefaultCompany); err != nil {
		t.Fatal(err)
	}
	if count(h.triggers("w"), "wait_ended") != 0 {
		t.Fatal("woken early", h.triggers("w"))
	}
	h.fake.Advance(3 * time.Hour) // Core was not running
	for range 3 {
		if err := h.s.ExpireWaits(ctx, storage.DefaultCompany); err != nil {
			t.Fatal(err)
		}
	}
	if got := h.triggers("w"); count(got, "wait_ended") != 1 {
		t.Fatal(got)
	}
	next := h.start("w")
	if run, _ := h.s.Get(ctx, storage.DefaultCompany, next.id); !strings.Contains(run.Spec.Prompt, "wait_ended") {
		t.Fatal(run.Spec.Prompt)
	}
	page, _ := h.s.Feed(ctx, storage.DefaultCompany, storage.FeedFilter{Task: "w"}, 0, 100)
	kinds := []string{}
	for _, f := range page.Items {
		kinds = append(kinds, f.Type)
	}
	if count(kinds, "task.exit.waiting") != 1 || count(kinds, "task.trigger.wait_ended") != 1 {
		t.Fatal(kinds)
	}
}

func answerEscalation(t *testing.T, h *exitHarness, reason, answer string) storage.Escalation {
	t.Helper()
	ctx := context.Background()
	open, _ := h.s.Escalations(ctx, storage.DefaultCompany, true)
	for _, e := range open {
		if e.Reason == reason {
			m, _ := h.s.Message(ctx, storage.DefaultCompany, e.MessageID)
			if _, err := h.s.ReplyMessage(ctx, storage.DefaultCompany, m.ID, messaging.Reply{Version: m.Version, Sender: "owner", Body: answer, Answers: map[string]any{"answer": answer}, RequestID: "a-" + e.ID}); err != nil {
				t.Fatal(err)
			}
			return e
		}
	}
	t.Fatalf("no open %s escalation: %+v", reason, open)
	return storage.Escalation{}
}

// TestWaitDeadlineExhaustionAndEarlyWake: the deadline passing during a wait and more than max_waits waits
// escalate to the manager with extend, retry, reassign and cancel; a directive ends a wait at once (1.3).
func TestWaitDeadlineExhaustionAndEarlyWake(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("d", "notes", "demo")
	r := h.start("d")
	if code, ref := h.wait(r, map[string]any{"reason": "review", "after": "1h", "deadline": h.at(time.Hour)}); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")
	h.fake.Advance(time.Hour)
	if err := h.s.ExpireWaits(ctx, storage.DefaultCompany); err != nil {
		t.Fatal(err)
	}
	if v := h.why("d"); v.Reason != tasks.ReasonParked+tasks.ParkEscalation || count(h.triggers("d"), "wait_ended") != 0 {
		t.Fatalf("%+v %v", v, h.triggers("d"))
	}
	e := answerEscalation(t, h, storage.WaitDeadlineReason, "extend")
	if e.Target != "manager" || strings.Join(e.Answers, ",") != "extend,retry,reassign,cancel" {
		t.Fatalf("%+v", e)
	}
	// extend: a new episode, the agent runs and may wait with a new deadline.
	r = h.start("d")
	if code, ref := h.wait(r, map[string]any{"reason": "review", "after": "1h", "deadline": h.at(10 * time.Hour)}); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")

	// Early wake: a manager's directive ends the wait as interrupted; the task runs now.
	if _, err := h.s.Directive(ctx, storage.DefaultCompany, "d", "manager", "the review is done, continue", false, "dir-1"); err != nil {
		t.Fatal(err)
	}
	if v := h.why("d"); v.Reason != tasks.ReasonRunnable {
		t.Fatalf("%+v", v)
	}
	page, _ := h.s.Feed(ctx, storage.DefaultCompany, storage.FeedFilter{Task: "d"}, 0, 100)
	if !slicesContains(page.Items, "task.wait_interrupted") {
		t.Fatalf("%+v", page.Items)
	}
	r = h.start("d")
	if w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_exit_done", Arguments: map[string]any{"summary": "review passed"}}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "succeeded", "accepted")

	// Exhaustion: max_waits 2, the third wait in a row escalates instead of parking.
	a := h.c.Agents["demo"]
	a.Rules = append(append([]policies.Rule{}, a.Rules...), policies.Rule{Name: "few-waits", Type: "wait", MaxWaits: 2, Instruction: "wait rarely"})
	h.c.Agents["demo"] = a
	h.create("x", "notes", "demo")
	deadline := h.at(24 * time.Hour)
	for i := range 3 {
		r = h.start("x")
		if code, ref := h.wait(r, map[string]any{"reason": "deploy", "after": "15m", "deadline": deadline}); code != 200 {
			t.Fatal(i, code, ref)
		}
		h.end(r, "succeeded", "accepted")
		if i < 2 {
			h.fake.Advance(15 * time.Minute)
			if err := h.s.ExpireWaits(ctx, storage.DefaultCompany); err != nil {
				t.Fatal(err)
			}
		}
	}
	if v := h.why("x"); v.Reason != tasks.ReasonParked+tasks.ParkEscalation {
		t.Fatalf("%+v", v)
	}
	answerEscalation(t, h, storage.WaitExhaustedReason, "reassign")
	if got := h.task("x"); got.Assignee != "" || h.why("x").Reason != tasks.ReasonUnassigned {
		t.Fatalf("owner's reassign leaves the task for a new assignee: %+v", got)
	}
}

func slicesContains(items []storage.FeedItem, kind string) bool {
	for _, f := range items {
		if f.Type == kind {
			return true
		}
	}
	return false
}

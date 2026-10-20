package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// exit sends one exit of the run at its brief's ack_seq.
func (h *exitHarness) exit(r exitRun, kind string, args map[string]any, facts *storage.ExitFacts) (int, storage.ExitRefusal) {
	h.t.Helper()
	var brief struct {
		AckSeq int64 `json:"ack_seq"`
	}
	_ = json.Unmarshal(h.do(r.token, "GET", "/agent/brief", nil, nil).Body.Bytes(), &brief)
	w := h.do(r.token, "POST", "/agent/exit", map[string]any{"kind": kind, "arguments": args, "ack_seq": brief.AckSeq, "facts": facts}, nil)
	return w.Code, refusalOf(h.t, w)
}

// approve takes a repository task through submit and the reviewer's approval of PR 7 at head h1.
func (h *exitHarness) approve(task string) {
	h.t.Helper()
	r := h.start(task)
	if code, ref := h.exit(r, "submit", map[string]any{"summary": "work", "reviewer": "reviewer"}, clean("h1", nil)); code != 200 {
		h.t.Fatal(code, ref)
	}
	h.end(r, "COMPLETED", "accepted")
	rv := h.start(task)
	if code, ref := h.exit(rv, "handoff", map[string]any{"to": "demo", "note": "ok", "verdict": "approved"}, clean("h1", &storage.PRFacts{Number: 7, State: "open", HeadSHA: "h1"})); code != 200 {
		h.t.Fatal(code, ref)
	}
	h.end(rv, "COMPLETED", "accepted")
}

// TestMergeFailuresAreAttempts (task-worktrees 3.3, D7): the implementer of an approved task reports a failed
// merge with handoff to itself and merge_failed; it counts as an attempt with backoff (1, then 5 minutes), and
// the third failed merge in a row escalates retries_exhausted. merge_failed is refused before an approval or
// handed to someone else.
func TestMergeFailuresAreAttempts(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("t", "code", "demo")
	open := &storage.PRFacts{Number: 7, State: "open", HeadSHA: "h1"}
	failed := map[string]any{"to": "demo", "note": "checks fail on main", "merge_failed": "required check build failed"}

	// Before an approval there is no merge to fail.
	r := h.start("t")
	if code, ref := h.exit(r, "handoff", failed, clean("h1", open)); code != 409 || ref.Reason != "invalid_exit" || !strings.Contains(ref.Hint, "approved task") {
		t.Fatal(code, ref)
	}
	if code, ref := h.exit(r, "submit", map[string]any{"summary": "work", "reviewer": "reviewer"}, clean("h1", nil)); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "COMPLETED", "accepted")
	rv := h.start("t")
	if code, ref := h.exit(rv, "handoff", map[string]any{"to": "demo", "note": "ok", "verdict": "approved"}, clean("h1", open)); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(rv, "COMPLETED", "accepted")

	waits := []time.Duration{time.Minute, 5 * time.Minute}
	for attempt := 1; attempt <= 3; attempt++ {
		r = h.start("t")
		if got := h.task("t"); got.Status != "IN_PROGRESS" || got.Attempt == 0 {
			t.Fatalf("attempt %d not running: %+v", attempt, got)
		}
		if attempt == 1 {
			other := map[string]any{"to": "reviewer", "note": "x", "merge_failed": "conflicts"}
			if code, ref := h.exit(r, "handoff", other, clean("h1", open)); code != 409 || ref.Reason != "invalid_exit" {
				t.Fatal("merge_failed handed to another agent:", code, ref)
			}
		}
		if code, ref := h.exit(r, "handoff", failed, clean("h1", open)); code != 200 {
			t.Fatal(code, ref)
		}
		h.end(r, "COMPLETED", "accepted")
		got := h.task("t")
		if got.AttemptsNoProgress != attempt || got.FailStreak != attempt || !strings.Contains(got.Result, "required check build failed") {
			t.Fatalf("attempt %d: %+v", attempt, got)
		}
		if attempt == 3 {
			if got.Status != "READY" || got.NotBefore != 0 {
				t.Fatalf("third failed merge: %+v", got)
			}
			break
		}
		if got.Status != "READY" || got.NotBefore != h.fake.Now().Add(waits[attempt-1]).Unix() {
			t.Fatalf("attempt %d backoff: %+v", attempt, got)
		}
		// Not before the backoff.
		h.fake.Advance(waits[attempt-1] - time.Second)
		if err := h.c.Schedule(ctx); err != nil {
			t.Fatal(err)
		}
		if h.task("t").Status != "READY" {
			t.Fatal("dispatched during the backoff")
		}
		h.fake.Advance(time.Second)
	}
	es, err := h.s.Escalations(ctx, storage.DefaultCompany, true)
	if err != nil || len(es) != 1 || es[0].Reason != "retries_exhausted" || es[0].TaskID != "t" || !strings.Contains(es[0].Summary, "3 merge attempts in a row failed") {
		t.Fatalf("%+v %v", es, err)
	}
	// The retry answer resets the counters and the implementer tries again.
	q, _ := h.s.Message(ctx, storage.DefaultCompany, es[0].MessageID)
	if w := h.do(h.c.Token, "POST", "/inbox/"+q.ID+"/answer", map[string]any{"version": q.Version, "body": "retry", "answers": map[string]any{storage.EscalationAnswerID: "retry"}, "request_id": "retry-1"}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if got := h.task("t"); got.Status != "READY" || got.AttemptsNoProgress != 0 {
		t.Fatalf("%+v", got)
	}
	// A merge that works ends the task.
	r = h.start("t")
	if code, ref := h.exit(r, "done", map[string]any{"summary": "merged"}, clean("h1", &storage.PRFacts{Number: 7, State: "merged", HeadSHA: "h1"})); code != 200 {
		t.Fatal(code, ref)
	}
}

// TestUnmergedBranchCheck (task-worktrees 4.3, D9): a task closed with its branch ahead of the default branch
// and an open pull request yields exactly one task.unmerged_branch event to the manager of its last assignee,
// however often the daily check runs; abandon_branch suppresses it; merged, old or open tasks yield none.
func TestUnmergedBranchCheck(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	open := &storage.PRFacts{Number: 9, State: "open", HeadSHA: "h1", URL: "https://example.com/app/pull/9"}
	cancel := func(task string) {
		t.Helper()
		v := h.task(task)
		if _, err := h.s.TaskAction(ctx, co, task, tasks.Action{Version: v.Version, Action: "cancel", Reason: "not needed", RequestID: "cancel-" + task}); err != nil {
			t.Fatal(err)
		}
	}
	// Cancelled tasks whose last exit handed over a branch with an open PR; "old" closed more than 30 days ago.
	// Each is cancelled before the next starts (demo runs one task at a time).
	for _, id := range []string{"old", "cancelled", "abandoned"} {
		h.create(id, "code", "demo")
		r := h.start(id)
		if code, ref := h.exit(r, "handoff", map[string]any{"to": "demo", "note": "later"}, clean("h1", open)); code != 200 {
			t.Fatal(code, ref)
		}
		h.end(r, "COMPLETED", "accepted")
		cancel(id)
		if id == "old" {
			h.fake.Advance(31 * 24 * time.Hour)
		}
	}
	if err := h.s.AbandonBranch(ctx, co, "abandoned", "owner"); err != nil {
		t.Fatal(err)
	}
	// A task done with its PR merged.
	h.create("merged", "code", "demo")
	h.approve("merged")
	r := h.start("merged")
	if code, ref := h.exit(r, "done", map[string]any{"summary": "merged"}, clean("h1", &storage.PRFacts{Number: 7, State: "merged", HeadSHA: "h1"})); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "COMPLETED", "accepted")
	// An open task with an open PR is not closed.
	h.create("open", "code", "demo")
	r = h.start("open")
	if code, ref := h.exit(r, "handoff", map[string]any{"to": "demo", "note": "later"}, clean("h1", open)); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "COMPLETED", "accepted")

	for range 2 { // two daily checks: still one event
		if err := h.c.checkUnmerged(ctx); err != nil {
			t.Fatal(err)
		}
		h.fake.Advance(unmergedCheckEvery)
	}
	page, err := h.s.Feed(ctx, co, storage.FeedFilter{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var events []storage.FeedItem
	for _, item := range page.Items {
		if item.Type == "task.unmerged_branch" {
			events = append(events, item)
		}
	}
	// demo reports to manager: the event goes to the manager of the last assignee.
	if len(events) != 1 || events[0].TaskID != "cancelled" || events[0].Agent != "manager" || !events[0].Attention ||
		!strings.Contains(events[0].Summary, "pull/9") || !strings.Contains(events[0].Summary, "abandon_branch") {
		t.Fatalf("%+v", events)
	}
	if n, err := h.s.ReportUnmergedBranches(ctx, co); err != nil || n != 0 {
		t.Fatal("a repeated check reported again:", n, err)
	}
}

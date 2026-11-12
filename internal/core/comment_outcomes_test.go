package core

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// owner-comments-handled: Core enforces the handling of the board's comments.

// newCommentHarness is the exit harness with the repository catalog's teams and a project of team core
// (lead manager; demo reports to manager) without a repository.
func newCommentHarness(t *testing.T) *exitHarness {
	t.Helper()
	h := newExitHarness(t)
	catalog, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	h.c.Teams = catalog.Teams
	if _, err = h.s.CreateProject(context.Background(), storage.DefaultCompany, tasks.ProjectCreate{ID: "tracely", Name: "Tracely", Workspace: "/tmp/exits-tracely", Team: "core"}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *exitHarness) comments(task string) []storage.Comment {
	h.t.Helper()
	v, err := h.s.Comments(context.Background(), storage.DefaultCompany, task)
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

func (h *exitHarness) commentOn(task, text string) string {
	h.t.Helper()
	if w := h.do(h.c.Token, "POST", "/tasks/"+task+"/comments", map[string]any{"text": text, "request_id": "c-" + task + "-" + text}, nil); w.Code != 200 {
		h.t.Fatal(w.Code, w.Body.String())
	}
	cs := h.comments(task)
	return cs[len(cs)-1].ID
}

func (h *exitHarness) countTriggers(task, kind string) int {
	h.t.Helper()
	var n int
	if err := h.s.DB.QueryRow("SELECT count(*) FROM task_triggers WHERE task_id=? AND kind=?", task, kind).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *exitHarness) callAt(r exitRun, name string, args map[string]any, ack int64) (int, storage.ExitRefusal, string) {
	h.t.Helper()
	w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: name, Arguments: args}, map[string]string{ackHeader: strconv.FormatInt(ack, 10)})
	var ref storage.ExitRefusal
	_ = json.Unmarshal(w.Body.Bytes(), &ref)
	return w.Code, ref, w.Body.String()
}

// TestBoardCommentReplay replays 2026-11-11 (tvorog a8ad9e45): the owner's "upload to TestFlight" on a developer's
// task cannot be dropped: the developer's exit is refused until it has an outcome, the forward goes to its
// manager (the project lead) as one message with the lead's locked-category copy, the task page shows
// "forwarded to manager", and the lead closes it with a task. A comment on the done task is refused with the two
// options; reopen and ask-the-lead work.
func TestBoardCommentReplay(t *testing.T) {
	h := newCommentHarness(t)
	ctx := context.Background()
	h.create("scroll", "tracely", "demo")
	r := h.start("scroll")
	ack := h.ack(r)

	// The owner's word arrives mid-run; it is in a locked category (release): the lead gets it at once.
	id := h.commentOn("scroll", "then upload the fix to TestFlight right away")
	lead := storage.ConversationID("tracely", "manager")
	if n := h.countTriggers(lead, "board_comment"); n != 1 {
		t.Fatalf("lead triggers %d", n)
	}
	if cs := h.comments("scroll"); cs[0].Category != "release" || len(cs[0].Outcomes) != 2 || cs[0].Outcomes[0].Status != "open" {
		t.Fatalf("%+v", cs)
	}
	// The developer reads the comment (ack) and tries to finish without handling it.
	ack = h.lastTriggerSeq("scroll")
	code, ref, body := h.callAt(r, "factory_exit_done", map[string]any{"summary": "fixed the scrubber"}, ack)
	if code != 409 || ref.Reason != "comments_unhandled" || len(ref.Comments) != 1 || ref.Comments[0].CommentID != id || !strings.Contains(ref.Hint, "factory_comment_forward") {
		t.Fatal(code, body)
	}
	// A done outcome for a comment that is not its own is refused too.
	if code, ref, body = h.callAt(r, "factory_exit_done", map[string]any{"summary": "fixed", "comments": []any{map[string]any{"comment_id": "nope", "outcome": "done"}}}, ack); code != 409 || ref.Reason != "invalid_exit" {
		t.Fatal(code, body)
	}
	// TestFlight is not the developer's: it forwards the comment up the chain.
	if code, _, body = h.callAt(r, "factory_comment_forward", map[string]any{"comment_id": id, "note": "TestFlight belongs to the release; the fix is merged"}, ack); code != 200 {
		t.Fatal(code, body)
	}
	if code, _, body = h.callAt(r, "factory_exit_done", map[string]any{"summary": "fixed the scrubber"}, ack); code != 200 || h.task("scroll").Status != "DONE" {
		t.Fatal(code, body)
	}
	h.end(r, "succeeded", "")
	// The manager is the lead: the forward and the lead's copy are one message.
	if n := h.countTriggers(lead, "board_comment"); n != 1 {
		t.Fatalf("lead triggers after the forward %d", n)
	}
	cs := h.comments("scroll")
	if got := storage.DescribeOutcomes(cs[0].Outcomes); !strings.Contains(got, "forwarded to manager by demo") || !strings.Contains(got, "not handled yet by manager") {
		t.Fatal(got)
	}

	// The lead's run: it cannot forward the board's word, nor exit without an outcome; done needs a task.
	lr := h.start(lead)
	var prompt string
	if err := h.s.DB.QueryRow("SELECT spec->>'prompt' FROM runs WHERE id=?", lr.id).Scan(&prompt); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "comment "+id+" on task scroll") || !strings.Contains(prompt, "forwarded by demo") || !strings.Contains(prompt, "You are the project lead") {
		t.Fatal(prompt)
	}
	lack := h.ack(lr)
	if code, _, body = h.callAt(lr, "factory_comment_forward", map[string]any{"comment_id": id, "note": "owner?"}, lack); code != 400 {
		t.Fatal(code, body)
	}
	if code, ref, body = h.callAt(lr, "factory_exit_done", map[string]any{"summary": "handled"}, lack); code != 409 || ref.Reason != "comments_unhandled" || !ref.Comments[0].NeedsTask {
		t.Fatal(code, body)
	}
	done := map[string]any{"comment_id": id, "outcome": "done"}
	if code, ref, body = h.callAt(lr, "factory_exit_done", map[string]any{"summary": "handled", "comments": []any{done}}, lack); code != 409 || ref.Reason != "invalid_exit" {
		t.Fatal(code, body)
	}
	h.create("release", "tracely", "manager")
	done["task_id"] = "release"
	if code, _, body = h.callAt(lr, "factory_exit_done", map[string]any{"summary": "release task created", "comments": []any{done}}, lack); code != 200 {
		t.Fatal(code, body)
	}
	if got := storage.DescribeOutcomes(h.comments("scroll")[0].Outcomes); !strings.Contains(got, "done by manager (task release)") {
		t.Fatal(got)
	}

	// 23:16: a comment on the done task is refused with the two options.
	w := h.do(h.c.Token, "POST", "/tasks/scroll/comments", map[string]any{"text": "why no simulator check?", "request_id": "late"}, nil)
	var closed struct {
		Reason  string              `json:"reason"`
		Options []map[string]string `json:"options"`
	}
	if w.Code != 409 || json.Unmarshal(w.Body.Bytes(), &closed) != nil || closed.Reason != "task_closed" || len(closed.Options) != 2 || closed.Options[0]["action"] != "reopen" || closed.Options[1]["lead"] != "manager" {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(h.comments("scroll")) != 1 {
		t.Fatal("a refused comment was saved")
	}
	// Ask the lead about it: a message to the project lead with the task linked.
	if w = h.do(h.c.Token, "POST", "/tasks/scroll/ask-lead", map[string]any{"text": "why no simulator check?", "request_id": "ask"}, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "About task scroll") || !strings.Contains(w.Body.String(), `"recipient":"manager"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// Reopen with comment: the task goes back to its assignee with the comment, which needs an outcome.
	if w = h.do(h.c.Token, "POST", "/tasks/scroll/reopen", map[string]any{"text": "check it on the simulator", "request_id": "reopen"}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if got := h.task("scroll"); got.Status != "READY" || got.Assignee != "demo" {
		t.Fatalf("%+v", got)
	}
	if cs = h.comments("scroll"); len(cs) != 2 || len(cs[1].Outcomes) != 1 || cs[1].Outcomes[0].Status != "open" {
		t.Fatalf("%+v", cs)
	}
	if w = h.do(h.c.Token, "POST", "/tasks/scroll/reopen", map[string]any{"text": "again", "request_id": "reopen-2"}, nil); w.Code != 409 {
		t.Fatal("reopened an open task", w.Code)
	}
	_ = ctx
}

// TestBoardCommentQuietCopy: a simple comment costs only the assignee's run (the lead is not woken) and reaches
// the lead's next brief with its outcome; a decline wakes the lead; a comment without the outcome done past the
// team's limit wakes the lead once; two runs ending without an exit raise an alert to the owner.
func TestBoardCommentQuietCopy(t *testing.T) {
	h := newCommentHarness(t)
	ctx := context.Background()
	lead := storage.ConversationID("tracely", "manager")
	h.create("copy", "tracely", "demo")
	fix := h.commentOn("copy", "fix the copy on the empty state")
	sim := h.commentOn("copy", "check it on the simulator")
	if h.countTriggers(lead, "board_comment") != 0 {
		t.Fatal("a simple comment woke the lead")
	}
	r := h.start("copy")
	var prompt string
	if err := h.s.DB.QueryRow("SELECT spec->>'prompt' FROM runs WHERE id=?", r.id).Scan(&prompt); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Board comments that need your outcome") || !strings.Contains(prompt, "comment "+fix) || !strings.Contains(prompt, "comment "+sim) {
		t.Fatal(prompt)
	}
	ack := h.ack(r)
	outcomes := []any{map[string]any{"comment_id": fix, "outcome": "done"}, map[string]any{"comment_id": sim, "outcome": "declined"}}
	if code, ref, body := h.callAt(r, "factory_exit_done", map[string]any{"summary": "copy fixed", "comments": outcomes}, ack); code != 409 || ref.Reason != "invalid_exit" {
		t.Fatal("a decline without a reason", code, body)
	}
	outcomes[1] = map[string]any{"comment_id": sim, "outcome": "declined", "reason": "this node has no simulator"}
	if code, _, body := h.callAt(r, "factory_exit_done", map[string]any{"summary": "copy fixed", "comments": outcomes}, ack); code != 200 {
		t.Fatal(code, body)
	}
	h.end(r, "succeeded", "")
	// Only the decline woke the lead.
	if n := h.countTriggers(lead, "board_comment"); n != 1 || !strings.Contains(h.lastTrigger(lead).body, "declined by demo: this node has no simulator") {
		t.Fatal(n, h.lastTrigger(lead).body)
	}

	// A comment on another task, not yet handled, is in the lead's next brief (quietly) with its state.
	h.create("later", "tracely", "demo")
	quiet := h.commentOn("later", "use the brand colour")
	if h.countTriggers(lead, "board_comment") != 1 {
		t.Fatal("the quiet copy woke the lead")
	}
	lr := h.start(lead)
	if err := h.s.DB.QueryRow("SELECT spec->>'prompt' FROM runs WHERE id=?", lr.id).Scan(&prompt); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Board comments in this project since your last run") || !strings.Contains(prompt, quiet+" on task later") || !strings.Contains(prompt, "not handled yet by demo") || !strings.Contains(prompt, "done by demo") {
		t.Fatal(prompt)
	}
	// The lead confirms the decline.
	if code, _, body := h.callAt(lr, "factory_exit_done", map[string]any{"summary": "confirmed", "comments": []any{map[string]any{"comment_id": sim, "outcome": "declined", "reason": "no simulator on our nodes yet; tracked separately"}}}, h.ack(lr)); code != 200 {
		t.Fatal(code, body)
	}
	h.end(lr, "succeeded", "")

	// Past the team's limit (default one hour) without done, the lead is woken for the comment once.
	h.fake.Advance(30 * time.Minute)
	if err := h.c.sweepComments(ctx, storage.DefaultCompany); err != nil {
		t.Fatal(err)
	}
	if h.countTriggers(lead, "board_comment") != 1 {
		t.Fatal("woken before the limit")
	}
	h.fake.Advance(31 * time.Minute)
	for range 2 {
		if err := h.c.sweepComments(ctx, storage.DefaultCompany); err != nil {
			t.Fatal(err)
		}
	}
	if n := h.countTriggers(lead, "board_comment"); n != 2 || !strings.Contains(h.lastTrigger(lead).body, "stalled") {
		t.Fatal(n, h.lastTrigger(lead).body)
	}

	// Two runs of the task end without an exit after being shown the comment: it stays open, the owner is alerted.
	for i := range 2 {
		h.fake.Advance(2 * time.Hour) // past the retry backoff
		r := h.start("later")
		h.end(r, "failed", "") // a crash: no exit
		var missed int
		if err := h.s.DB.QueryRow("SELECT missed FROM comment_outcomes WHERE comment_id=? AND task_id='later'", quiet).Scan(&missed); err != nil || missed != i+1 {
			t.Fatal(missed, err)
		}
	}
	if err := h.c.sweepComments(ctx, storage.DefaultCompany); err != nil {
		t.Fatal(err)
	}
	var alerts int
	if err := h.s.DB.QueryRow("SELECT count(*) FROM task_alerts WHERE kind='comment_unhandled' AND status='active' AND task_id='later' AND ref_id=?", quiet).Scan(&alerts); err != nil || alerts != 1 {
		t.Fatal(alerts, err)
	}
}

// TestForwardUpTheChain: a manager that cannot act forwards the comment on, so it reaches the lead without a
// parallel copy; the manager's done names its task.
func TestForwardUpTheChain(t *testing.T) {
	h := newCommentHarness(t)
	demo := h.c.Agents["demo"]
	demo.Spec.ReportsTo = "manager-autonomous"
	h.c.Agents["demo"] = demo
	lead := storage.ConversationID("tracely", "manager")
	middle := storage.ConversationID("tracely", "manager-autonomous")
	h.create("api", "tracely", "demo")
	id := h.commentOn("api", "rename the endpoint to /v2/tracks")
	r := h.start("api")
	if code, _, body := h.callAt(r, "factory_comment_forward", map[string]any{"comment_id": id, "note": "the API contract is the backend's"}, h.ack(r)); code != 200 || !strings.Contains(body, `"to":"manager-autonomous"`) {
		t.Fatal(code, body)
	}
	if code, _, body := h.callAt(r, "factory_exit_done", map[string]any{"summary": "nothing else to do"}, h.ack(r)); code != 200 {
		t.Fatal(code, body)
	}
	h.end(r, "succeeded", "")
	if h.countTriggers(middle, "board_comment") != 1 || h.countTriggers(lead, "board_comment") != 0 {
		t.Fatal("the forward did not go to the direct manager only")
	}
	mr := h.start(middle)
	if code, _, body := h.callAt(mr, "factory_comment_forward", map[string]any{"comment_id": id, "note": "needs the lead's call"}, h.ack(mr)); code != 200 || !strings.Contains(body, `"to":"manager"`) {
		t.Fatal(code, body)
	}
	if code, _, body := h.callAt(mr, "factory_exit_done", map[string]any{"summary": "forwarded"}, h.ack(mr)); code != 200 {
		t.Fatal(code, body)
	}
	if n := h.countTriggers(lead, "board_comment"); n != 1 || !strings.Contains(h.lastTrigger(lead).body, "forwarded by manager-autonomous") {
		t.Fatal(n)
	}
	if got := storage.DescribeOutcomes(h.comments("api")[0].Outcomes); got != "forwarded to manager-autonomous by demo; forwarded to manager by manager-autonomous; not handled yet by manager" {
		t.Fatal(got)
	}
}

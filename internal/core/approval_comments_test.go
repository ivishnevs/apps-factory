package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"appfactory/internal/approvals"
	"appfactory/internal/artifacts"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// approval-comments-handled: the owner's comment on an approval decision is a board comment on its task.

// sample registers version v of the screenshot sample on task and asks the owner to approve it from run.
func (h *exitHarness) sample(task, run, v string) approvals.Approval {
	h.t.Helper()
	ctx := context.Background()
	a := tasks.Artifact{ID: "screenshots", Version: v, URI: "file:///screens-" + v + ".png"}
	if _, err := h.s.RegisterArtifact(ctx, storage.DefaultCompany, "tracely", artifacts.Register{Artifact: a, TaskID: task, Actor: "owner", RequestID: "reg-" + task + v}); err != nil {
		h.t.Fatal(err)
	}
	req, err := h.s.RequestArtifactApproval(ctx, storage.DefaultCompany, "tracely", artifacts.ApprovalRequest{ArtifactID: a.ID, ArtifactVersion: v, Agent: "demo", TaskID: task, RunID: run, Reason: "approve the screenshot sample", RequestID: "ask-" + task + v})
	if err != nil {
		h.t.Fatal(err)
	}
	return req
}

func (h *exitHarness) decide(a approvals.Approval, action, reason string) {
	h.t.Helper()
	if w := h.do(h.c.Token, "POST", "/approvals/"+a.ID+"/decision", approvals.Decision{Version: a.Version, Action: action, Reason: reason, RequestID: "decide-" + a.ID}, nil); w.Code != 200 {
		h.t.Fatal(w.Code, w.Body.String())
	}
}

func (h *exitHarness) approvalOf(id string) approvals.Approval {
	h.t.Helper()
	var a approvals.Approval
	if w := h.do(h.c.Token, "GET", "/approvals/"+id, nil, nil); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &a) != nil {
		h.t.Fatal(w.Code, w.Body.String())
	}
	return a
}

// TestApprovalCommentReplay replays 2026-11-11 (tvorog 2509ee3e, Tracely screenshots): the board approves sample
// v2 with "the first screenshot without the callout"; the next run's brief lists it, its exit is refused until it
// records done with what it changed, and the approval (and the task's comment) show that outcome.
func TestApprovalCommentReplay(t *testing.T) {
	h := newCommentHarness(t)
	ctx := context.Background()
	h.create("shots", "tracely", "demo")
	r := h.start("shots")
	req := h.sample("shots", r.id, "2")
	h.callOK(r, "factory_exit_wait", map[string]any{"reason": "board approval of the sample", "after": "6h", "deadline": h.at(72 * time.Hour)})
	h.end(r, "succeeded", "accepted")

	h.decide(req, "approve", "the first screenshot without the callout")
	cs := h.comments("shots")
	if len(cs) != 1 || cs[0].Author != "owner" || cs[0].ApprovalID != req.ID || cs[0].Text != "the first screenshot without the callout" || len(cs[0].Outcomes) != 1 || cs[0].Outcomes[0].Status != "open" {
		t.Fatalf("%+v", cs)
	}
	id := cs[0].ID
	if a := h.approvalOf(req.ID); a.Status != "APPROVED" || a.CommentID != id || a.Comment == nil || a.Comment.Status != "open" || a.Comment.Outcome != "not handled yet by demo" {
		t.Fatalf("%+v %+v", a, a.Comment)
	}
	// The decision wakes the task (approval_decided) once; the comment adds no second wake-up.
	if tr := h.lastTrigger("shots"); tr.kind != "approval_decided" || h.countTriggers("shots", "owner_comment") != 0 {
		t.Fatalf("%+v", tr)
	}

	next := h.start("shots")
	var prompt string
	if err := h.s.DB.QueryRow("SELECT spec->>'prompt' FROM runs WHERE id=?", next.id).Scan(&prompt); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "comment "+id+" [on artifact approval "+req.ID+" of screenshots@2 approved]") || !strings.Contains(prompt, "its done needs reason") {
		t.Fatal(prompt)
	}
	ack := h.ack(next)
	code, ref, body := h.callAt(next, "factory_exit_done", map[string]any{"summary": "sample v2 final"}, ack)
	if code != 409 || ref.Reason != "comments_unhandled" || len(ref.Comments) != 1 || ref.Comments[0].CommentID != id || ref.Comments[0].Approval != req.ID || !strings.Contains(ref.Hint, "what you changed") {
		t.Fatal(code, body)
	}
	done := map[string]any{"comment_id": id, "outcome": "done"}
	if code, ref, body = h.callAt(next, "factory_exit_done", map[string]any{"summary": "sample v2 final", "comments": []any{done}}, ack); code != 409 || ref.Reason != "invalid_exit" || !strings.Contains(ref.Hint, "done needs reason") {
		t.Fatal("done without what changed", code, body)
	}
	done["reason"] = "frame 1 without the window"
	if code, _, body = h.callAt(next, "factory_exit_done", map[string]any{"summary": "sample v2 final", "comments": []any{done}}, ack); code != 200 {
		t.Fatal(code, body)
	}
	h.end(next, "succeeded", "")

	a := h.approvalOf(req.ID)
	if a.Comment == nil || a.Comment.Status != "done" || a.Comment.Outcome != "done by demo: frame 1 without the window" {
		t.Fatalf("%+v", a.Comment)
	}
	if got := storage.DescribeOutcomes(h.comments("shots")[0].Outcomes); got != "done by demo: frame 1 without the window" {
		t.Fatal(got)
	}
	// The Library and the task's materials read it through the artifact approval; the list of approvals too.
	if v, err := h.s.ArtifactApproval(ctx, storage.DefaultCompany, "tracely", "screenshots", "2"); err != nil || v.Comment == nil || v.Comment.Status != "done" {
		t.Fatal(v.Comment, err)
	}
	list, err := h.s.Approvals(ctx, storage.DefaultCompany, "tracely")
	if err != nil || len(list) != 1 || list[0].Comment == nil || list[0].Comment.Status != "done" {
		t.Fatal(list, err)
	}
	// The stored approval keeps only the link: the outcome is read, never saved.
	var stored string
	if err = h.s.DB.QueryRow("SELECT body::text FROM approvals WHERE id=?", req.ID).Scan(&stored); err != nil || strings.Contains(stored, `"comment":`) || !strings.Contains(stored, `"comment_id":"`+id+`"`) {
		t.Fatal(stored, err)
	}
}

// TestApprovalCommentKinds: a rejection's reason is a comment too (the revision says what it changed); the
// default reason, a delegated decision and a revoke are not; on a closed task the comment goes to the project
// lead; a command approval's comment works the same.
func TestApprovalCommentKinds(t *testing.T) {
	h := newCommentHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	lead := storage.ConversationID("tracely", "manager")

	// The default reason is no comment.
	h.create("plain", "tracely", "demo")
	plain := h.sample("plain", "", "p1")
	h.decide(plain, "approve", "approved by owner")
	if cs := h.comments("plain"); len(cs) != 0 {
		t.Fatalf("%+v", cs)
	}
	if a := h.approvalOf(plain.ID); a.CommentID != "" || a.Comment != nil {
		t.Fatalf("%+v", a)
	}
	// Revoking it with a reason adds none either.
	if w := h.do(h.c.Token, "POST", "/approvals/"+plain.ID+"/decision", approvals.Decision{Version: h.approvalOf(plain.ID).Version, Action: "revoke", Reason: "changed my mind", RequestID: "revoke-plain"}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if cs := h.comments("plain"); len(cs) != 0 {
		t.Fatalf("%+v", cs)
	}
	h.cancel("plain") // demo runs one task at a time

	// A rejection's reason is a comment: the revision run must record what it changed.
	h.create("rework", "tracely", "demo")
	rej := h.sample("rework", "", "r1")
	h.decide(rej, "reject", "the phone is too small")
	cs := h.comments("rework")
	if len(cs) != 1 || cs[0].ApprovalID != rej.ID || len(cs[0].Outcomes) != 1 {
		t.Fatalf("%+v", cs)
	}
	r := h.start("rework")
	if code, ref, body := h.callAt(r, "factory_exit_done", map[string]any{"summary": "v2"}, h.ack(r)); code != 409 || ref.Reason != "comments_unhandled" {
		t.Fatal(code, body)
	}
	if code, _, body := h.callAt(r, "factory_exit_done", map[string]any{"summary": "v2", "comments": []any{map[string]any{"comment_id": cs[0].ID, "outcome": "declined", "reason": "the store template fixes the phone size"}}}, h.ack(r)); code != 200 {
		t.Fatal(code, body)
	}
	h.end(r, "succeeded", "")
	if a := h.approvalOf(rej.ID); a.Comment == nil || a.Comment.Status != "open" || !strings.Contains(a.Comment.Outcome, "declined by demo: the store template fixes the phone size") {
		// The decline wakes the lead, who still has it: the approval shows both.
		t.Fatalf("%+v", a.Comment)
	}

	// A delegated decision (an agent decides) is not the board's comment.
	h.create("deleg", "tracely", "demo")
	del := h.sample("deleg", "", "d1")
	if _, err := h.s.DecideApproval(ctx, co, del.ID, approvals.Decision{Version: del.Version, Action: "approve", Reason: "looks right per the brand book", RequestID: "deleg", DecidedBy: "manager"}); err != nil {
		t.Fatal(err)
	}
	if cs := h.comments("deleg"); len(cs) != 0 {
		t.Fatalf("%+v", cs)
	}

	// A closed task runs no more: the comment goes to the project lead's conversation.
	h.create("closed", "tracely", "demo")
	late := h.sample("closed", "", "c1")
	h.cancel("closed")
	before := h.countTriggers(lead, "board_comment")
	h.decide(late, "approve", "next time crop the status bar")
	cs = h.comments("closed")
	if len(cs) != 1 || cs[0].ApprovalID != late.ID || len(cs[0].Outcomes) != 1 || cs[0].Outcomes[0].Agent != "manager" || cs[0].Outcomes[0].Reason != "closed" {
		t.Fatalf("%+v", cs)
	}
	if n := h.countTriggers(lead, "board_comment"); n != before+1 || !strings.Contains(h.lastTrigger(lead).body, "CANCELLED") {
		t.Fatal(n, h.lastTrigger(lead).body)
	}
}

func TestDecisionComment(t *testing.T) {
	for _, c := range []struct {
		action, reason string
		want           bool
	}{
		{"approve", "approved by owner", false},
		{"approve", "OK.", false},
		{"approve", "  lgtm ", false},
		{"approve", "ок", false},
		{"approve", "норм, но я бы мб телефон больше сделал", true},
		{"approve", "гуд только первый скрин не надо выносить плажку", true},
		{"reject", "no", true},
		{"reject", "", false},
	} {
		if got := storage.DecisionComment(c.action, c.reason); got != c.want {
			t.Errorf("%s %q: %v", c.action, c.reason, got)
		}
	}
}

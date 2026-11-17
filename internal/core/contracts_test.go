package core

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"appfactory/internal/closedset"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// onTaskBranch are clean exit facts of an implementer run whose task branch is af/<task>, checked out on branch with
// pull request pr.
func onTaskBranch(task, branch string, pr *storage.PRFacts) *storage.ExitFacts {
	f := clean("h1", pr)
	f.Branch, f.TaskBranch = branch, "af/"+task
	return f
}

// TestWrongBranchRefused replays 2026-11-15 with fixtures (contracts-at-the-boundary §1): the node created the
// worktree on af/<task>; the agent checked out another branch, pushed it, opened a pull request from it and submitted.
// Core refuses the exit with wrong_branch (the run goes on and the refusal is recorded for it); once the work is on the
// task branch the exit is accepted. A pull request from another branch is refused too, and an exit without the task
// branch in its facts (a review copy, the project workspace) is not checked.
func TestWrongBranchRefused(t *testing.T) {
	h := newExitHarness(t)
	h.create("audit", "code", "demo")
	r := h.start("audit")
	other := &storage.PRFacts{Number: 19, State: "open", HeadSHA: "h1", HeadBranch: "audit-onboard"}
	code, ref := h.exit(r, "submit", map[string]any{"summary": "audit"}, onTaskBranch("audit", "audit-onboard", other))
	if code != 409 || ref.Reason != "wrong_branch" || !strings.Contains(ref.Hint, "git checkout -B af/audit") || !strings.Contains(ref.Hint, "gh pr close 19") {
		t.Fatal("submit from another branch:", code, ref)
	}
	var recorded int
	if err := h.s.DB.QueryRow("SELECT count(*) FROM run_refusals WHERE run_id=? AND tool='factory_exit_submit' AND reason='wrong_branch' AND fixable=1 AND cleared_at=0", r.id).Scan(&recorded); err != nil || recorded != 1 {
		t.Fatal("refusal not recorded for the run:", recorded, err)
	}
	// On the task branch, but the pull request is still the one from the other branch.
	if code, ref = h.exit(r, "submit", map[string]any{"summary": "audit"}, onTaskBranch("audit", "af/audit", other)); code != 409 || ref.Reason != "wrong_branch" {
		t.Fatal("pull request from another branch:", code, ref)
	}
	if code, ref = h.exit(r, "submit", map[string]any{"summary": "audit"}, onTaskBranch("audit", "af/audit", nil)); code != 200 {
		t.Fatal("submit from the task branch:", code, ref)
	}
	if err := h.s.DB.QueryRow("SELECT count(*) FROM run_refusals WHERE run_id=? AND cleared_at=0", r.id).Scan(&recorded); err != nil || recorded != 0 {
		t.Fatal("the accepted exit left the refusal open:", recorded, err)
	}
	h.end(r, "succeeded", "accepted")

	// No task branch in the facts (a review copy, a fresh_from_default worktree): not checked.
	h.create("plain", "code", "demo")
	r = h.start("plain")
	if code, ref = h.exit(r, "submit", map[string]any{"summary": "work"}, clean("h2", &storage.PRFacts{Number: 3, State: "open", HeadSHA: "h2", HeadBranch: "feature"})); code != 200 {
		t.Fatal("exit without a task branch:", code, ref)
	}
}

// TestRefusedReportHoldsExit (contracts-at-the-boundary §3): a report kind outside the set is refused with every
// allowed kind, and the run cannot exit done past it until it creates the report or says why not.
func TestRefusedReportHoldsExit(t *testing.T) {
	h := newExitHarness(t)
	for i, fixed := range []bool{true, false} {
		task := []string{"created", "explained"}[i]
		h.create(task, "notes", "demo")
		r := h.start(task)
		ack := h.ack(r)
		report := map[string]any{"kind": "audit", "summary": "audit", "purpose": "onboarding", "changes": []any{}, "result": "options", "request_id": task + "-1"}
		code, _, body := h.callAt(r, "factory_report_create", report, ack)
		if code != 400 || !strings.Contains(body, "dependency_installation, environment_change, plan_deviation, problem, stage_completed") {
			t.Fatal("report kind audit:", code, body)
		}
		if code, ref, body := h.callAt(r, "factory_exit_done", map[string]any{"summary": "audited"}, ack); code != 409 || ref.Reason != "unresolved_refusal" || !strings.Contains(ref.Hint, "unresolved_reason") {
			t.Fatal("done past a refused report:", code, body)
		}
		exit := map[string]any{"summary": "audited", "unresolved_reason": "the summary is in the pull request"}
		if fixed {
			// Created again under a new request_id: any report created settles the refused one.
			report["kind"], report["request_id"] = "stage_completed", task+"-2"
			if code, _, body := h.callAt(r, "factory_report_create", report, ack); code != 200 {
				t.Fatal("report", code, body)
			}
			exit = map[string]any{"summary": "audited"}
		}
		if code, _, body := h.callAt(r, "factory_exit_done", exit, ack); code != 200 {
			t.Fatal("done", code, body)
		}
		h.end(r, "succeeded", "accepted")
	}
}

// TestClosedSetErrors (contracts-at-the-boundary §3): every closed-set refusal lists the allowed values, and
// factory_task_list refuses an unknown status instead of answering an empty list.
func TestClosedSetErrors(t *testing.T) {
	h := newExitHarness(t)
	h.create("t", "notes", "demo")
	r := h.start("t")
	ack := h.ack(r)
	for _, c := range []struct {
		tool string
		args map[string]any
		set  closedset.Set
	}{
		{"factory_task_list", map[string]any{"status": "open"}, closedset.TaskStatuses},
		{"factory_decision_list", map[string]any{"status": "pending"}, closedset.DecisionListStatuses},
		{"factory_report_list", map[string]any{"kind": "audit"}, closedset.ReportListKinds},
		{"factory_message_send", map[string]any{"recipient": "reviewer", "body": "b", "message_type": "info", "request_id": "m1"}, closedset.MessageTypes},
	} {
		code, _, body := h.callAt(r, c.tool, c.args, ack)
		if code != 400 || !strings.Contains(body, strings.Join(c.set[1:], ", ")) {
			t.Fatalf("%s: %d %s", c.tool, code, body)
		}
	}
	// The store refuses outside the set too (callers other than the tools).
	if _, err := h.s.Tasks(context.Background(), storage.DefaultCompany, "notes", "open"); err == nil || !strings.Contains(err.Error(), "one of: BACKLOG") {
		t.Fatal(err)
	}
}

// endFailed ends the run before its agent started with result and failure.
func (h *exitHarness) endFailed(r exitRun, outcome, result string, f *protocol.Failure) {
	h.t.Helper()
	if err := h.s.Apply(context.Background(), storage.DefaultCompany, protocol.Wrap("agent.ended", r.id, "", protocol.Event{RunID: r.id, Lease: r.lease, Status: "ENDED", Outcome: outcome, Result: result, Failure: f})); err != nil {
		h.t.Fatal(err)
	}
}

func (h *exitHarness) openEscalations(task string) []storage.Escalation {
	h.t.Helper()
	all, err := h.s.Escalations(context.Background(), storage.DefaultCompany, true)
	if err != nil {
		h.t.Fatal(err)
	}
	return slices.DeleteFunc(all, func(e storage.Escalation) bool { return e.TaskID != task })
}

// submitted takes a repository task through the implementer's submit to its reviewer.
func (h *exitHarness) submitted(task string) {
	h.t.Helper()
	h.create(task, "code", "demo")
	r := h.start(task)
	if code, ref := h.exit(r, "submit", map[string]any{"summary": "work"}, clean("h1", nil)); code != 200 {
		h.t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")
	if tk := h.task(task); tk.Status != "IN_REVIEW" || tk.Assignee != "reviewer" {
		h.t.Fatalf("not in review: %s %s", tk.Status, tk.Assignee)
	}
}

// TestFailureRoutesToImplementer replays 2026-11-15 with fixtures (contracts-at-the-boundary §2): the reviewer's run
// cannot make its review copy (not reviewable). The task goes back to its implementer with the cause, the reviewer's
// attempt is not counted, and nobody gets an escalation.
func TestFailureRoutesToImplementer(t *testing.T) {
	h := newExitHarness(t)
	h.submitted("audit")
	r := h.start("audit")
	h.endFailed(r, "setup_failed", "not reviewable: the task branch af/audit has no pushed head on the remote; the implementer must push it", &protocol.Failure{Code: protocol.FailureNotReviewable, Fixer: protocol.FixerImplementer})
	tk := h.task("audit")
	if tk.Assignee != "demo" || tk.Status != "READY" || !strings.Contains(tk.ReviewFeedback, "not reviewable") || !strings.Contains(tk.ReviewFeedback, "task branch") || tk.AttemptsNoProgress != 0 {
		t.Fatalf("task %s %s attempts %d feedback %q", tk.Assignee, tk.Status, tk.AttemptsNoProgress, tk.ReviewFeedback)
	}
	if es := h.openEscalations("audit"); len(es) != 0 {
		t.Fatalf("escalations %+v", es)
	}
	if tr := h.lastTrigger("audit"); !strings.Contains(tr.body, "returned to you") {
		t.Fatalf("trigger %+v", tr)
	}
}

// TestRepeatedFailureIsDeterministic (contracts-at-the-boundary §2): an unclassified setup failure retries once;
// the same failure of the same agent on the same handed-over head again is deterministic and goes to the
// implementer of the handed-over work instead of a third identical run.
func TestRepeatedFailureIsDeterministic(t *testing.T) {
	h := newExitHarness(t)
	h.submitted("t")
	h.endFailed(h.start("t"), "setup_failed", "review copy: something unexpected", nil)
	if tk := h.task("t"); tk.Assignee != "reviewer" || tk.AttemptsNoProgress != 1 {
		t.Fatalf("first failure: %s attempts %d", tk.Assignee, tk.AttemptsNoProgress)
	}
	h.fake.Advance(time.Hour)
	h.endFailed(h.start("t"), "setup_failed", "review copy: something unexpected", nil)
	if tk := h.task("t"); tk.Assignee != "demo" || tk.Status != "READY" || len(h.openEscalations("t")) != 0 {
		t.Fatalf("second identical failure: %s %s %+v", tk.Assignee, tk.Status, h.openEscalations("t"))
	}
}

// TestTransientAndOwnerFailures (contracts-at-the-boundary §2): lock contention keeps the bounded retry; a failure only
// the owner can fix (a missing secret) opens one owner item at once, without retries first.
func TestTransientAndOwnerFailures(t *testing.T) {
	h := newExitHarness(t)
	h.create("lock", "code", "demo")
	h.endFailed(h.start("lock"), "failed", "worktree: could not lock config file", &protocol.Failure{Code: protocol.FailureLock, Fixer: protocol.FixerNone})
	if tk := h.task("lock"); tk.Assignee != "demo" || tk.Status != "READY" || tk.AttemptsNoProgress != 1 || len(h.openEscalations("lock")) != 0 {
		t.Fatalf("transient: %s %s attempts %d", tk.Assignee, tk.Status, tk.AttemptsNoProgress)
	}

	h.create("secret", "code", "demo")
	h.endFailed(h.start("secret"), "setup_failed", "secret STORE_KEY was not delivered with the claim (set it on the Secrets page)", &protocol.Failure{Code: protocol.FailureSecret, Fixer: protocol.FixerOwner})
	es := h.openEscalations("secret")
	if len(es) != 1 || es[0].Reason != storage.SetupBlockedReason || es[0].Target != "owner" || !strings.Contains(es[0].Summary, "Secrets page") || h.task("secret").AttemptsNoProgress != 0 {
		t.Fatalf("owner failure: %+v", es)
	}
}

// TestEscalatedReviewReturns (contracts-at-the-boundary §2): a review that ran out of retries offers return and
// reassign; return gives the task to its implementer with the cause.
func TestEscalatedReviewReturns(t *testing.T) {
	h := newExitHarness(t)
	h.submitted("t")
	for i := range storage.RetryAttempts {
		h.fake.Advance(2 * time.Hour)
		h.endFailed(h.start("t"), "failed", "review run failed "+string(rune('a'+i)), nil)
	}
	es := h.openEscalations("t")
	if len(es) != 1 || es[0].Reason != "retries_exhausted" || !slices.Contains(es[0].Answers, "return") || !slices.Contains(es[0].Answers, "reassign") {
		t.Fatalf("escalations %+v", es)
	}
	q := h.message(es[0].MessageID)
	if _, err := h.s.ReplyMessage(context.Background(), storage.DefaultCompany, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: "return", Answers: map[string]any{storage.EscalationAnswerID: "return"}, RequestID: "return-1"}); err != nil {
		t.Fatal(err)
	}
	if tk := h.task("t"); tk.Assignee != "demo" || tk.Status != "READY" || !strings.Contains(tk.ReviewFeedback, "review run failed") {
		t.Fatalf("returned: %s %s %q", tk.Assignee, tk.Status, tk.ReviewFeedback)
	}
}

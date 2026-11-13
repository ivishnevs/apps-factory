package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"appfactory/internal/approvals"
	"appfactory/internal/artifacts"
	"appfactory/internal/factorytools"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// Approval gates (approval-gates-merge): fixtures are the harness's agents (demo implements, reviewer reviews,
// manager is demo's manager) and projects; the rules are general.

// produce registers an artifact version from the task's run and asks its approval (a delegate when given).
func (h *exitHarness) produce(task, project string, r exitRun, id, v, delegate string) approvals.Approval {
	h.t.Helper()
	ctx := context.Background()
	a := tasks.Artifact{ID: id, Version: v, URI: "https://example.com/" + id + "-" + v + ".html"}
	if _, err := h.s.RegisterArtifact(ctx, storage.DefaultCompany, project, artifacts.Register{Artifact: a, TaskID: task, RunID: r.id, Actor: "demo", RequestID: "reg-" + task + id + v}); err != nil {
		h.t.Fatal(err)
	}
	in := artifacts.ApprovalRequest{ArtifactID: id, ArtifactVersion: v, Agent: "demo", TaskID: task, RunID: r.id, Reason: "approve the prototype", RequestID: "ask-" + task + id + v}
	if delegate != "" {
		in.Delegate, in.Setting, in.Audience = delegate, "team core: internal artifacts agent "+delegate, "internal"
	}
	req, err := h.s.RequestArtifactApproval(ctx, storage.DefaultCompany, project, in)
	if err != nil {
		h.t.Fatal(err)
	}
	return req
}

// command asks Core's command guard about argv for the run.
func (h *exitHarness) command(r exitRun, argv ...string) (int, commandAnswer) {
	h.t.Helper()
	w := h.do(r.token, "POST", "/agent/command", map[string]any{"argv": argv}, nil)
	var a commandAnswer
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	return w.Code, a
}

// TestApprovalHoldsTask (1.1–1.4, 4.1–4.2): while the task's artifact waits for people, done and the merge are
// refused (other pushes run); approved needs every submitted artifact checked; after the review the task waits as
// awaiting_approval; the decision moves it: approved → merge and done, changes requested → back to the implementer,
// rejected → the pull request is closed unmerged and done needs no merge.
func TestApprovalHoldsTask(t *testing.T) {
	h := newExitHarness(t)
	open := &storage.PRFacts{Number: 7, State: "open", HeadSHA: "h1"}
	merged := &storage.PRFacts{Number: 7, State: "merged", HeadSHA: "h1"}
	for _, decision := range []string{"approve", "changes", "reject"} {
		id := "proto-" + decision
		h.create(id, "code", "demo")
		r := h.start(id)
		req := h.produce(id, "code", r, "home-"+decision, "3", "")
		art := req.Options[0].Artifact

		code, ref := h.exit(r, "done", map[string]any{"summary": "merged"}, clean("h1", merged))
		if code != 409 || ref.Reason != "awaiting_approval" || !strings.Contains(ref.Hint, req.ID) || !strings.Contains(ref.Hint, "approval_id") {
			t.Fatal("done while the artifact waits", code, ref)
		}
		for argv, want := range map[string]int{
			"gh pr merge 7 --squash":              409,
			"gh -R org/app pr merge 7":            409,
			"git push origin HEAD:main":           409,
			"git -C /w push -f origin +af/t:main": 409,
			"git push origin af/t":                200,
			"git push":                            200,
			"git status":                          200,
			"gh pr view 7":                        200,
		} {
			if code, a := h.command(r, strings.Fields(argv)...); code != want || want == 409 && (a.Rule != "task-merge" || !strings.Contains(a.Instruction, req.ID)) {
				t.Fatal(argv, code, a)
			}
		}
		submit := map[string]any{"summary": "prototype v3", "artifacts": []map[string]any{{"id": art.ID, "version": art.Version, "uri": art.URI}}}
		if code, ref = h.exit(r, "submit", submit, clean("h1", open)); code != 200 {
			t.Fatal(code, ref)
		}
		h.end(r, "succeeded", "accepted")

		rv := h.start(id)
		approve := map[string]any{"to": "demo", "note": "looks right", "verdict": "approved"}
		if code, ref = h.exit(rv, "handoff", approve, clean("h1", open)); code != 409 || ref.Reason != "review_incomplete" || !strings.Contains(ref.Hint, art.ID+"@3") {
			t.Fatal("approved without the artifact", code, ref)
		}
		approve["artifact_checks"] = []map[string]any{{"id": art.ID, "version": "3", "checked": false, "note": "did not run the prototype"}}
		if code, ref = h.exit(rv, "handoff", approve, clean("h1", open)); code != 409 || ref.Reason != "artifact_not_checked" || !strings.Contains(ref.Hint, "did not run the prototype") || !strings.Contains(ref.Hint, "changes_requested") {
			t.Fatal("approved with the artifact not checked", code, ref)
		}
		approve["artifact_checks"] = []map[string]any{{"id": art.ID + "@3", "checked": true, "note": "ran it: the home screen draws the new style"}}
		if code, ref = h.exit(rv, "handoff", approve, clean("h1", open)); code != 200 {
			t.Fatal(code, ref)
		}
		h.end(rv, "succeeded", "accepted")
		got := h.task(id)
		if got.Status != "READY" || got.Assignee != "demo" || got.ReviewChecks == nil || got.ReviewChecks.Reviewer != "reviewer" || len(got.ReviewChecks.Artifacts) != 1 || !got.ReviewChecks.Artifacts[0].Checked || got.ReviewChecks.Artifacts[0].Version != "3" {
			t.Fatalf("%+v %+v", got, got.ReviewChecks)
		}
		if v := h.verdict(id); v.Reason != tasks.ReasonParked+tasks.ParkApproval || v.Clearer.Who != "owner" || !strings.Contains(v.Detail, req.ID) {
			t.Fatalf("after the review: %+v", v)
		}

		// Authority input does not override the wait: only the decision moves the task.
		if w := h.do(h.c.Token, "POST", "/tasks/"+id+"/directives", map[string]any{"text": "merge it now", "request_id": "direct-" + id}, nil); w.Code != 200 || h.parkStatus(id, tasks.ParkApproval) != "active" {
			t.Fatal("directive", w.Code, w.Body.String(), h.parkStatus(id, tasks.ParkApproval))
		}
		reason := "the kangaroo needs the new style"
		if decision == "approve" {
			reason = "" // approving needs no comment (a comment would be a board comment the task owes an outcome)
		}
		h.decide(req, decision, reason)
		if h.parkStatus(id, tasks.ParkApproval) != "cleared" {
			t.Fatal("park", h.parkStatus(id, tasks.ParkApproval))
		}
		if v := h.verdict(id); v.Reason != tasks.ReasonRunnable {
			t.Fatalf("after the decision: %+v", v)
		}
		tr := h.lastTrigger(id)
		var prApprovals int
		_ = h.s.DB.QueryRow("SELECT count(*) FROM pr_approvals WHERE task_id=?", id).Scan(&prApprovals)
		next := h.start(id)
		switch decision {
		case "approve":
			if tr.kind != "approval_decided" || !strings.Contains(tr.body, "merge its pull request") || prApprovals != 1 {
				t.Fatalf("%+v %d", tr, prApprovals)
			}
			if code, _ := h.command(next, "gh", "pr", "merge", "7"); code != 200 {
				t.Fatal("merge after the approval", code)
			}
			if code, ref = h.exit(next, "done", map[string]any{"summary": "merged"}, clean("h1", merged)); code != 200 || h.task(id).Status != "DONE" {
				t.Fatal(code, ref)
			}
			h.end(next, "succeeded", "accepted")
		case "changes":
			got = h.task(id)
			if !strings.Contains(tr.body, "rework it on the same branch") || prApprovals != 0 || got.Assignee != "demo" || !strings.Contains(got.ReviewFeedback, "the kangaroo needs the new style") || got.ReviewChecks != nil {
				t.Fatalf("%+v %d %+v", tr, prApprovals, got)
			}
			h.end(next, "succeeded", "none")
			h.cancel(id)
		case "reject":
			if !strings.Contains(tr.body, "close the task's pull request unmerged") || prApprovals != 0 {
				t.Fatalf("%+v %d", tr, prApprovals)
			}
			if code, a := h.command(next, "gh", "pr", "merge", "7"); code != 409 || !strings.Contains(a.Instruction, "abandon") {
				t.Fatal("merge after the rejection", code, a)
			}
			closed := &storage.PRFacts{Number: 7, State: "closed", HeadSHA: "h1"}
			// The rejection's reason is a board comment the run answers on its exit.
			done := map[string]any{"summary": "closed unmerged: the design was rejected"}
			if code, ref = h.exit(next, "done", done, clean("h1", closed)); code != 409 || ref.Reason != "comments_unhandled" {
				t.Fatal(code, ref)
			}
			done["comments"] = []map[string]any{{"comment_id": ref.Comments[0].CommentID, "outcome": "done", "reason": "closed the pull request unmerged"}}
			if code, ref = h.exit(next, "done", done, clean("h1", closed)); code != 200 || h.task(id).Status != "DONE" {
				t.Fatal(code, ref)
			}
			h.end(next, "succeeded", "accepted")
		}
	}
}

// TestApprovalHoldsOnlyItsTask (1.5): a delegate's decision moves the task like people's; a wait on the approval
// parks it; approvals of another task's versions and command approvals do not hold it.
func TestApprovalHoldsOnlyItsTask(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany

	// The delegate holds the approval: the task waits for it, and its decision wakes the task.
	h.create("d", "notes", "demo")
	r := h.start("d")
	req := h.produce("d", "notes", r, "icon", "1", "manager")
	if code, ref := h.wait(r, map[string]any{"reason": "the icon", "approval_id": "0123456789abcdef"}); code != 409 || ref.Reason != "approval_not_holding" || !strings.Contains(ref.Hint, req.ID) {
		t.Fatal(code, ref)
	}
	if w := h.exitAt(r, "wait", map[string]any{"reason": "the icon", "approval_id": req.ID}, h.ack(r), nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "succeeded", "accepted")
	if v := h.verdict("d"); v.Reason != tasks.ReasonParked+tasks.ParkApproval || v.Clearer.Who != "agent:manager" {
		t.Fatalf("%+v", v)
	}
	if _, err := h.s.DecideApproval(ctx, co, req.ID, approvals.Decision{Version: req.Version, Action: "approve", Reason: "fits the brief", DecidedBy: "manager", RequestID: "delegate-approve"}); err != nil {
		t.Fatal(err)
	}
	if v := h.verdict("d"); v.Reason != tasks.ReasonRunnable || h.parkStatus("d", tasks.ParkApproval) != "cleared" {
		t.Fatalf("%+v", v)
	}
	if tr := h.lastTrigger("d"); tr.actor != "manager" || !strings.Contains(tr.body, "merge its pull request") {
		t.Fatalf("%+v", tr)
	}
	r = h.start("d")
	if code, ref := h.exit(r, "done", map[string]any{"summary": "icon approved"}, nil); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")

	// Another task's version and a command approval do not hold this task.
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "other", ProjectID: "notes", Title: "other", Assignee: "demo", RequestID: "create-other"}); err != nil {
		t.Fatal(err)
	}
	h.create("mine", "notes", "demo")
	r = h.start("mine")
	a := tasks.Artifact{ID: "logo", Version: "1", URI: "https://example.com/logo-1.png"}
	if _, err := h.s.RegisterArtifact(ctx, co, "notes", artifacts.Register{Artifact: a, TaskID: "other", Actor: "owner", RequestID: "reg-other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.RequestArtifactApproval(ctx, co, "notes", artifacts.ApprovalRequest{ArtifactID: "logo", ArtifactVersion: "1", Agent: "demo", TaskID: "mine", RunID: r.id, Reason: "approve the logo", RequestID: "ask-logo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.RequestApproval(ctx, co, approvals.Request{ProjectID: "notes", Agent: "demo", TaskID: "mine", RunID: r.id, Kind: "command", Call: factorytools.Call{Name: "command", Arguments: map[string]any{"argv": []any{"make", "release"}}}, Reason: "release", RequestID: "ask-cmd"}); err != nil {
		t.Fatal(err)
	}
	if holds, err := h.s.ApprovalHolds(ctx, co, "mine"); err != nil || len(holds) != 0 {
		t.Fatal(holds, err)
	}
	if holds, err := h.s.ApprovalHolds(ctx, co, "other"); err != nil || len(holds) != 1 {
		t.Fatal("the producing task is held", holds, err)
	}
	if code, ref := h.exit(r, "done", map[string]any{"summary": "done"}, nil); code != 200 {
		t.Fatal(code, ref)
	}
}

// TestPeopleReviewWaitsForApproval (1.3): a person's accept of a task whose artifact waits for a decision does not end
// it, even without a repository: it returns to its implementer and waits as awaiting_approval.
func TestPeopleReviewWaitsForApproval(t *testing.T) {
	h := newExitHarness(t)
	h.createReviewedBy("p", "notes", "demo", "owner")
	r := h.start("p")
	req := h.produce("p", "notes", r, "copy", "1", "")
	art := req.Options[0].Artifact
	if code, ref := h.exit(r, "submit", map[string]any{"summary": "store copy", "artifacts": []map[string]any{{"id": art.ID, "version": art.Version, "uri": art.URI}}}, nil); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")
	if w := h.do(h.c.Token, "POST", "/tasks/p/review", tasks.Review{Version: h.task("p").Version, Accept: true, RequestID: "accept-p"}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if got := h.task("p"); got.Status != "READY" || got.Assignee != "demo" {
		t.Fatalf("%+v", got)
	}
	if v := h.verdict("p"); v.Reason != tasks.ReasonParked+tasks.ParkApproval || !strings.Contains(h.lastTrigger("p").body, req.ID) {
		t.Fatalf("%+v", v)
	}
}

// TestApprovalLeavesPending: an approval that leaves pending undecided (expired) releases the task waiting on it with
// what to do; a newer request of the same task's next version holds it again.
func TestApprovalLeavesPending(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("e", "notes", "demo")
	r := h.start("e")
	req := h.produce("e", "notes", r, "flow", "1", "")
	if code, ref := h.wait(r, map[string]any{"reason": "the flow", "approval_id": req.ID}); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")
	h.fake.Advance(25 * time.Hour) // past the default one-day expiry
	if err := h.s.ExpireApprovals(ctx, storage.DefaultCompany); err != nil {
		t.Fatal(err)
	}
	if v := h.verdict("e"); v.Reason == tasks.ReasonParked+tasks.ParkApproval || h.parkStatus("e", tasks.ParkApproval) != "cleared" {
		t.Fatalf("%+v", v)
	}
	if tr := h.lastTrigger("e"); tr.kind != "approval_decided" || !strings.Contains(tr.body, "expired without a decision") {
		t.Fatalf("%+v", tr)
	}
	r = h.start("e")
	req = h.produce("e", "notes", r, "flow", "2", "")
	if code, ref := h.exit(r, "done", map[string]any{"summary": "flow v2"}, nil); code != 409 || ref.Reason != "awaiting_approval" || !strings.Contains(ref.Hint, req.ID) {
		t.Fatal(code, ref)
	}
}

// TestBindingReviewer (3.2–3.3): a submit goes to the task's reviewer; naming another is refused with the task's
// reviewer named; a task without one takes the default (the assignee's manager) and records it; changing the
// reviewer is for people, the creator and the assignee's managers.
func TestBindingReviewer(t *testing.T) {
	h := newExitHarness(t)
	co := storage.DefaultCompany
	h.create("b", "code", "demo")
	r := h.start("b")
	if code, ref := h.exit(r, "submit", map[string]any{"summary": "login", "reviewer": "manager"}, clean("h1", nil)); code != 409 || ref.Reason != "reviewer_not_task_reviewer" || !strings.Contains(ref.Hint, "reviewer is reviewer") {
		t.Fatal(code, ref)
	}
	if code, ref := h.exit(r, "submit", map[string]any{"summary": "login"}, clean("h1", nil)); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")
	if got := h.task("b"); got.Status != "IN_REVIEW" || got.Assignee != "reviewer" || got.Reviewer != "reviewer" {
		t.Fatalf("%+v", got)
	}
	h.cancel("b")

	h.createReviewedBy("n", "code", "demo", "")
	r = h.start("n")
	if code, ref := h.exit(r, "submit", map[string]any{"summary": "login", "reviewer": "reviewer"}, clean("h1", nil)); code != 409 || !strings.Contains(ref.Hint, "reviewer is manager") {
		t.Fatal("the default binds too", code, ref)
	}
	if code, ref := h.exit(r, "submit", map[string]any{"summary": "login"}, clean("h1", nil)); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")
	if got := h.task("n"); got.Assignee != "manager" || got.ReviewBy != "manager" {
		t.Fatalf("%+v", got)
	}

	// The reviewer hands the review on: the task stays in review with the new reviewer.
	rv := h.start("n")
	if code, ref := h.exit(rv, "handoff", map[string]any{"to": "reviewer", "note": "you know the API"}, clean("h1", nil)); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(rv, "succeeded", "accepted")
	if got := h.task("n"); got.Status != "IN_REVIEW" || got.Assignee != "reviewer" || got.Reviewer != "reviewer" || got.Implementer != "demo" {
		t.Fatalf("%+v", got)
	}

	// Changing the reviewer is an authority action.
	h.create("c", "notes", "demo")
	dev := h.start("c")
	if code, body := h.callCode(dev, "factory_task_update", map[string]any{"id": "c", "version": h.task("c").Version, "review_by": "manager", "request_id": "self"}); code != 403 {
		t.Fatal("the assignee changed its reviewer", code, body)
	}
	created := tasks.Task{ID: "x", Assignee: "demo", CreatedBy: "calorie-qa", Status: "READY"}
	inReview := tasks.Task{ID: "y", Assignee: "reviewer", Implementer: "demo", CreatedBy: "owner", Status: "IN_REVIEW"}
	for _, c := range []struct {
		actor string
		t     tasks.Task
		ok    bool
	}{{"", created, true}, {"calorie-qa", created, true}, {"manager", created, true}, {"reviewer", created, false}, {"demo", created, false},
		{"manager", inReview, true}, {"reviewer", inReview, false}} {
		if got := h.c.mayChangeReviewer(co, c.actor, c.t); got != c.ok {
			t.Fatalf("%q on %s: %v", c.actor, c.t.ID, got)
		}
	}
}

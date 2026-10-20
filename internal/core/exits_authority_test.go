package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"appfactory/internal/approvals"
	"appfactory/internal/artifacts"
	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// ack is the run's starting ack_seq (its brief window end).
func (h *exitHarness) ack(r exitRun) int64 {
	h.t.Helper()
	var brief struct {
		AckSeq int64 `json:"ack_seq"`
	}
	if err := json.Unmarshal(h.do(r.token, "GET", "/agent/brief", nil, nil).Body.Bytes(), &brief); err != nil {
		h.t.Fatal(err)
	}
	return brief.AckSeq
}

func (h *exitHarness) exitAt(r exitRun, kind string, args map[string]any, ack int64, facts *storage.ExitFacts) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.do(r.token, "POST", "/agent/exit", map[string]any{"kind": kind, "arguments": args, "ack_seq": ack, "facts": facts}, nil)
}

// call is an exit or tool call through /agent/call that must succeed; it returns the decoded result.
func (h *exitHarness) callOK(r exitRun, name string, args map[string]any) map[string]any {
	h.t.Helper()
	w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: name, Arguments: args}, nil)
	if w.Code != 200 {
		h.t.Fatal(name, w.Code, w.Body.String())
	}
	var v map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &v)
	return v
}

type triggerRow struct {
	kind, actor, body, addressee string
	authority                    bool
}

// lastTrigger is the task's newest trigger.
func (h *exitHarness) lastTrigger(task string) triggerRow {
	h.t.Helper()
	var r triggerRow
	if err := h.s.DB.QueryRow("SELECT kind,actor,body,addressee,authority FROM task_triggers WHERE task_id=? ORDER BY seq DESC LIMIT 1", task).Scan(&r.kind, &r.actor, &r.body, &r.addressee, &r.authority); err != nil {
		h.t.Fatal(err)
	}
	return r
}

// parkStatus is the status of the task's newest park of kind ("" when it has none).
func (h *exitHarness) parkStatus(task, kind string) string {
	var status string
	_ = h.s.DB.QueryRow("SELECT status FROM task_parks WHERE task_id=? AND kind=? ORDER BY created_at DESC, seq DESC LIMIT 1", task, kind).Scan(&status)
	return status
}

func (h *exitHarness) verdict(task string) tasks.Verdict {
	h.t.Helper()
	snap, err := h.s.TaskSnapshot(context.Background(), storage.DefaultCompany, task)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.c.evaluate(snap)
}

func (h *exitHarness) cancel(task string) {
	h.t.Helper()
	if _, err := h.s.TaskAction(context.Background(), storage.DefaultCompany, task, tasks.Action{Version: h.task(task).Version, Action: "cancel", Cascade: true}); err != nil {
		h.t.Fatal(err)
	}
}

// TestVerdictsHandoffsAndOwnerReview (1.2): changes_requested needs feedback and returns the task to its
// implementer with it; a handoff without verdict gives the task to another configured agent with an
// assignment trigger; the owner's review maps to a verdict (a repository task returns to its implementer,
// a scratch one ends DONE).
func TestVerdictsHandoffsAndOwnerReview(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	pr := &storage.PRFacts{Number: 9, State: "open", HeadSHA: "h1"}
	h.create("v", "code", "demo")
	r := h.start("v")
	if w := h.exitAt(r, "submit", map[string]any{"summary": "login", "reviewer": "reviewer"}, h.ack(r), clean("h1", pr)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "succeeded", "accepted")

	rv := h.start("v")
	rework := map[string]any{"to": "demo", "note": "not yet", "verdict": "changes_requested"}
	if w := h.exitAt(rv, "handoff", rework, h.ack(rv), clean("h1", pr)); w.Code != 409 || refusalOf(t, w).Reason != "invalid_exit" {
		t.Fatal(w.Code, w.Body.String())
	}
	rework["feedback"] = "add tests for the error path"
	if w := h.exitAt(rv, "handoff", rework, h.ack(rv), clean("h1", pr)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	got := h.task("v")
	if got.Status != "READY" || got.Assignee != "demo" || got.ReviewFeedback != "add tests for the error path" || got.Reviewer != "" || got.Implementer != "demo" {
		t.Fatalf("%+v", got)
	}
	if tr := h.lastTrigger("v"); tr.kind != "assignment" || tr.authority || tr.addressee != "demo" || !strings.Contains(tr.body, "handoff from reviewer") {
		t.Fatalf("%+v", tr)
	}
	var approvals int
	_ = h.s.DB.QueryRow("SELECT count(*) FROM pr_approvals WHERE task_id='v'").Scan(&approvals)
	if approvals != 0 {
		t.Fatal("changes_requested recorded an approval")
	}
	h.end(rv, "succeeded", "accepted")

	// The implementer hands the task on to another agent (no verdict).
	r = h.start("v")
	if run, _ := h.s.Get(ctx, storage.DefaultCompany, r.id); !strings.Contains(run.Spec.Prompt, "add tests for the error path") {
		t.Fatal("the rework run does not see the feedback:\n", run.Spec.Prompt)
	}
	if w := h.exitAt(r, "handoff", map[string]any{"to": "nobody", "note": "x"}, h.ack(r), clean("h2", pr)); w.Code != 409 || refusalOf(t, w).Reason != "invalid_exit" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := h.exitAt(r, "handoff", map[string]any{"to": "reviewer", "note": "you know the API better"}, h.ack(r), clean("h2", pr)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if got = h.task("v"); got.Status != "READY" || got.Assignee != "reviewer" || got.AttemptsNoProgress != 0 {
		t.Fatalf("%+v", got)
	}
	if tr := h.lastTrigger("v"); tr.kind != "assignment" || tr.addressee != "reviewer" || !strings.Contains(tr.body, "you know the API better") {
		t.Fatalf("%+v", tr)
	}
	h.end(r, "succeeded", "accepted")
	h.cancel("v")

	// The owner reviews (no reviewer named): reject and approve are verdicts.
	review := func(task string, accept bool, feedback string) tasks.Task {
		t.Helper()
		w := h.do(h.c.Token, "POST", "/tasks/"+task+"/review", tasks.Review{Version: h.task(task).Version, Accept: accept, Feedback: feedback, RequestID: task + feedback}, nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		return h.task(task)
	}
	submit := func(task, project string) {
		t.Helper()
		r := h.start(task)
		var facts *storage.ExitFacts
		if project == "code" {
			facts = clean("h3", &storage.PRFacts{Number: 10, State: "open", HeadSHA: "h3"})
		}
		if w := h.exitAt(r, "submit", map[string]any{"summary": "done"}, h.ack(r), facts); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		h.end(r, "succeeded", "accepted")
		if got := h.task(task); got.Status != "IN_REVIEW" || got.Assignee != "demo" || got.Reviewer != "" {
			t.Fatalf("%+v", got)
		}
	}
	h.create("o", "code", "demo")
	submit("o", "code")
	if got = review("o", false, "rename the screen"); got.Status != "READY" || got.Assignee != "demo" || got.ReviewFeedback != "rename the screen" {
		t.Fatalf("%+v", got)
	}
	if tr := h.lastTrigger("o"); tr.kind != "review_result" || !tr.authority || tr.body != "changes requested: rename the screen" {
		t.Fatalf("%+v", tr)
	}
	submit("o", "code")
	if got = review("o", true, ""); got.Status != "READY" || got.Assignee != "demo" {
		t.Fatalf("%+v", got)
	}
	if tr := h.lastTrigger("o"); !strings.HasPrefix(tr.body, "approved: merge your pull request") {
		t.Fatalf("%+v", tr)
	}
	h.cancel("o")
	h.create("s", "notes", "demo")
	submit("s", "notes")
	if got = review("s", true, ""); got.Status != "DONE" {
		t.Fatalf("%+v", got)
	}
}

// TestDoneRefusedByUnseenTrigger (1.3): done is refused while any trigger (here a non-authority one) is above
// ack_seq; other exits pass it; done passes once the trigger was seen.
func TestDoneRefusedByUnseenTrigger(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("d", "notes", "demo")
	r := h.start("d")
	ack := h.ack(r)
	if _, err := h.s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ID: "d-sub", ProjectID: "notes", Title: "sub", ParentTaskID: "d", RequestID: "sub"}); err != nil {
		t.Fatal(err)
	}
	h.cancel("d-sub") // the last child closed: a system trigger on d
	w := h.exitAt(r, "done", map[string]any{"summary": "notes written"}, ack, nil)
	unseen := refusalOf(t, w).Unseen
	if w.Code != 409 || len(unseen) != 1 || unseen[0].Kind != "last_child_closed" || unseen[0].Authority {
		t.Fatal(w.Code, w.Body.String())
	}
	if h.task("d").Status != "IN_PROGRESS" {
		t.Fatal("a refused exit changed the task")
	}
	if w = h.do(r.token, "POST", "/agent/exit/preflight", map[string]any{"kind": "submit", "arguments": map[string]any{"summary": "s"}, "ack_seq": ack}, nil); w.Code != 200 {
		t.Fatal("non-authority input blocked submit", w.Code, w.Body.String())
	}
	if w = h.exitAt(r, "done", map[string]any{"summary": "notes written"}, unseen[0].Seq, nil); w.Code != 200 || h.task("d").Status != "DONE" {
		t.Fatal(w.Code, w.Body.String())
	}
}

// TestBlockFallsBackToAsk (1.6): a block on a closed task, on a task of another project or on no open child
// becomes an ask; a block on open children holds the task until the last one closes.
func TestBlockFallsBackToAsk(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	for id, project := range map[string]string{"fin": "notes", "far": "code"} {
		if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: project, Title: id, RequestID: id}); err != nil {
			t.Fatal(err)
		}
	}
	h.cancel("fin")
	for name, args := range map[string]map[string]any{
		"closed":      {"on": []any{"fin"}, "reason": "needs fin"},
		"foreign":     {"on": []any{"far"}, "reason": "needs far"},
		"no-children": {"on_children": true, "reason": "waits for children"},
	} {
		h.create(name, "notes", "demo")
		r := h.start(name)
		v := h.callOK(r, "factory_exit_block", args)
		h.end(r, "succeeded", "accepted")
		note, _ := v["note"].(string)
		if got := h.task(name); !strings.Contains(note, "asked your manager instead") || got.WaitMessageID == "" || got.Status != "BLOCKED" || len(got.Dependencies) != 0 {
			t.Fatalf("%s: %v %+v", name, v, got)
		}
		if q, _ := h.s.Message(ctx, co, h.task(name).WaitMessageID); !strings.Contains(q.Body, "I am blocked") || q.Recipient != "manager" {
			t.Fatalf("%s: %+v", name, q)
		}
	}

	h.create("parent", "notes", "demo")
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "kid", ProjectID: "notes", Title: "kid", ParentTaskID: "parent", RequestID: "kid"}); err != nil {
		t.Fatal(err)
	}
	r := h.start("parent")
	if v := h.callOK(r, "factory_exit_block", map[string]any{"on_children": true, "reason": "after the kid"}); v["note"] != nil {
		t.Fatal(v)
	}
	h.end(r, "succeeded", "accepted")
	if got := h.task("parent"); got.Status != "BLOCKED" || h.parkStatus("parent", tasks.ParkChildren) != "active" {
		t.Fatalf("%+v", got)
	}
	h.cancel("kid")
	if got := h.task("parent"); got.Status != "READY" || h.parkStatus("parent", tasks.ParkChildren) != "cleared" || h.verdict("parent").Reason != tasks.ReasonRunnable {
		t.Fatalf("%+v %+v", got, h.verdict("parent"))
	}
}

// TestNoExitEscalationAnswers (2.2): the no_exit escalation goes to the assignee's manager with retry,
// reassign, accept and cancel; retry runs the task again, reassign by the manager gives it to the manager
// (by the owner: unassigned until the owner chooses), cancel ends it. The manager's answer is authority input.
func TestNoExitEscalationAnswers(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	for _, c := range []struct{ name, answer, by string }{
		{"retry", "retry", "calorie-manager"}, {"reassign", "reassign", "calorie-manager"},
		{"owner-reassign", "reassign", "owner"}, {"cancel", "cancel", "calorie-manager"},
	} {
		id := "ne-" + c.name
		h.create(id, "notes", "calorie-ios")
		r := h.start(id)
		h.end(r, "succeeded", "none")
		var e storage.Escalation
		open, _ := h.s.Escalations(ctx, co, true)
		for _, x := range open {
			if x.TaskID == id {
				e = x
			}
		}
		if e.Reason != "no_exit" || e.Target != "calorie-manager" || !slices.Equal(e.Answers, []string{"retry", "reassign", "accept", "cancel"}) {
			t.Fatalf("%s: %+v", c.name, e)
		}
		q, _ := h.s.Message(ctx, co, e.MessageID)
		if !strings.Contains(q.Body, "for review: the agent did not finish") {
			t.Fatal(q.Body)
		}
		if _, err := h.s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: c.by, Body: c.answer, Answers: map[string]any{"answer": c.answer}, RequestID: id}); err != nil {
			t.Fatal(c.name, err)
		}
		got := h.task(id)
		switch c.name {
		case "retry":
			if got.Status != "READY" || got.AttemptsNoProgress != 0 || got.NotBefore != 0 || h.verdict(id).Reason != tasks.ReasonRunnable {
				t.Fatalf("%+v %+v", got, h.verdict(id))
			}
		case "reassign":
			if got.Assignee != "calorie-manager" || got.Status != "READY" {
				t.Fatalf("%+v", got)
			}
		case "owner-reassign":
			if got.Assignee != "" || got.Status != "READY" || h.verdict(id).Reason != tasks.ReasonUnassigned {
				t.Fatalf("%+v %+v", got, h.verdict(id))
			}
		case "cancel":
			if got.Status != "CANCELLED" {
				t.Fatalf("%+v", got)
			}
			continue
		}
		if tr := h.lastTrigger(id); tr.kind != "answer" || !tr.authority || tr.actor != c.by {
			t.Fatalf("%s: %+v", c.name, tr)
		}
		h.cancel(id)
	}
}

// TestArtifactApprovedIsAuthority (3.2): approving an artifact version is authority input for the tasks that
// require it and for the tasks pinned to another version of it; an urgent approval stops their live runs.
func TestArtifactApprovedIsAuthority(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	v1 := tasks.Artifact{ID: "mockup", Version: "1", URI: "file:///mockup-1.png"}
	v2 := tasks.Artifact{ID: "mockup", Version: "2", URI: "file:///mockup-2.png"}
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "design", ProjectID: "notes", Title: "design", Assignee: "calorie-designer", RequestID: "design"}); err != nil {
		t.Fatal(err)
	}
	approve := func(a tasks.Artifact, urgent bool) {
		t.Helper()
		if _, err := h.s.RegisterArtifact(ctx, co, "notes", artifacts.Register{Artifact: a, TaskID: "design", Actor: "owner", RequestID: "reg-" + a.Version}); err != nil {
			t.Fatal(err)
		}
		req, err := h.s.RequestArtifactApproval(ctx, co, "notes", artifacts.ApprovalRequest{ArtifactID: a.ID, ArtifactVersion: a.Version, Agent: "calorie-designer", TaskID: "design", Reason: "review the mockup", RequestID: "ask-" + a.Version})
		if err != nil {
			t.Fatal(err)
		}
		w := h.do(h.c.Token, "POST", "/approvals/"+req.ID+"/decision", approvals.Decision{Version: req.Version, Action: "approve", Reason: "ok", Urgent: urgent, RequestID: "ok-" + a.Version}, nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// In this order: the older work of "uses" (its trigger) is dispatched before "needs" after the urgent stop.
	for _, c := range []struct {
		id string
		a  tasks.Artifact
	}{{"uses", v1}, {"needs", v2}} {
		id, a := c.id, c.a
		if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "notes", Title: id, Assignee: "demo", RequiredArtifacts: []tasks.Artifact{a}, Ready: true, RequestID: id}); err != nil {
			t.Fatal(err)
		}
	}
	approve(v1, false)
	if tr := h.lastTrigger("uses"); tr.kind != "artifact_approved" || !tr.authority || tr.body != "artifact mockup@1 approved" {
		t.Fatalf("%+v", tr)
	}
	r := h.start("uses")
	approve(v2, true)
	for id, body := range map[string]string{"needs": "artifact mockup@2 approved", "uses": "artifact mockup@2 approved (a new version of mockup@1, which this task uses)"} {
		if tr := h.lastTrigger(id); tr.kind != "artifact_approved" || !tr.authority || tr.actor != "owner" || tr.body != body {
			t.Fatalf("%s: %+v", id, tr)
		}
	}
	if run, _ := h.s.Get(ctx, co, r.id); run.Status != "CANCELLING" {
		t.Fatal("urgent approval did not stop the pinned task's run:", run.Status)
	}
	h.end(r, "revoked", "none")
	next := h.start("uses")
	if run, _ := h.s.Get(ctx, co, next.id); !strings.Contains(run.Spec.Prompt, "a new version of mockup@1") {
		t.Fatal(run.Spec.Prompt)
	}
}

// TestDirectivesRankByDepth (3.3): every tool response of the run shows the unseen authority input with
// author and depth rank; among conflicting directives the one nearest the owner wins, whatever their order.
func TestDirectivesRankByDepth(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	junior := h.c.Agents["calorie-ios"]
	junior.Metadata.Name, junior.Spec.ReportsTo = "junior", "calorie-ios"
	h.c.Agents["junior"] = junior
	h.create("rank", "notes", "junior")
	r := h.start("rank")
	ack := h.ack(r)
	type wrapped struct {
		AuthorityInput []storage.AuthorityInput `json:"authority_input"`
		Winning        int64                    `json:"winning"`
	}
	look := func() wrapped {
		t.Helper()
		w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_project_get", Arguments: map[string]any{}}, map[string]string{ackHeader: strconv.FormatInt(ack, 10)})
		var v wrapped
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		return v
	}
	direct := func(actor, text string) int64 {
		t.Helper()
		if _, err := h.s.Directive(ctx, co, "rank", actor, text, false, actor+text); err != nil {
			t.Fatal(err)
		}
		return h.lastTriggerSeq("rank")
	}
	manager := direct("calorie-manager", "use GraphQL")
	lead := direct("calorie-ios", "use REST")
	v := look()
	if len(v.AuthorityInput) != 2 || v.AuthorityInput[0].Author != "calorie-manager" || v.AuthorityInput[0].Rank != 1 || v.AuthorityInput[1].Author != "calorie-ios" || v.AuthorityInput[1].Rank != 2 {
		t.Fatalf("%+v", v)
	}
	if v.Winning != manager || v.Winning == lead {
		t.Fatalf("the later, deeper directive won: %+v", v)
	}
	owner := direct("owner", "keep REST, it ships today")
	if v = look(); len(v.AuthorityInput) != 3 || v.AuthorityInput[2].Rank != 0 || v.Winning != owner {
		t.Fatalf("%+v", v)
	}
}

func (h *exitHarness) lastTriggerSeq(task string) int64 {
	h.t.Helper()
	var seq int64
	if err := h.s.DB.QueryRow("SELECT max(seq) FROM task_triggers WHERE task_id=?", task).Scan(&seq); err != nil {
		h.t.Fatal(err)
	}
	return seq
}

// TestAuthorityOverridesParks (3.4): authority input (an owner comment, a manager's directive) overrides
// wait, ask, block and children parks and the no_exit and retries_exhausted escalations in its own
// transaction, and counts as progress; an inspect escalation and the owner's manual block keep the task.
func TestAuthorityOverridesParks(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	comment := func(task string) {
		t.Helper()
		if _, err := h.s.CommentTask(ctx, co, task, tasks.Comment{Text: "change of plan", RequestID: "comment-" + task}); err != nil {
			t.Fatal(err)
		}
	}
	runnable := func(task string) {
		t.Helper()
		if v := h.verdict(task); v.Reason != tasks.ReasonRunnable {
			t.Fatalf("%s: %+v", task, v)
		}
	}
	exitRun := func(task, name string, args map[string]any) {
		t.Helper()
		r := h.start(task)
		h.callOK(r, name, args)
		h.end(r, "succeeded", "accepted")
	}

	h.create("wait", "notes", "demo")
	exitRun("wait", "factory_exit_wait", map[string]any{"reason": "store review", "after": "6h", "deadline": h.fake.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)})
	if h.parkStatus("wait", tasks.ParkWait) != "active" {
		t.Fatal("no wait park")
	}
	comment("wait")
	if h.parkStatus("wait", tasks.ParkWait) != "overridden" {
		t.Fatal(h.parkStatus("wait", tasks.ParkWait))
	}
	runnable("wait")
	h.cancel("wait")

	h.create("ask", "notes", "demo")
	exitRun("ask", "factory_exit_ask", map[string]any{"question": "Which color?"})
	question := h.task("ask").WaitMessageID
	h.end(h.start(storage.ConversationID("notes", "manager")), "succeeded", "") // the question's delivery run
	comment("ask")
	if q, _ := h.s.Message(ctx, co, question); q.Status != "WITHDRAWN" || !strings.Contains(q.EscalationReason, "superseded by owner_comment from owner") || h.parkStatus("ask", tasks.ParkAsk) != "overridden" {
		t.Fatalf("%+v %s", q, h.parkStatus("ask", tasks.ParkAsk))
	}
	if got := h.task("ask"); got.Status != "READY" || got.WaitMessageID != "" {
		t.Fatalf("%+v", got)
	}
	runnable("ask")
	h.cancel("ask")

	for _, id := range []string{"dep", "dep2"} {
		if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "notes", Title: id, RequestID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "block", ProjectID: "notes", Title: "block", Assignee: "demo", Dependencies: []string{"dep2"}, RequestID: "block"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.UpdateTask(ctx, co, "block", tasks.Update{Version: h.task("block").Version, Dependencies: &[]string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.TaskAction(ctx, co, "block", tasks.Action{Version: h.task("block").Version, Action: "ready"}); err != nil {
		t.Fatal(err)
	}
	exitRun("block", "factory_exit_block", map[string]any{"on": []any{"dep"}, "reason": "needs dep"})
	if _, err := h.s.UpdateTask(ctx, co, "block", tasks.Update{Version: h.task("block").Version, Dependencies: &[]string{"dep", "dep2"}}); err != nil {
		t.Fatal(err) // the owner adds a dependency of its own
	}
	if got := h.task("block"); !slices.Equal(got.BlockedOn, []string{"dep"}) || h.verdict("block").Reason != tasks.ReasonDependencies {
		t.Fatalf("%+v", got)
	}
	if _, err := h.s.Directive(ctx, co, "block", "manager", "go ahead without dep", false, "go"); err != nil {
		t.Fatal(err)
	}
	if got := h.task("block"); !slices.Equal(got.Dependencies, []string{"dep2"}) || len(got.BlockedOn) != 0 || !strings.Contains(h.verdict("block").Detail, "dep2") {
		t.Fatalf("the block's dependency stayed or the owner's went: %+v %+v", got, h.verdict("block"))
	}
	h.cancel("block")

	h.create("children", "notes", "demo")
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "child", ProjectID: "notes", Title: "child", ParentTaskID: "children", RequestID: "child"}); err != nil {
		t.Fatal(err)
	}
	exitRun("children", "factory_exit_block", map[string]any{"on_children": true, "reason": "after the child"})
	comment("children")
	if got := h.task("children"); got.Status != "READY" || h.parkStatus("children", tasks.ParkChildren) != "overridden" {
		t.Fatalf("%+v", got)
	}
	runnable("children")
	h.cancel("children")

	h.create("noexit", "notes", "demo")
	r := h.start("noexit")
	h.end(r, "succeeded", "none")
	if h.parkStatus("noexit", tasks.ParkEscalation) != "active" {
		t.Fatal("no no_exit escalation")
	}
	comment("noexit")
	if got := h.task("noexit"); h.parkStatus("noexit", tasks.ParkEscalation) != "cleared" || got.AttemptsNoProgress != 0 {
		t.Fatalf("%+v", got)
	}
	runnable("noexit")
	h.cancel("noexit")

	h.create("retries", "notes", "demo")
	for range storage.RetryAttempts {
		r := h.start("retries")
		h.end(r, "failed", "")
		h.fake.Advance(time.Hour)
	}
	if open, _ := h.s.Escalations(ctx, co, true); !slices.ContainsFunc(open, func(e storage.Escalation) bool { return e.TaskID == "retries" && e.Reason == "retries_exhausted" }) {
		t.Fatalf("%+v", open)
	}
	if _, err := h.s.Directive(ctx, co, "retries", "manager", "try the other API", false, "retry-d"); err != nil {
		t.Fatal(err)
	}
	if got := h.task("retries"); got.AttemptsNoProgress != 0 || got.FailStreak != 0 || got.NotBefore != 0 || h.parkStatus("retries", tasks.ParkEscalation) != "cleared" {
		t.Fatalf("%+v", got)
	}
	runnable("retries")
	h.cancel("retries")

	// Not overridden: inspect needs its answer; the owner's manual block is lifted by the owner.
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "inspect", ProjectID: "notes", Title: "publish", Assignee: "demo", SideEffects: true, Ready: true, RequestID: "inspect"}); err != nil {
		t.Fatal(err)
	}
	r = h.start("inspect")
	h.end(r, "failed", "")
	comment("inspect")
	if h.parkStatus("inspect", tasks.ParkEscalation) != "active" || h.verdict("inspect").Reason != tasks.ReasonParked+tasks.ParkEscalation {
		t.Fatalf("inspect was overridden: %+v", h.verdict("inspect"))
	}
	h.create("manual", "notes", "demo")
	if _, err := h.s.TaskAction(ctx, co, "manual", tasks.Action{Version: h.task("manual").Version, Action: "block", Reason: "wait for legal"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.Directive(ctx, co, "manual", "manager", "start anyway", false, "manual-d"); err != nil {
		t.Fatal(err)
	}
	if h.parkStatus("manual", tasks.ParkBlock) != "active" || h.task("manual").Status != "BLOCKED" {
		t.Fatalf("%+v", h.task("manual"))
	}
}

// TestUrgentStopOnControlSubjectAndHeartbeat (3.5): an urgent directive to a run of a v2 node queues run.stop
// on that node's control subject and stops it through the heartbeat answer as well (the guarantee when the
// control message is lost); a v1 run has no control subject and stops through its heartbeat.
func TestUrgentStopOnControlSubjectAndHeartbeat(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	h.create("v1", "code", "calorie-ios")
	r := h.start("v1") // claimed by a v1 worker before any node registered
	if _, err := h.s.RegisterNode(ctx, co, registration("n1", 1)); err != nil {
		t.Fatal(err)
	}
	h.create("urgent", "notes", "demo")
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	run := h.task("urgent").CurrentRunID
	if run == "" {
		t.Fatalf("not dispatched: %+v", h.verdict("urgent"))
	}
	if _, err := h.s.ClaimV2(ctx, co, run, storage.ClaimRequest{NodeID: "n1", Epoch: 1, ClaimID: "1-0-1"}); err != nil {
		t.Fatal(err)
	}
	if w := h.do(h.c.Token, "POST", "/tasks/urgent/directives", map[string]any{"text": "stop: the API changed", "urgent": true, "request_id": "u"}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var subject, body string
	if err := h.s.DB.QueryRow("SELECT subject,body FROM outbox WHERE subject LIKE 'factory.default.nodes.%'").Scan(&subject, &body); err != nil {
		t.Fatal(err)
	}
	var e protocol.Envelope
	var stop protocol.RunStop
	if subject != "factory.default.nodes.n1.control" || json.Unmarshal([]byte(body), &e) != nil || e.Type != "run.stop" || json.Unmarshal(e.Payload, &stop) != nil || stop.RunID != run || stop.Reason != "revoked" {
		t.Fatal(subject, body)
	}
	answer, err := h.s.NodeHeartbeat(ctx, co, "n1", storage.NodeHeartbeat{Epoch: 1, Inventory: []storage.InventoryRun{{RunID: run, State: "claimed"}}})
	if err != nil || !slices.Contains(answer.Stop, storage.Stop{RunID: run, Reason: "revoked"}) {
		t.Fatalf("%+v %v", answer, err)
	}

	if _, err = h.s.Directive(ctx, co, "v1", "owner", "stop", true, "v1"); err != nil {
		t.Fatal(err)
	}
	var controls int
	_ = h.s.DB.QueryRow("SELECT count(*) FROM outbox WHERE subject LIKE 'factory.default.nodes.%'").Scan(&controls)
	if cancelled, err := h.s.Heartbeat(ctx, co, r.id, r.lease); err != nil || !cancelled || controls != 1 {
		t.Fatal(cancelled, err, controls)
	}
}

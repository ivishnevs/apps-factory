package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

type exitHarness struct {
	t    *testing.T
	c    *Core
	s    *storage.Store
	fake *clock.Fake
}

func newExitHarness(t *testing.T) *exitHarness {
	t.Helper()
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	catalog, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	reviewer := catalog.Agents["demo"]
	reviewer.Metadata.Name = "reviewer"
	catalog.Agents["reviewer"] = reviewer
	c := &Core{Store: s, Agents: catalog.Agents, Skills: catalog.Skills, Token: "owner-secret", ExplicitExits: true}
	c.Handler()
	ctx := context.Background()
	for id, repo := range map[string]string{"code": "https://example.com/app.git", "notes": ""} {
		if _, err = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: id, Name: id, Workspace: "/tmp/exits-" + id, RepoURL: repo}); err != nil {
			t.Fatal(err)
		}
	}
	return &exitHarness{t: t, c: c, s: s, fake: fake}
}

func (h *exitHarness) do(token, method, path string, body any, header map[string]string) *httptest.ResponseRecorder {
	h.t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+token)
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.c.Handler().ServeHTTP(w, r)
	return w
}

type exitRun struct{ id, lease, token string }

// start schedules and claims the task's next run and returns its credential.
func (h *exitHarness) start(task string) exitRun {
	h.t.Helper()
	ctx := context.Background()
	if err := h.c.Schedule(ctx); err != nil {
		h.t.Fatal(err)
	}
	tk, _ := h.s.Task(ctx, storage.DefaultCompany, task)
	lease, err := h.s.Claim(ctx, storage.DefaultCompany, tk.CurrentRunID)
	if err != nil {
		h.t.Fatalf("claim %s (%s): %v", tk.CurrentRunID, tk.Status, err)
	}
	w := h.do(h.c.Token, "POST", "/runs/"+tk.CurrentRunID+"/credential", map[string]string{"lease": lease}, nil)
	var cred struct{ Token string }
	if json.Unmarshal(w.Body.Bytes(), &cred) != nil || cred.Token == "" {
		h.t.Fatal(w.Code, w.Body.String())
	}
	return exitRun{tk.CurrentRunID, lease, cred.Token}
}

func (h *exitHarness) end(r exitRun, status, exitState string) {
	h.t.Helper()
	if err := h.s.Apply(context.Background(), storage.DefaultCompany, protocol.Wrap("agent.ended", r.id, "", protocol.Event{RunID: r.id, Lease: r.lease, Status: status, ExitState: exitState})); err != nil {
		h.t.Fatal(err)
	}
}

func (h *exitHarness) task(id string) tasks.Task {
	h.t.Helper()
	v, err := h.s.Task(context.Background(), storage.DefaultCompany, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

func (h *exitHarness) create(id, project, assignee string) {
	h.t.Helper()
	if _, err := h.s.CreateTask(context.Background(), storage.DefaultCompany, tasks.Create{ID: id, ProjectID: project, Title: id, Assignee: assignee, Ready: true, RequestID: "create-" + id}); err != nil {
		h.t.Fatal(err)
	}
}

func clean(head string, pr *storage.PRFacts) *storage.ExitFacts {
	return &storage.ExitFacts{Head: head, Branch: "af/t", DefaultBranch: "main", AheadOfDefault: 2, Pushed: true, PR: pr, Scan: &storage.ScanFacts{OK: true}}
}

func refusalOf(t *testing.T, w *httptest.ResponseRecorder) storage.ExitRefusal {
	t.Helper()
	var r storage.ExitRefusal
	_ = json.Unmarshal(w.Body.Bytes(), &r)
	return r
}

// TestExitSubmitReviewDone: submit needs facts, a clean tree and every directive seen; the named reviewer
// reviews in its own run and approves; done needs the merged pull request; every exit is idempotent.
func TestExitSubmitReviewDone(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("t", "code", "demo")
	r := h.start("t")

	w := h.do(r.token, "GET", "/agent/tools", nil, nil)
	if body := w.Body.String(); !strings.Contains(body, "factory_exit_submit") || strings.Contains(body, `"factory_task_submit"`) || strings.Contains(body, "factory_task_direct") {
		t.Fatal(body)
	}
	var brief struct {
		AckSeq int64 `json:"ack_seq"`
	}
	_ = json.Unmarshal(h.do(r.token, "GET", "/agent/brief", nil, nil).Body.Bytes(), &brief)

	submit := map[string]any{"summary": "login screen", "checks": []string{"go test ./..."}, "reviewer": "reviewer"}
	// Through /agent/call (an adapter without facts) a repository task is refused.
	w = h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_exit_submit", Arguments: submit}, nil)
	if w.Code != 409 || refusalOf(t, w).Reason != "facts_required" {
		t.Fatal(w.Code, w.Body.String())
	}
	exit := func(path string, kind string, args map[string]any, ack int64, facts *storage.ExitFacts) *httptest.ResponseRecorder {
		return h.do(r.token, "POST", path, map[string]any{"kind": kind, "arguments": args, "ack_seq": ack, "facts": facts}, nil)
	}
	dirty := clean("h1", nil)
	dirty.Dirty = true
	if w = exit("/agent/exit", "submit", submit, brief.AckSeq, dirty); w.Code != 409 || refusalOf(t, w).Reason != "dirty_tree" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = exit("/agent/exit/preflight", "submit", submit, brief.AckSeq, clean("h1", nil)); w.Code != 200 || h.task("t").Status != "IN_PROGRESS" {
		t.Fatal("preflight changed the task", w.Code, w.Body.String())
	}
	// The owner gives a directive: the old ack_seq cannot exit; a tool result shows the directive.
	if w = h.do(h.c.Token, "POST", "/tasks/t/directives", map[string]any{"text": "use the new logo", "request_id": "d1"}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = exit("/agent/exit", "submit", submit, brief.AckSeq, clean("h1", nil))
	ref := refusalOf(t, w)
	if w.Code != 409 || ref.Reason != "unseen_input" || len(ref.Unseen) != 1 || ref.Unseen[0].Text != "use the new logo" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_project_get", Arguments: map[string]any{}}, map[string]string{ackHeader: strconv.FormatInt(brief.AckSeq, 10)})
	var wrapped struct {
		Result         map[string]any           `json:"result"`
		AuthorityInput []storage.AuthorityInput `json:"authority_input"`
	}
	if json.Unmarshal(w.Body.Bytes(), &wrapped) != nil || len(wrapped.AuthorityInput) != 1 || wrapped.Result["id"] != "code" || w.Header().Get(seenHeader) != strconv.FormatInt(ref.Unseen[0].Seq, 10) {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
	seen := ref.Unseen[0].Seq
	if w = exit("/agent/exit", "submit", submit, seen, clean("h1", nil)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	got := h.task("t")
	if got.Status != "IN_REVIEW" || got.Assignee != "reviewer" || got.Implementer != "demo" || got.Submission == nil || got.Submission.Commit != "h1" {
		t.Fatalf("%+v", got)
	}
	if again := exit("/agent/exit", "submit", submit, seen, clean("h1", nil)); again.Code != 200 || again.Body.String() != w.Body.String() {
		t.Fatal("retried exit differs", again.Body.String())
	}
	if other := exit("/agent/exit", "done", map[string]any{"summary": "x"}, seen, clean("h1", nil)); other.Code != 409 || refusalOf(t, other).Reason != "exit_already_accepted" {
		t.Fatal(other.Code, other.Body.String())
	}
	h.end(r, "COMPLETED", "accepted")
	if run, _ := h.s.Get(ctx, storage.DefaultCompany, r.id); run.Outcome != "succeeded" || h.task("t").Status != "IN_REVIEW" {
		t.Fatalf("%+v %+v", run, h.task("t"))
	}

	// The reviewer's run (the task stays IN_REVIEW) approves; the approval is bound to the PR head.
	rv := h.start("t")
	if run, _ := h.s.Get(ctx, storage.DefaultCompany, rv.id); run.Spec.Agent != "reviewer" || !strings.Contains(run.Spec.Prompt, "You review this task, submitted by demo") || h.task("t").Status != "IN_REVIEW" {
		t.Fatalf("%+v", run.Spec)
	}
	pr := &storage.PRFacts{Number: 7, State: "open", HeadSHA: "h1"}
	_ = json.Unmarshal(h.do(rv.token, "GET", "/agent/brief", nil, nil).Body.Bytes(), &brief)
	w = h.do(rv.token, "POST", "/agent/exit", map[string]any{"kind": "handoff", "arguments": map[string]any{"to": "demo", "note": "looks good", "verdict": "approved"}, "ack_seq": brief.AckSeq, "facts": clean("h1", pr)}, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if got = h.task("t"); got.Status != "READY" || got.Assignee != "demo" || got.Reviewer != "" {
		t.Fatalf("%+v", got)
	}
	h.end(rv, "COMPLETED", "accepted")

	// done is refused until the pull request is merged.
	r = h.start("t")
	_ = json.Unmarshal(h.do(r.token, "GET", "/agent/brief", nil, nil).Body.Bytes(), &brief)
	done := map[string]any{"summary": "merged"}
	if w = exit("/agent/exit", "done", done, brief.AckSeq, clean("h1", pr)); w.Code != 409 || refusalOf(t, w).Reason != "pr_not_merged" {
		t.Fatal(w.Code, w.Body.String())
	}
	// The owner may give up the branch: then done needs no merged PR.
	if err := h.s.AbandonBranch(ctx, storage.DefaultCompany, "t", "owner"); err != nil {
		t.Fatal(err)
	}
	// The decision is new input the agent must see first; then done passes without a merged PR.
	w = exit("/agent/exit/preflight", "done", done, brief.AckSeq, clean("h1", pr))
	unseen := refusalOf(t, w).Unseen
	if w.Code != 409 || len(unseen) != 1 || unseen[0].Text != "abandon_branch" {
		t.Fatal(w.Code, w.Body.String())
	}
	brief.AckSeq = unseen[0].Seq
	if w = exit("/agent/exit/preflight", "done", done, brief.AckSeq, clean("h1", pr)); w.Code != 200 {
		t.Fatal("abandon_branch did not lift the merge rule", w.Code, w.Body.String())
	}
	merged := &storage.PRFacts{Number: 7, State: "merged", HeadSHA: "h1"}
	if w = exit("/agent/exit", "done", done, brief.AckSeq, clean("h1", merged)); w.Code != 200 || h.task("t").Status != "DONE" {
		t.Fatal(w.Code, w.Body.String(), h.task("t").Status)
	}
}

// TestNoExitLostAndGone: a turn without an exit escalates no_exit (accept leaves the task idle); an exit
// that was not delivered is lost and retried; a stopped run is told 410.
func TestNoExitLostAndGone(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("n", "notes", "demo")
	r := h.start("n")
	h.end(r, "COMPLETED", "none")
	open, _ := h.s.Escalations(ctx, storage.DefaultCompany, true)
	if run, _ := h.s.Get(ctx, storage.DefaultCompany, r.id); run.Outcome != "no_exit" || len(open) != 1 || open[0].Reason != "no_exit" || h.task("n").Status != "IN_PROGRESS" {
		t.Fatalf("%+v %+v %+v", run, open, h.task("n"))
	}
	q, _ := h.s.Message(ctx, storage.DefaultCompany, open[0].MessageID)
	if _, err := h.s.ReplyMessage(ctx, storage.DefaultCompany, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: "fine", Answers: map[string]any{"answer": "accept"}, RequestID: "a"}); err != nil {
		t.Fatal(err)
	}
	snap, _ := h.s.TaskSnapshot(ctx, storage.DefaultCompany, "n")
	if snap.Task.Status != "READY" || snap.WakeSeq != snap.HandledSeq {
		t.Fatalf("accept left the task runnable: %+v %d/%d", snap.Task, snap.WakeSeq, snap.HandledSeq)
	}

	h.create("l", "notes", "demo")
	r = h.start("l")
	h.end(r, "COMPLETED", "pending")
	if run, _ := h.s.Get(ctx, storage.DefaultCompany, r.id); run.Status != "FAILED" || run.Outcome != "lost" || h.task("l").Status != "READY" || h.task("l").NotBefore == 0 {
		t.Fatalf("%+v %+v", run, h.task("l"))
	}

	h.create("g", "notes", "reviewer")
	r = h.start("g")
	if err := h.s.Cancel(ctx, storage.DefaultCompany, r.id); err != nil {
		t.Fatal(err)
	}
	w := h.do(r.token, "POST", "/agent/exit", map[string]any{"kind": "done", "arguments": map[string]any{"summary": "x"}, "ack_seq": 0}, nil)
	if w.Code != 410 {
		t.Fatal(w.Code, w.Body.String())
	}
}

// TestAskBlockWaitAndUrgentDirective: ask blocks on the owner's answer, block waits on tasks (a cycle falls
// back to ask), wait resumes at its time, and an urgent directive stops the run without cancelling the task.
func TestAskBlockWaitAndUrgentDirective(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	call := func(r exitRun, name string, args map[string]any) map[string]any {
		t.Helper()
		w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: name, Arguments: args}, nil)
		if w.Code != 200 {
			t.Fatal(name, w.Code, w.Body.String())
		}
		var v map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &v)
		return v
	}
	h.create("ask", "notes", "demo")
	r := h.start("ask")
	call(r, "factory_exit_ask", map[string]any{"question": "Which database?", "options": []any{"sqlite", "postgres"}})
	h.end(r, "COMPLETED", "accepted")
	got := h.task("ask")
	if got.Status != "BLOCKED" || got.WaitMessageID == "" {
		t.Fatalf("%+v", got)
	}
	q, _ := h.s.Message(ctx, storage.DefaultCompany, got.WaitMessageID)
	if _, err := h.s.ReplyMessage(ctx, storage.DefaultCompany, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: "sqlite", Answers: map[string]any{"answer": "sqlite"}, RequestID: "ans"}); err != nil {
		t.Fatal(err)
	}
	if got = h.task("ask"); got.Status != "READY" {
		t.Fatalf("%+v", got)
	}
	if _, err := h.s.TaskAction(ctx, storage.DefaultCompany, "ask", tasks.Action{Version: got.Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}

	if _, err := h.s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ID: "base", ProjectID: "notes", Title: "base", RequestID: "create-base"}); err != nil {
		t.Fatal(err)
	}
	h.create("blk", "notes", "reviewer")
	r = h.start("blk")
	v := call(r, "factory_exit_block", map[string]any{"on": []any{"base"}, "reason": "needs the base"})
	if got = h.task("blk"); got.Status != "READY" || len(got.Dependencies) != 1 || v["note"] != nil {
		t.Fatalf("%+v %v", got, v)
	}
	h.end(r, "COMPLETED", "accepted")

	h.create("cyc", "notes", "demo")
	if _, err := h.s.UpdateTask(ctx, storage.DefaultCompany, "base", tasks.Update{Version: h.task("base").Version, Dependencies: &[]string{"cyc"}}); err != nil {
		t.Fatal(err)
	}
	h.create("w", "notes", "qa-agent")
	r = h.start("cyc")
	v = call(r, "factory_exit_block", map[string]any{"on": []any{"blk"}, "reason": "needs blk"})
	if note, _ := v["note"].(string); !strings.Contains(note, "cycle") || h.task("cyc").WaitMessageID == "" {
		t.Fatalf("%v %+v", v, h.task("cyc"))
	}
	h.end(r, "COMPLETED", "accepted")

	// wait: READY again only at its time, with a wake trigger.
	if _, ok := h.c.Agents["qa-agent"]; !ok {
		a := h.c.Agents["demo"]
		a.Metadata.Name = "qa-agent"
		h.c.Agents["qa-agent"] = a
	}
	r = h.start("w")
	wake := h.fake.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	call(r, "factory_exit_wait", map[string]any{"reason": "store review", "wake_at": wake})
	h.end(r, "COMPLETED", "accepted")
	if got = h.task("w"); got.Status != "READY" || got.NotBefore != h.fake.Now().Add(time.Hour).Unix() {
		t.Fatalf("%+v", got)
	}
	h.fake.Advance(time.Hour)
	if err := h.s.ExpireWaits(ctx, storage.DefaultCompany); err != nil {
		t.Fatal(err)
	}
	snap, _ := h.s.TaskSnapshot(ctx, storage.DefaultCompany, "w")
	if len(snap.Pending) == 0 || snap.Pending[len(snap.Pending)-1].Kind != "wake" {
		t.Fatalf("%+v", snap.Pending)
	}

	// Urgent directive: the run is stopped, the task returns to READY and the next run starts with it.
	r = h.start("w")
	if w := h.do(h.c.Token, "POST", "/tasks/w/directives", map[string]any{"text": "stop and fix the crash first", "urgent": true, "request_id": "u1"}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if run, _ := h.s.Get(ctx, storage.DefaultCompany, r.id); run.Status != "CANCELLING" {
		t.Fatal(run.Status)
	}
	h.end(r, "CANCELLED", "none")
	if got = h.task("w"); got.Status != "READY" {
		t.Fatalf("%+v", got)
	}
	next := h.start("w")
	if run, _ := h.s.Get(ctx, storage.DefaultCompany, next.id); !strings.Contains(run.Spec.Prompt, "Directive from owner (directive): stop and fix the crash first") || !strings.Contains(run.Spec.Prompt, "exactly one factory_exit_* call") {
		t.Fatal(run.Spec.Prompt)
	}
}

// TestManagerDirective: managers get factory_task_direct; their directive is authority input on the task.
func TestManagerDirective(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("target", "notes", "demo")
	h.create("plan", "code", "calorie-manager")
	m := h.start("plan")
	if body := h.do(m.token, "GET", "/agent/tools", nil, nil).Body.String(); !strings.Contains(body, "factory_task_direct") {
		t.Fatal("manager without the directive tool")
	}
	// Only tasks of the manager's run project; then the same project.
	w := h.do(m.token, "POST", "/agent/call", factorytools.Call{Name: "factory_task_direct", Arguments: map[string]any{"task_id": "target", "text": "ship the login first", "request_id": "d"}}, nil)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := h.s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ID: "feature", ProjectID: "code", Title: "feature", RequestID: "create-feature"}); err != nil {
		t.Fatal(err)
	}
	w = h.do(m.token, "POST", "/agent/call", factorytools.Call{Name: "factory_task_direct", Arguments: map[string]any{"task_id": "feature", "text": "ship the login first", "request_id": "d"}}, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	input, _ := h.s.AuthorityInputSince(ctx, storage.DefaultCompany, "feature", 0)
	if last := input[len(input)-1]; last.Kind != "directive" || last.Author != "calorie-manager" || last.Rank != 1 {
		t.Fatalf("%+v", input)
	}
	d := h.start("target") // a manager's directive does not lift an owner's manual block; this task was ready
	if w = h.do(d.token, "POST", "/agent/call", factorytools.Call{Name: "factory_task_direct", Arguments: map[string]any{"task_id": "plan", "text": "x", "request_id": "e"}}, nil); w.Code != 403 {
		t.Fatal("a developer could give directives", w.Code, w.Body.String())
	}
}

// TestReviewAfterConflictsRequired: with the policy required, done is refused when the PR head moved after
// the approval, and accepted for the approved head.
func TestReviewAfterConflictsRequired(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	if _, err := h.s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "strict", Name: "strict", Workspace: "/tmp/exits-strict", RepoURL: "https://example.com/s.git", ReviewAfterConflicts: "required"}); err != nil {
		t.Fatal(err)
	}
	h.create("s", "strict", "demo")
	r := h.start("s")
	var brief struct {
		AckSeq int64 `json:"ack_seq"`
	}
	_ = json.Unmarshal(h.do(r.token, "GET", "/agent/brief", nil, nil).Body.Bytes(), &brief)
	exit := func(run exitRun, kind string, args map[string]any, facts *storage.ExitFacts) *httptest.ResponseRecorder {
		var b struct {
			AckSeq int64 `json:"ack_seq"`
		}
		_ = json.Unmarshal(h.do(run.token, "GET", "/agent/brief", nil, nil).Body.Bytes(), &b)
		return h.do(run.token, "POST", "/agent/exit", map[string]any{"kind": kind, "arguments": args, "ack_seq": b.AckSeq, "facts": facts}, nil)
	}
	open := &storage.PRFacts{Number: 3, State: "open", HeadSHA: "h1"}
	if w := exit(r, "submit", map[string]any{"summary": "s", "reviewer": "reviewer"}, clean("h1", open)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "COMPLETED", "accepted")
	rv := h.start("s")
	if w := exit(rv, "handoff", map[string]any{"to": "demo", "note": "ok", "verdict": "approved"}, clean("h1", open)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(rv, "COMPLETED", "accepted")
	r = h.start("s")
	moved := &storage.PRFacts{Number: 3, State: "merged", HeadSHA: "h2"}
	if w := exit(r, "done", map[string]any{"summary": "x"}, clean("h2", moved)); w.Code != 409 || refusalOf(t, w).Reason != "needs_approval" {
		t.Fatal(w.Code, w.Body.String())
	}
	approvedHead := &storage.PRFacts{Number: 3, State: "merged", HeadSHA: "h1"}
	if w := exit(r, "done", map[string]any{"summary": "x"}, clean("h1", approvedHead)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

// TestCommentsInBrief: a non-authority comment wakes nothing but reaches the next brief; a manager's wakes.
func TestCommentsInBrief(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	if _, err := h.s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ID: "c", ProjectID: "notes", Title: "c", Assignee: "demo", RequestID: "c"}); err != nil {
		t.Fatal(err)
	}
	for actor, code := range map[string]int{"calorie-qa": 200, "calorie-manager": 200} {
		if w := h.do(h.c.Token, "POST", "/tasks/c/comments", map[string]any{"text": "note from " + actor, "actor": actor, "request_id": actor}, nil); w.Code != code {
			t.Fatal(actor, w.Code, w.Body.String())
		}
	}
	snap, _ := h.s.TaskSnapshot(ctx, storage.DefaultCompany, "c")
	if len(snap.Pending) != 1 || snap.Pending[0].Actor != "calorie-manager" || !snap.Pending[0].Authority {
		t.Fatalf("%+v", snap.Pending)
	}
	task := h.task("c")
	if _, err := h.s.TaskAction(ctx, storage.DefaultCompany, "c", tasks.Action{Version: task.Version, Action: "ready"}); err != nil {
		t.Fatal(err)
	}
	r := h.start("c")
	run, _ := h.s.Get(ctx, storage.DefaultCompany, r.id)
	for _, want := range []string{"Comment by calorie-qa: note from calorie-qa", "Since your last run:", "owner_comment by calorie-manager (authority): note from calorie-manager"} {
		if !strings.Contains(run.Spec.Prompt, want) {
			t.Fatal(want, "\n", run.Spec.Prompt)
		}
	}
}

// TestProgressNeedsAnExitThatMoves: a handoff to itself is not progress (attempts stay), a submit is.
func TestProgressNeedsAnExitThatMoves(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("loop", "notes", "demo")
	for range 2 {
		r := h.start("loop")
		if err := h.s.Apply(ctx, storage.DefaultCompany, protocol.Wrap("agent.ended", r.id, "", protocol.Event{RunID: r.id, Lease: r.lease, Status: "FAILED", Outcome: "failed"})); err != nil {
			t.Fatal(err)
		}
		h.fake.Advance(time.Hour)
	}
	if got := h.task("loop"); got.AttemptsNoProgress != 2 {
		t.Fatal(got.AttemptsNoProgress)
	}
	exitRun := func(name string, args map[string]any) {
		t.Helper()
		r := h.start("loop")
		if w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: name, Arguments: args}, nil); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		h.end(r, "COMPLETED", "accepted")
		h.fake.Advance(time.Hour)
	}
	exitRun("factory_exit_handoff", map[string]any{"to": "demo", "note": "later"})
	if got := h.task("loop"); got.AttemptsNoProgress != 2 || got.FailStreak != 0 {
		t.Fatalf("handoff to itself counted as progress: %+v", got)
	}
	exitRun("factory_exit_submit", map[string]any{"summary": "done"})
	if got := h.task("loop"); got.AttemptsNoProgress != 0 {
		t.Fatalf("submit is progress: %+v", got)
	}
}

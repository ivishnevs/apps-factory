package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// peopleHarness adds two people to the exit harness's company: ilya (owner) and bob (member), each with a signed-in
// browser.
func peopleHarness(t *testing.T) (*exitHarness, map[string]*browser) {
	t.Helper()
	h := newExitHarness(t)
	ctx := context.Background()
	people := map[string]*browser{}
	for user, role := range map[string]string{"ilya": storage.RoleOwner, "bob": storage.RoleMember} {
		if err := h.s.CreateUser(ctx, storage.DefaultAccount, user, user+"@example.com"); err != nil {
			t.Fatal(err)
		}
		if err := h.s.AddMember(ctx, storage.DefaultCompany, user, role); err != nil {
			t.Fatal(err)
		}
		id, _, err := h.s.CreateSession(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		people[user] = &browser{t: t, h: h.c.Handler(), cookies: map[string]string{SessionCookie: id, CSRFCookie: "csrf"}, company: storage.DefaultCompany}
	}
	return h, people
}

func (h *exitHarness) owner(method, path string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.do("owner-secret", method, path, body, nil)
}

func taskOf(t *testing.T, w *httptest.ResponseRecorder) tasks.Task {
	t.Helper()
	var v tasks.Task
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	return v
}

// TestPersonTaskReleasesDependents replays people-assignees 5.1 with fakes: the owner takes a task two agent tasks
// depend on, starts it, submits it with pull request #200; four days of waiting open no stalled escalation and send
// one reminder; after the approval the owner confirms the merge, the task is DONE and both dependents become
// schedulable without any escalation or manual action.
func TestPersonTaskReleasesDependents(t *testing.T) {
	h, people := peopleHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	if w := h.owner("POST", "/tasks", map[string]any{"id": "p", "project_id": "code", "title": "Main menu layout", "assignee": "demo", "review_by": "owner", "ready": true, "request_id": "p"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, id := range []string{"a", "b"} {
		if w := h.owner("POST", "/tasks", map[string]any{"id": id, "project_id": "code", "title": "After menu " + id, "assignee": "demo", "review_by": "reviewer", "ready": true, "dependencies": []string{"p"}, "request_id": id}); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// Unknown people are refused; the owner takes the task on the board (reassign; no live run).
	if w := h.owner("POST", "/tasks/p/reassign", map[string]any{"version": h.task("p").Version, "assignee": "member:nobody", "request_id": "x"}); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	p := taskOf(t, people["ilya"].do("POST", "/tasks/p/reassign", map[string]any{"version": h.task("p").Version, "assignee": "member:ilya", "request_id": "take"}))
	if p.Status != "READY" || p.Assignee != "member:ilya" {
		t.Fatalf("%+v", p)
	}
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if p = h.task("p"); p.CurrentRunID != "" {
		t.Fatalf("a run was dispatched for a person's task: %+v", p)
	}
	if v := h.why("p"); v.Reason != tasks.ReasonIdle || v.Detail != "assigned to ilya" || v.Clearer.Who != "member:ilya" {
		t.Fatalf("%+v", v)
	}
	if v := h.why("a"); v.Reason != tasks.ReasonDependencies || !strings.Contains(v.Detail, "waits for p, with ilya") {
		t.Fatalf("%+v", v)
	}
	// "Mine": the person's list.
	var mine []tasks.Task
	if w := people["ilya"].do("GET", "/tasks?assignee=me", nil); json.Unmarshal(w.Body.Bytes(), &mine) != nil || len(mine) != 1 || mine[0].ID != "p" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := h.owner("GET", "/tasks?assignee=me", nil); json.Unmarshal(w.Body.Bytes(), &mine) != nil || len(mine) != 1 {
		t.Fatal("the owner token's me is the company's owners:", w.Body.String())
	}

	// Another member does not move it; the person does.
	if w := people["bob"].do("POST", "/tasks/p/actions", map[string]any{"version": p.Version, "action": "start", "request_id": "bob"}); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	p = taskOf(t, people["ilya"].do("POST", "/tasks/p/actions", map[string]any{"version": p.Version, "action": "start", "request_id": "start"}))
	if p.Status != "IN_PROGRESS" {
		t.Fatalf("%+v", p)
	}
	if w := people["ilya"].do("POST", "/tasks/p/actions", map[string]any{"version": p.Version, "action": "submit", "summary": "menu laid out", "request_id": "s0"}); w.Code != 400 || !strings.Contains(w.Body.String(), "pull request") {
		t.Fatal("a repository project needs the pull request:", w.Code, w.Body.String())
	}
	p = taskOf(t, people["ilya"].do("POST", "/tasks/p/actions", map[string]any{"version": p.Version, "action": "submit", "summary": "menu laid out", "pull_request": "https://github.com/o/r/pull/200", "request_id": "s1"}))
	if p.Status != "IN_REVIEW" || p.Assignee != "member:ilya" || p.Implementer != "member:ilya" || p.Submission.PullRequest.Number != 200 {
		t.Fatalf("%+v", p)
	}
	if w := people["ilya"].do("POST", "/tasks/p/actions", map[string]any{"version": p.Version, "action": "done", "merged": true, "request_id": "d0"}); w.Code != 409 {
		t.Fatal("done before the approval:", w.Code, w.Body.String())
	}

	// Four days of waiting: no stalled escalation, one reminder (once per episode).
	for range 2 {
		h.fake.Advance(2 * 24 * time.Hour)
		if err := h.c.sweepStalls(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.c.sweepStalls(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := h.s.Escalations(ctx, co, true)
	for _, e := range all {
		if e.Reason == "stalled" {
			t.Fatalf("a wait on a person's task escalated: %+v", e)
		}
	}
	if got := ownerNotes(t, h, "Reminder for ilya"); len(got) != 1 || !strings.Contains(got[0], "a (") || !strings.Contains(got[0], "b (") {
		t.Fatal(got)
	}

	// The owner approves, the person confirms the merge, the dependents are released.
	p = taskOf(t, h.owner("POST", "/tasks/p/review", map[string]any{"version": p.Version, "accept": true, "request_id": "ok"}))
	if p.Status != "READY" || p.Assignee != "member:ilya" {
		t.Fatalf("%+v", p)
	}
	if w := people["ilya"].do("POST", "/tasks/p/actions", map[string]any{"version": p.Version, "action": "done", "request_id": "d1"}); w.Code != 400 || !strings.Contains(w.Body.String(), "#200") {
		t.Fatal("done needs the merge confirmed:", w.Code, w.Body.String())
	}
	p = taskOf(t, people["ilya"].do("POST", "/tasks/p/actions", map[string]any{"version": p.Version, "action": "done", "merged": true, "request_id": "d2"}))
	if p.Status != "DONE" {
		t.Fatalf("%+v", p)
	}
	for _, id := range []string{"a", "b"} {
		if v := h.why(id); v.Reason == tasks.ReasonDependencies || v.Reason == tasks.ReasonIdle || strings.HasPrefix(v.Reason, tasks.ReasonParked) {
			t.Fatalf("%s not released: %+v", id, v)
		}
	}
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if a := h.task("a"); a.CurrentRunID == "" {
		t.Fatalf("dependent not scheduled: %+v", a)
	}
	all, _ = h.s.Escalations(ctx, co, true)
	if len(all) != 0 {
		t.Fatalf("%+v", all)
	}
}

// ownerNotes are the bodies of the owner's notifications containing text.
func ownerNotes(t *testing.T, h *exitHarness, text string) []string {
	t.Helper()
	rows, err := h.s.DB.QueryContext(context.Background(), "SELECT body->>'body' FROM messages WHERE company_id=? AND recipient='owner' AND message_type='notification' ORDER BY seq", storage.DefaultCompany)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(body, text) {
			out = append(out, body)
		}
	}
	return out
}

// TestPersonTaskAgentReviewAndGiveBack: an agent reviewer gets a review run for a person's submission and its
// approval lets the person finish; changes requested return it to the person; "your task is ready" tells the person
// when the last dependency is done; giving the task back to an agent schedules it as usual.
func TestPersonTaskAgentReviewAndGiveBack(t *testing.T) {
	h, people := peopleHarness(t)
	ctx := context.Background()
	h.create("first", "notes", "demo")
	if w := h.owner("POST", "/tasks", map[string]any{"id": "p", "project_id": "notes", "title": "Write the copy", "assignee": "member:bob", "review_by": "reviewer", "ready": true, "dependencies": []string{"first"}, "request_id": "p"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := people["bob"].do("POST", "/tasks/p/actions", map[string]any{"version": h.task("p").Version, "action": "start", "request_id": "early"}); w.Code != 409 || !strings.Contains(w.Body.String(), "first") {
		t.Fatal("started before its dependency:", w.Code, w.Body.String())
	}
	r := h.start("first")
	if w := h.exitAt(r, "done", map[string]any{"summary": "done"}, h.ack(r), nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "succeeded", "accepted")
	if got := ownerNotes(t, h, "Your task is ready, bob"); len(got) != 1 {
		t.Fatal(got)
	}
	p := taskOf(t, people["bob"].do("POST", "/tasks/p/actions", map[string]any{"version": h.task("p").Version, "action": "start", "request_id": "start"}))
	p = taskOf(t, people["bob"].do("POST", "/tasks/p/actions", map[string]any{"version": p.Version, "action": "submit", "summary": "copy written", "request_id": "s1"}))
	if p.Status != "IN_REVIEW" || p.Assignee != "reviewer" || p.Reviewer != "reviewer" {
		t.Fatalf("%+v", p)
	}
	// The reviewer's run asks for changes: the task returns to the person.
	rv := h.start("p")
	if w := h.exitAt(rv, "handoff", map[string]any{"verdict": "changes_requested", "feedback": "shorter", "note": "see feedback"}, h.ack(rv), nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(rv, "succeeded", "accepted")
	if p = h.task("p"); p.Assignee != "member:bob" || p.Status != "READY" || p.ReviewFeedback != "shorter" {
		t.Fatalf("%+v", p)
	}
	if v := h.why("p"); !strings.Contains(v.Detail, "changes requested: shorter") {
		t.Fatalf("%+v", v)
	}
	if w := people["bob"].do("POST", "/tasks/p/actions", map[string]any{"version": p.Version, "action": "done", "request_id": "d0"}); w.Code != 409 {
		t.Fatal("done after changes requested:", w.Code, w.Body.String())
	}
	p = taskOf(t, people["bob"].do("POST", "/tasks/p/actions", map[string]any{"version": p.Version, "action": "submit", "summary": "shorter copy", "request_id": "s2"}))
	h.fake.Advance(time.Minute)
	rv = h.start("p")
	if w := h.exitAt(rv, "handoff", map[string]any{"verdict": "approved", "note": "good"}, h.ack(rv), nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(rv, "succeeded", "accepted")
	p = h.task("p")
	p = taskOf(t, people["bob"].do("POST", "/tasks/p/actions", map[string]any{"version": p.Version, "action": "done", "request_id": "d1"}))
	if p.Status != "DONE" {
		t.Fatalf("%+v", p)
	}

	// Give back: a person's started task reassigned to an agent is READY for it and scheduled as usual.
	if w := h.owner("POST", "/tasks", map[string]any{"id": "g", "project_id": "notes", "title": "Give back", "assignee": "member:bob", "review_by": "owner", "ready": true, "request_id": "g"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	g := taskOf(t, people["bob"].do("POST", "/tasks/g/actions", map[string]any{"version": h.task("g").Version, "action": "start", "request_id": "gs"}))
	g = taskOf(t, people["bob"].do("POST", "/tasks/g/reassign", map[string]any{"version": g.Version, "assignee": "demo", "reason": "no time", "request_id": "gb"}))
	if g.Status != "READY" || g.Assignee != "demo" {
		t.Fatalf("%+v", g)
	}
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if g = h.task("g"); g.CurrentRunID == "" {
		t.Fatalf("given-back task not scheduled: %+v", g)
	}
}

// TestAgentsAndPeoplesTasks (factory-mcp): an agent gives a task to a person only citing that person's message;
// it cannot take a person's task back, cancel it, block it or rewrite it; reading and depending stay allowed.
func TestAgentsAndPeoplesTasks(t *testing.T) {
	h, _ := peopleHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "menu", ProjectID: "notes", Title: "Main menu layout", Assignee: "demo", ReviewBy: "owner", RequestID: "menu"}); err != nil {
		t.Fatal(err)
	}
	// The owner writes to the producer, as ilya.
	msg, err := h.s.SendMessage(storage.WithPerson(ctx, storage.Person{ID: "ilya", Name: "Ilya"}), co, messaging.Send{ProjectID: "notes", Sender: "owner", Recipient: "manager", MessageType: "notification", Body: "I'll do the main menu layout myself", RequestID: "say"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := h.s.SendMessage(storage.WithPerson(ctx, storage.Person{ID: "bob", Name: "Bob"}), co, messaging.Send{ProjectID: "notes", Sender: "owner", Recipient: "manager", MessageType: "notification", Body: "ilya will do it", RequestID: "bob-says"})
	if err != nil {
		t.Fatal(err)
	}
	h.create("plan", "notes", "manager")
	r := h.start("plan")
	call := func(name string, args map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		return h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: name, Arguments: args}, nil)
	}
	version := func() float64 { return float64(h.task("menu").Version) }
	for _, reason := range []string{"", "they said so", "bob said so: " + other.ID} {
		if w := call("factory_task_assign", map[string]any{"id": "menu", "version": version(), "assignee": "member:ilya", "assign_reason": reason, "request_id": "a-" + reason}); w.Code != 403 {
			t.Fatalf("assigned to a person without their word (%q): %d %s", reason, w.Code, w.Body.String())
		}
	}
	if w := call("factory_task_assign", map[string]any{"id": "menu", "version": version(), "assignee": "member:ilya", "assign_reason": "owner message " + msg.ID, "request_id": "a-ok"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if m := h.task("menu"); m.Assignee != "member:ilya" {
		t.Fatalf("%+v", m)
	}
	// A dependent task created by the producer keeps its dependency on it.
	if w := call("factory_task_create", map[string]any{"title": "Menu art", "assignee": "demo", "dependencies": []string{"menu"}, "request_id": "dep"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("factory_task_create", map[string]any{"title": "Bob's part", "assignee": "member:bob", "assign_reason": msg.ID, "request_id": "bob"}); w.Code != 403 {
		t.Fatal("ilya's message is not bob's word:", w.Code, w.Body.String())
	}
	// Hands off: assignee, description, status and cancel are refused with the hint; reading is allowed.
	for name, args := range map[string]map[string]any{
		"factory_task_assign":   {"id": "menu", "version": version(), "assignee": "manager", "request_id": "back"},
		"factory_task_update":   {"id": "menu", "version": version(), "description": "do it my way", "request_id": "desc"},
		"factory_task_cancel":   {"id": "menu", "version": version(), "reason": "dup", "request_id": "cancel"},
		"factory_task_block":    {"id": "menu", "version": version(), "reason": "wait", "request_id": "block"},
		"factory_task_ready":    {"id": "menu", "version": version(), "request_id": "ready"},
		"factory_task_reassign": {"id": "menu", "version": version(), "assignee": "demo", "request_id": "re"},
		"factory_task_create":   {"title": "Menu, my way", "assignee": "demo", "replaces": []string{"menu"}, "request_id": "rep"},
	} {
		if w := call(name, args); w.Code != 403 || !strings.Contains(w.Body.String(), "message about it") && !strings.Contains(w.Body.String(), "comment on it") {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if m := h.task("menu"); m.Assignee != "member:ilya" || m.Status == "CANCELLED" {
		t.Fatalf("%+v", m)
	}
	if w := call("factory_task_get", map[string]any{"id": "menu"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Agents do not move a person's task: start, submit and done are people's.
	if w := h.owner("POST", "/tasks/menu/actions", map[string]any{"version": version(), "action": "start", "actor": "manager", "request_id": "st"}); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}

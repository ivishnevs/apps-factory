package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// The repository catalog's tree: calorie-designer, calorie-ios and calorie-qa report to calorie-manager; demo
// and claude-demo to manager; calorie-manager, manager and manager-autonomous to the owner.

// TestAuthorityActions (hierarchy 1.3): a lead pauses, reassigns within its subtree and reprioritizes its
// reports' tasks; outside its subtree it gets 403. A pause shows paused:task and only the pauser or a higher
// authority resumes it; pause and reprioritize leave a wait park active; cancel needs cascade with open children.
func TestAuthorityActions(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	owner := func(path string, body map[string]any) (int, string) {
		t.Helper()
		w := h.do(h.c.Token, "POST", path, body, nil)
		return w.Code, w.Body.String()
	}
	h.create("ios-work", "notes", "calorie-ios")

	// A manager outside the assignee's chain has no authority; the lead may not move work out of its subtree.
	if code, body := owner("/tasks/ios-work/pause", map[string]any{"version": h.task("ios-work").Version, "actor": "manager", "request_id": "p0"}); code != 403 {
		t.Fatal(code, body)
	}
	if code, body := owner("/tasks/ios-work/reassign", map[string]any{"version": h.task("ios-work").Version, "actor": "calorie-manager", "assignee": "demo", "request_id": "r0"}); code != 403 || h.task("ios-work").Assignee != "calorie-ios" {
		t.Fatal(code, body)
	}
	// The lead pauses: why says paused:task; a lower or equal authority other than the pauser cannot resume.
	if code, body := owner("/tasks/ios-work/pause", map[string]any{"version": h.task("ios-work").Version, "actor": "calorie-manager", "reason": "hold for the design", "request_id": "p1"}); code != 200 {
		t.Fatal(code, body)
	}
	var why tasks.Verdict
	_ = json.Unmarshal(h.do(h.c.Token, "GET", "/tasks/ios-work/why", nil, nil).Body.Bytes(), &why)
	if why.Reason != "paused:task" || why.Clearer.Who != "calorie-manager" || !strings.Contains(why.Detail, "hold for the design") {
		t.Fatalf("%+v", why)
	}
	if got := h.task("ios-work"); got.PausedBy != "calorie-manager" {
		t.Fatalf("%+v", got)
	}
	if err := h.c.Schedule(ctx); err != nil || h.task("ios-work").CurrentRunID != "" {
		t.Fatal("a paused task was dispatched", err)
	}
	// The owner outranks the pauser.
	if code, body := owner("/tasks/ios-work/resume", map[string]any{"version": h.task("ios-work").Version, "request_id": "u1"}); code != 200 {
		t.Fatal(code, body)
	}
	_ = json.Unmarshal(h.do(h.c.Token, "GET", "/tasks/ios-work/why", nil, nil).Body.Bytes(), &why)
	if why.Reason != tasks.ReasonRunnable {
		t.Fatalf("%+v", why)
	}
	// The owner pauses; the lead (a lower rank) cannot resume it.
	if code, _ := owner("/tasks/ios-work/pause", map[string]any{"version": h.task("ios-work").Version, "request_id": "p2"}); code != 200 {
		t.Fatal(code)
	}
	if code, body := owner("/tasks/ios-work/resume", map[string]any{"version": h.task("ios-work").Version, "actor": "calorie-manager", "request_id": "u2"}); code != 403 {
		t.Fatal(code, body)
	}
	if code, _ := owner("/tasks/ios-work/resume", map[string]any{"version": h.task("ios-work").Version, "request_id": "u3"}); code != 200 {
		t.Fatal(code)
	}

	// Through Factory MCP: the lead's run reassigns within its subtree only (the owner holds the task meanwhile,
	// so its run does not take the project workspace first).
	if code, _ := owner("/tasks/ios-work/pause", map[string]any{"version": h.task("ios-work").Version, "request_id": "p3"}); code != 200 {
		t.Fatal(code)
	}
	h.create("plan", "notes", "calorie-manager")
	lead := h.start("plan")
	call := func(name string, args map[string]any) (int, string) {
		t.Helper()
		w := h.do(lead.token, "POST", "/agent/call", factorytools.Call{Name: name, Arguments: args}, nil)
		return w.Code, w.Body.String()
	}
	if code, body := call("factory_task_reassign", map[string]any{"id": "ios-work", "version": float64(h.task("ios-work").Version), "assignee": "demo", "request_id": "m1"}); code != 403 {
		t.Fatal(code, body)
	}
	if code, body := call("factory_task_reassign", map[string]any{"id": "ios-work", "version": float64(h.task("ios-work").Version), "assignee": "calorie-qa", "reason": "qa first", "request_id": "m2"}); code != 200 {
		t.Fatal(code, body)
	}
	if snap, _ := h.s.TaskSnapshot(ctx, co, "ios-work"); snap.Task.Assignee != "calorie-qa" || snap.Pending[len(snap.Pending)-1].Kind != "assignment" || !snap.Pending[len(snap.Pending)-1].Authority {
		t.Fatalf("%+v %+v", snap.Task, snap.Pending)
	}
	if code, body := call("factory_task_pause", map[string]any{"agent": "demo", "request_id": "m3"}); code != 403 {
		t.Fatal("paused an agent outside the subtree", code, body)
	}
	if code, body := call("factory_task_pause", map[string]any{"agent": "calorie-qa", "reason": "review backlog", "request_id": "m4"}); code != 200 {
		t.Fatal(code, body)
	}
	_ = json.Unmarshal(h.do(h.c.Token, "GET", "/tasks/ios-work/why", nil, nil).Body.Bytes(), &why)
	if why.Reason != "paused:agent:calorie-qa" {
		t.Fatalf("%+v", why)
	}
	if code, body := call("factory_task_resume", map[string]any{"agent": "calorie-qa", "request_id": "m5"}); code != 200 {
		t.Fatal(code, body)
	}
	h.end(lead, "succeeded", "")

	// A task waiting on its wait exit: reprioritize and pause change nothing about the wait (its park and its
	// episode stay); a reassignment ends both.
	episodes := func() int {
		t.Helper()
		var n int
		if err := h.s.DB.QueryRowContext(ctx, "SELECT count(*) FROM wait_episodes WHERE task_id='waits'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	h.create("waits", "notes", "calorie-ios")
	r := h.start("waits")
	wake := h.fake.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	if w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_exit_wait", Arguments: map[string]any{"reason": "store review", "wake_at": wake, "deadline": h.at(24 * time.Hour)}}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "succeeded", "accepted")
	if code, body := owner("/tasks/waits/reprioritize", map[string]any{"version": h.task("waits").Version, "actor": "calorie-manager", "priority": 50, "request_id": "pr"}); code != 200 {
		t.Fatal(code, body)
	}
	if code, body := owner("/tasks/waits/pause", map[string]any{"version": h.task("waits").Version, "actor": "calorie-manager", "request_id": "pw"}); code != 200 {
		t.Fatal(code, body)
	}
	snap, _ := h.s.TaskSnapshot(ctx, co, "waits")
	if snap.Task.Priority != 50 || len(snap.Parks) != 1 || snap.Parks[0].Kind != tasks.ParkWait || episodes() != 1 {
		t.Fatalf("%+v %+v", snap.Task, snap.Parks)
	}
	if code, body := owner("/tasks/waits/reassign", map[string]any{"version": h.task("waits").Version, "actor": "calorie-manager", "assignee": "calorie-qa", "request_id": "rw"}); code != 200 {
		t.Fatal(code, body)
	}
	if snap, _ = h.s.TaskSnapshot(ctx, co, "waits"); len(snap.Parks) != 0 || episodes() != 0 || snap.Task.Assignee != "calorie-qa" {
		t.Fatalf("%+v %+v", snap.Task, snap.Parks)
	}
	// Every action is in the one feed, visible in the lead's subtree view (factory_feed_list, ?subtree=).
	feed, _ := h.s.Feed(ctx, co, storage.FeedFilter{Task: "waits", Agents: h.c.subtree("calorie-manager")}, 0, 100)
	kinds := map[string]bool{}
	for _, f := range feed.Items {
		kinds[f.Type] = true
	}
	if !kinds["task.reprioritized"] || !kinds["task.paused"] || !kinds["task.reassigned"] || !kinds["task.wait_interrupted"] {
		t.Fatal("authority actions are events", kinds)
	}

	// Cancel: an agent without authority is refused; open children need cascade.
	h.create("parent", "notes", "calorie-ios")
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "child", ProjectID: "notes", Title: "child", Assignee: "calorie-qa", ParentTaskID: "parent", RequestID: "child"}); err != nil {
		t.Fatal(err)
	}
	if code, body := owner("/tasks/parent/actions", map[string]any{"version": h.task("parent").Version, "action": "cancel", "actor": "manager", "request_id": "c0"}); code != 403 {
		t.Fatal(code, body)
	}
	if code, body := owner("/tasks/parent/actions", map[string]any{"version": h.task("parent").Version, "action": "cancel", "actor": "calorie-manager", "request_id": "c1"}); code != 409 || !strings.Contains(body, "needs_cascade") {
		t.Fatal(code, body)
	}
	if code, body := owner("/tasks/parent/actions", map[string]any{"version": h.task("parent").Version, "action": "cancel", "actor": "calorie-manager", "cascade": true, "request_id": "c2"}); code != 200 {
		t.Fatal(code, body)
	}
	if h.task("parent").Status != "CANCELLED" || h.task("child").Status != "CANCELLED" {
		t.Fatal(h.task("parent").Status, h.task("child").Status)
	}
}

// TestUndispatchableConversationDoesNotStopCore: a message to an agent removed from the catalog or with an
// oversized context waits on its conversation task with a reason; Core keeps scheduling.
func TestUndispatchableConversationDoesNotStopCore(t *testing.T) {
	for _, scenario := range []string{"missing-agent", "oversized-context"} {
		t.Run(scenario, func(t *testing.T) {
			h := newExitHarness(t)
			ctx := context.Background()
			if _, err := h.s.SendMessage(ctx, storage.DefaultCompany, messaging.Send{ProjectID: "notes", Sender: "demo", Recipient: "manager", MessageType: "question", Body: "Cannot dispatch", RequestID: "ask"}); err != nil {
				t.Fatal(err)
			}
			if scenario == "missing-agent" {
				delete(h.c.Agents, "manager")
			} else {
				a := h.c.Agents["manager"]
				a.Instructions = strings.Repeat("x", 1<<20)
				h.c.Agents["manager"] = a
			}
			if err := h.c.Schedule(ctx); err != nil {
				t.Fatal("scheduler stopped", err)
			}
			snap, _ := h.s.TaskSnapshot(ctx, storage.DefaultCompany, storage.ConversationID("notes", "manager"))
			v := h.c.evaluate(snap)
			if scenario == "missing-agent" && v.Reason != tasks.ReasonUnassigned || scenario == "oversized-context" && v.Reason != tasks.ReasonParked+tasks.ParkBlock {
				t.Fatalf("%+v", v)
			}
		})
	}
}

// protocolEnded is a run's accepted end with the session its runtime reported.
func protocolEnded(r exitRun, session string) protocol.Envelope {
	return protocol.Wrap("agent.ended", r.id, "", protocol.Event{RunID: r.id, Lease: r.lease, Status: "ENDED", Outcome: "succeeded", ExitState: "accepted", SessionID: session})
}

// TestDecisionAnswers (hierarchy 2.4): a free-text answer is accepted, the first answer wins and a later one
// gets 409 with it; a question whose task changed reads as possibly outdated; closing the task cancels it.
func TestDecisionAnswers(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	ask := func(task, question string) messaging.Message {
		t.Helper()
		h.create(task, "notes", "calorie-ios")
		r := h.start(task)
		if w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_exit_ask", Arguments: map[string]any{"question": question, "options": []any{"Diary", "Log"}, "category": "product"}}, nil); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		h.end(r, "succeeded", "accepted")
		m, err := h.s.Message(ctx, co, h.task(task).WaitMessageID)
		if err != nil || m.Recipient != "calorie-manager" || m.MayBeOutdated {
			t.Fatalf("%+v %v", m, err)
		}
		return m
	}
	q := ask("naming", "Call it Diary or Log?")
	// The lead answers in free text (no form answers); the owner's later answer is refused with it.
	if _, err := h.s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "calorie-manager", Body: "Neither: call it Journal", RequestID: "lead"}); err != nil {
		t.Fatal(err)
	}
	w := h.do(h.c.Token, "POST", "/inbox/"+q.ID+"/answer", map[string]any{"version": q.Version, "body": "Diary", "request_id": "owner"}, nil)
	var refused struct {
		Reason string            `json:"reason"`
		Answer messaging.Message `json:"answer"`
	}
	if w.Code != 409 || json.Unmarshal(w.Body.Bytes(), &refused) != nil || refused.Reason != "already_answered" || refused.Answer.Body != "Neither: call it Journal" || refused.Answer.Sender != "calorie-manager" {
		t.Fatal(w.Code, w.Body.String())
	}
	if snap, _ := h.s.TaskSnapshot(ctx, co, "naming"); snap.Pending[len(snap.Pending)-1].Kind != "answer" || !snap.Pending[len(snap.Pending)-1].Authority {
		t.Fatalf("%+v", snap.Pending)
	}
	if _, err := h.s.TaskAction(ctx, co, "naming", tasks.Action{Version: h.task("naming").Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}

	// The task changed after the question (its last child closed): the question may be outdated. (Authority
	// input would supersede it: explicit-exits-authority D7.)
	q = ask("colors", "Green or blue?")
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "colors-sub", ProjectID: "notes", Title: "sub", ParentTaskID: "colors", RequestID: "sub"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.s.TaskAction(ctx, co, "colors-sub", tasks.Action{Version: h.task("colors-sub").Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	if q, _ = h.s.Message(ctx, co, q.ID); !q.MayBeOutdated {
		t.Fatalf("%+v", q)
	}
	// Closing the task cancels its open question.
	if _, err := h.s.TaskAction(ctx, co, "colors", tasks.Action{Version: h.task("colors").Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	if q, _ = h.s.Message(ctx, co, q.ID); q.Status != "CANCELLED" {
		t.Fatalf("%+v", q)
	}
	if _, err := h.s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: "blue", RequestID: "late"}); err == nil {
		t.Fatal("answered a cancelled question")
	}
}

// TestSilentLeadNoteAndCategoryInterval (hierarchy 2.3): a policy's escalate_after for a category moves the
// question up after that interval, and the skipped lead's conversation records the forward.
func TestSilentLeadNoteAndCategoryInterval(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	// calorie-project gives calorie-manager 2h for marketing questions (4h otherwise).
	if d := h.c.Agents["calorie-manager"].EscalateAfter("marketing"); d != 2*time.Hour {
		t.Fatal(d)
	}
	h.create("pricing", "notes", "calorie-ios")
	r := h.start("pricing")
	if w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_exit_ask", Arguments: map[string]any{"question": "Charge for export?", "category": "marketing"}}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "succeeded", "accepted")
	id := h.task("pricing").WaitMessageID
	h.fake.Advance(2*time.Hour - time.Minute)
	if n, err := h.s.AutoForward(ctx, co); err != nil || n != 0 {
		t.Fatal("forwarded early", n, err)
	}
	h.fake.Advance(2 * time.Minute)
	if n, err := h.s.AutoForward(ctx, co); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if m, _ := h.s.Message(ctx, co, id); m.Recipient != "owner" {
		t.Fatalf("%+v", m)
	}
	comments, _ := h.s.Comments(ctx, co, storage.ConversationID("notes", "calorie-manager"))
	if len(comments) != 1 || !strings.Contains(comments[0].Text, "forwarded to owner") {
		t.Fatalf("%+v", comments)
	}
}

// TestConversationTasks (hierarchy 3.1): owner messages are triggers on the default direct report's
// conversation task, whose runs resume one session; done keeps the task open; @name reaches another report.
func TestConversationTasks(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	h.c.DefaultReport = "manager"
	say := func(text, key string) messaging.Message {
		t.Helper()
		w := h.do(h.c.Token, "POST", "/messages", map[string]any{"project_id": "notes", "body": text, "message_type": "notification", "request_id": key}, nil)
		var m messaging.Message
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &m) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		return m
	}
	conv := storage.ConversationID("notes", "manager")
	first := say("Plan the onboarding flow", "one")
	if first.Recipient != "manager" || first.DeliveryStatus != "TRIGGERED" {
		t.Fatalf("%+v", first)
	}
	r1 := h.start(conv)
	if spec, _ := h.s.Get(ctx, co, r1.id); !strings.Contains(spec.Spec.Prompt, "Plan the onboarding flow") || !strings.Contains(spec.Spec.Prompt, "conversation task") {
		t.Fatal(spec.Spec.Prompt)
	}
	if w := h.do(r1.token, "POST", "/agent/call", factorytools.Call{Name: "factory_exit_done", Arguments: map[string]any{"summary": "planned"}}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := h.s.Apply(ctx, co, protocolEnded(r1, "thread-manager")); err != nil {
		t.Fatal(err)
	}
	if got := h.task(conv); got.Status != "READY" || got.Kind != tasks.KindConversation || got.Counterpart != "owner" {
		t.Fatalf("%+v", got)
	}
	h.fake.Advance(time.Hour)
	say("Also add a dark theme", "two")
	r2 := h.start(conv)
	spec, _ := h.s.Get(ctx, co, r2.id)
	if spec.Spec.SessionID != "thread-manager" || !strings.Contains(spec.Spec.Prompt, "dark theme") || strings.Contains(spec.Spec.Prompt, "onboarding") {
		t.Fatalf("%+v", spec.Spec)
	}
	h.end(r2, "succeeded", "")
	// Addressed to another direct report.
	if m := say("@calorie-manager how is the calculator going?", "three"); m.Recipient != "calorie-manager" {
		t.Fatalf("%+v", m)
	}
	if snap, _ := h.s.TaskSnapshot(ctx, co, storage.ConversationID("notes", "calorie-manager")); len(snap.Pending) != 1 || snap.Pending[0].Kind != "message" || !snap.Pending[0].Authority {
		t.Fatalf("%+v", snap.Pending)
	}
	if w := h.do(h.c.Token, "POST", "/messages", map[string]any{"project_id": "notes", "body": "@demo hi", "request_id": "four"}, nil); w.Code != 400 {
		t.Fatal("addressed an agent that does not report to the owner", w.Code)
	}
	// The owner conversation maps to the conversation task.
	var convs []messaging.Conversation
	_ = json.Unmarshal(h.do(h.c.Token, "GET", "/conversations?project=notes", nil, nil).Body.Bytes(), &convs)
	if len(convs) != 2 || convs[0].TaskID != conv {
		t.Fatalf("%+v", convs)
	}
}

// TestDelegatedClosedAndSubtreeBrief (hierarchy 3.3): a closed task the manager delegated wakes its
// conversation (delegated_closed) and the manager's brief lists the subtree's closed, escalated and failed
// tasks since its last run; a failed attempt alone wakes nobody.
func TestDelegatedClosedAndSubtreeBrief(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	conv := storage.ConversationID("notes", "manager")
	if _, err := h.s.ConversationTask(ctx, co, "notes", "manager"); err != nil {
		t.Fatal(err)
	}
	h.fake.Advance(time.Minute)
	h.create("plan", "notes", "manager")
	r := h.start("plan")
	if w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_task_create", Arguments: map[string]any{"title": "Build the list", "assignee": "demo", "request_id": "d1"}}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_exit_done", Arguments: map[string]any{"summary": "delegated"}}, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "succeeded", "accepted")
	created, _ := h.s.CreatedBy(ctx, co, "plan")
	if len(created) != 1 {
		t.Fatal(created)
	}
	// A report's failed attempt is retried and wakes nobody.
	h.create("flaky", "notes", "demo")
	h.end(h.start("flaky"), "failed", "")
	if snap, _ := h.s.TaskSnapshot(ctx, co, conv); len(snap.Pending) != 0 {
		t.Fatalf("woken by a failure: %+v", snap.Pending)
	}
	if _, err := h.s.PauseTask(ctx, co, "flaky", tasks.Authority{Version: h.task("flaky").Version}); err != nil { // its retry would take the workspace first
		t.Fatal(err)
	}
	// A report's run without an exit escalates to the manager (a message on its conversation).
	h.fake.Advance(time.Hour) // past the backoff
	h.create("silent", "notes", "claude-demo")
	h.end(h.start("silent"), "succeeded", "")
	// The delegated task closes.
	if _, err := h.s.TaskAction(ctx, co, created[0].ID, tasks.Action{Version: created[0].Version, Action: "cancel", Reason: "not needed"}); err != nil {
		t.Fatal(err)
	}
	snap, _ := h.s.TaskSnapshot(ctx, co, conv)
	kinds := []string{}
	for _, p := range snap.Pending {
		kinds = append(kinds, p.Kind)
	}
	if strings.Join(kinds, ",") != "message,delegated_closed" {
		t.Fatal(kinds)
	}
	brief := h.start(conv)
	spec, _ := h.s.Get(ctx, co, brief.id)
	for _, want := range []string{"Under you since your last run", created[0].ID + " [CANCELLED]", "silent escalated no_exit to manager", "flaky run", "ended failed", "delegated_closed"} {
		if !strings.Contains(spec.Spec.Prompt, want) {
			t.Fatalf("brief lacks %q:\n%s", want, spec.Spec.Prompt)
		}
	}
}

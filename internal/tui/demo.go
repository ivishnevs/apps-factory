package tui

import (
	"encoding/json"
	"fmt"
	"time"

	"appfactory/internal/approvals"
	"appfactory/internal/core"
	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// DemoAt is the moment the demo fixture shows (factory ui --demo).
var DemoAt = time.Date(2026, 10, 20, 14, 30, 0, 0, time.UTC)

// DemoFixture is a small company at work: two projects, a manager tree, live runs, a question to the owner,
// an escalation, an approval, a paused agent and an offline node. factory ui --demo renders it.
func DemoFixture() Fixture {
	at := DemoAt
	ago := func(m int) time.Time { return at.Add(-time.Duration(m) * time.Minute) }
	unix := func(m int) int64 { return ago(m).Unix() }
	f := Fixture{Name: "demo", Details: map[string]TaskDetail{}, Runs: map[string]storage.Run{}, RunEvents: map[string][]RunEvent{}, ToolCalls: map[string][]storage.ToolCall{}}

	agents := []Agent{
		{Name: "calorie-designer", Role: "designer", ReportsTo: "calorie-manager", Provider: "claude", Model: "claude-opus-4-7"},
		{Name: "calorie-ios", Role: "ios-engineer", ReportsTo: "calorie-manager", Provider: "claude", Model: "claude-opus-4-7"},
		{Name: "calorie-manager", Role: "manager", ReportsTo: "owner", Provider: "codex", Model: "gpt-5.5"},
		{Name: "calorie-qa", Role: "qa", ReportsTo: "calorie-manager", Provider: "codex", Model: "gpt-5.5"},
		{Name: "demo", Role: "engineer", ReportsTo: "manager", Provider: "codex", Model: "gpt-5.5"},
		{Name: "manager", Role: "manager", ReportsTo: "owner", Provider: "codex", Model: "gpt-5.5"},
		{Name: "reviewer", Role: "reviewer", ReportsTo: "manager", Provider: "codex", Model: "gpt-5.5"},
	}
	task := func(id, project, title, status, assignee, createdBy, origin string, prio, createdMin int, reason, who, action string) Task {
		return Task{Task: tasks.Task{ID: id, ProjectID: project, Title: title, Status: status, Assignee: assignee, CreatedBy: createdBy, OriginTask: origin,
			Priority: prio, CreatedAt: unix(createdMin), UpdatedAt: unix(createdMin / 2), Version: 3, Dependencies: []string{}}, Reason: reason, Clearer: tasks.Clearer{Who: who, Action: action}}
	}
	ts := []Task{
		task("CAL-1", "calorie", "Ship calorie calculator v1", "READY", "calorie-manager", "owner", "", 9, 300, "parked:children", "calorie-manager", "wait for CAL-3, CAL-4"),
		task("CAL-2", "calorie", "Design the meal entry screen", "DONE", "calorie-designer", "calorie-manager", "CAL-1", 7, 280, "terminal", "nobody", "final state"),
		task("CAL-3", "calorie", "Build the meal entry screen", "IN_PROGRESS", "calorie-ios", "calorie-manager", "CAL-1", 7, 270, "live_run", "calorie-ios", "run r-ios-7 is running"),
		task("CAL-4", "calorie", "Test meal entry on iPhone SE", "READY", "calorie-qa", "calorie-manager", "CAL-1", 6, 265, "dependencies", "calorie-ios", "finish CAL-3"),
		task("CAL-5", "calorie", "Pick the nutrition data API", "READY", "calorie-manager", "calorie-manager", "CAL-1", 8, 200, "parked:ask", "owner", "answer m-ask-1 in the inbox"),
		task("CAL-6", "calorie", "App Store screenshots", "BACKLOG", "calorie-designer", "owner", "", 2, 150, "backlog", "owner", "factory tasks ready CAL-6 VERSION"),
		task("N-1", "notes", "Markdown export", "IN_REVIEW", "reviewer", "manager", "N-3", 6, 240, "live_run", "reviewer", "run r-rev-2 is running"),
		task("N-2", "notes", "Fix the sync conflict on rename", "READY", "demo", "manager", "N-3", 7, 230, "backoff", "nobody", "retry at 14:34"),
		task("N-3", "notes", "Release notes app 1.2", "READY", "manager", "owner", "", 9, 320, "parked:escalation", "owner", "answer m-esc-1 (unblock or cancel)"),
		{Task: tasks.Task{ID: "conv-notes-manager", ProjectID: "notes", Title: "Conversation with owner", Status: "READY", Assignee: "manager", Kind: "conversation", Counterpart: "owner", Version: 1, CreatedAt: unix(400)}, Reason: "idle", Clearer: tasks.Clearer{Who: "nobody", Action: "waits for a message"}},
	}
	ts[2].Dependencies = []string{"CAL-2"}
	ts[2].CurrentRunID = "r-ios-7"
	ts[2].Attempt = 2
	ts[3].Dependencies = []string{"CAL-3"}
	ts[6].Implementer, ts[6].Reviewer, ts[6].CurrentRunID = "demo", "reviewer", "r-rev-2"
	ts[1].Description = "Mockups for the meal entry screen: portion picker, recent meals, barcode button."
	ts[2].Description = "Implement the meal entry screen from the CAL-2 mockups.\n\nAcceptance:\n- portion picker in grams\n- works offline\n- unit tests for the calorie sum"

	status := []core.ProjectStatus{
		{Project: tasks.Project{ID: "calorie", Name: "Calorie Calculator"}, Tasks: map[string]int{"READY": 3, "IN_PROGRESS": 1, "DONE": 1, "BACKLOG": 1},
			Runs:    []core.RunStatus{{ID: "r-ios-7", Agent: "calorie-ios", TaskID: "CAL-3", Status: "RUNNING", StartedAt: unix(12)}},
			Waiting: []tasks.Verdict{}, Runnable: 0, Inbox: 1},
		{Project: tasks.Project{ID: "notes", Name: "Notes"}, Tasks: map[string]int{"READY": 3, "IN_REVIEW": 1},
			Runs:    []core.RunStatus{{ID: "r-rev-2", Agent: "reviewer", TaskID: "N-1", Status: "RUNNING", StartedAt: unix(4)}},
			Waiting: []tasks.Verdict{}, Runnable: 0, Inbox: 2},
	}

	ask := messaging.Message{ID: "m-ask-1", ProjectID: "calorie", TaskID: "CAL-5", Sender: "calorie-manager", Recipient: "owner", ConversationID: "owner-calorie-manager-calorie", MessageType: "question",
		Body: "Which nutrition data API may we pay for? USDA is free but has no barcodes.", Blocking: true, Status: "PENDING", Version: 1, CreatedAt: unix(95), Deadline: at.Add(23 * time.Hour).Unix(),
		Questions: []messaging.Question{{ID: "answer", Type: "single_choice", Question: "Which nutrition API?", Options: []string{"usda-free", "nutritionix-paid", "edamam-paid"}}}}
	esc := messaging.Message{ID: "m-esc-1", ProjectID: "notes", TaskID: "N-3", Sender: "factory", Recipient: "owner", ConversationID: "owner-manager-notes", MessageType: "question",
		Body: "N-3 stalled: it waits on N-2, which failed 3 times without progress.", Status: "PENDING", Version: 1, CreatedAt: unix(30), EscalationReason: "stalled",
		Questions: []messaging.Question{{ID: "answer", Type: "single_choice", Question: "How should task N-3 continue?", Options: []string{"unblock", "cancel"}}}}
	appr := messaging.Message{ID: "m-appr-1", ProjectID: "notes", TaskID: "N-3", Sender: "manager", Recipient: "owner", ConversationID: "owner-manager-notes", MessageType: "question", ApprovalID: "ap-1",
		Body: "Approve publishing notes 1.2 to TestFlight.", Status: "PENDING", Version: 1, CreatedAt: unix(20)}
	approval := approvals.Approval{ID: "ap-1", ProjectID: "notes", Agent: "manager", TaskID: "N-3", RunID: "r-mgr-3", MessageID: "m-appr-1", Status: "PENDING", Version: 1, CreatedAt: unix(20), ExpiresAt: at.Add(24 * time.Hour).Unix(),
		Reason: "release 1.2 is approved by review", Call: factorytools.Call{Name: "factory_release_publish", Arguments: map[string]any{"channel": "testflight", "version": "1.2"}}}

	f.Overview = Overview{
		Health: Health{Status: "ok", Database: "postgres 17.6", Company: "default", ExplicitExits: "on"},
		Status: status, Agents: agents, Tasks: ts,
		Pauses:    []storage.Pause{{Scope: "agent", Target: "calorie-designer", Reason: "waiting for the brand guide", Actor: "owner", CreatedAt: unix(60)}},
		Pending:   storage.PendingCounts{Inbox: 3, Approvals: 1, Alerts: 1, Reviews: 1, Total: 4},
		Nodes:     demoNodes(at),
		Inbox:     []messaging.Message{ask, appr, esc},
		Approvals: []approvals.Approval{approval},
		Costs: storage.CostReport{Groups: []storage.CostGroup{
			{Key: "calorie-ios", Runs: 6, CostUSD: 1.84}, {Key: "demo", Runs: 4, CostUSD: 0.92}, {Key: "calorie-designer", Runs: 2, CostUSD: 0.55},
			{Key: "reviewer", Runs: 1, CostUSD: 0.31}, {Key: "calorie-manager", Runs: 3, CostUSD: 0.12}}, Total: storage.CostGroup{Key: "total", Runs: 16, CostUSD: 3.74}},
		At: at,
	}

	seq := int64(0)
	item := func(m int, kind, project, taskID, runID, agent, actor, summary string, attention bool) storage.FeedItem {
		seq++
		entity := taskID
		if entity == "" {
			entity = kind
		}
		return storage.FeedItem{Sequence: seq, EventID: fmt.Sprintf("e%03d", seq), Type: kind, EntityID: entity, ProjectID: project, CreatedAt: ago(m), TaskID: taskID, RunID: runID, Agent: agent, Actor: actor, Summary: summary, Attention: attention}
	}
	f.Feed = []storage.FeedItem{
		item(320, "task.created", "notes", "N-3", "", "manager", "owner", "Release notes app 1.2", false),
		item(300, "task.created", "calorie", "CAL-1", "", "calorie-manager", "owner", "Ship calorie calculator v1", false),
		item(280, "task.created", "calorie", "CAL-2", "", "calorie-designer", "calorie-manager", "Design the meal entry screen", false),
		item(270, "task.created", "calorie", "CAL-3", "", "calorie-ios", "calorie-manager", "Build the meal entry screen", false),
		item(265, "task.created", "calorie", "CAL-4", "", "calorie-qa", "calorie-manager", "Test meal entry on iPhone SE", false),
		item(240, "task.created", "notes", "N-1", "", "demo", "manager", "Markdown export", false),
		item(230, "task.created", "notes", "N-2", "", "demo", "manager", "Fix the sync conflict on rename", false),
		item(210, "task.exit.done", "calorie", "CAL-2", "r-des-3", "calorie-designer", "calorie-designer", "calorie-designer done: meal entry mockups in Figma v3", false),
		item(209, "task.trigger.dependency_done", "calorie", "CAL-3", "", "calorie-ios", "calorie-designer", "CAL-2 done", false),
		item(200, "task.created", "calorie", "CAL-5", "", "calorie-manager", "calorie-manager", "Pick the nutrition data API", false),
		item(160, "message.created", "calorie", "CAL-3", "", "", "calorie-ios", "calorie-ios → calorie-manager (question): Should the portion picker allow grams and ounces?", false),
		item(150, "message.created", "calorie", "CAL-3", "", "", "calorie-manager", "calorie-manager → calorie-ios (answer): Grams only for v1.", false),
		item(120, "task.exit.submitted", "notes", "N-1", "r-demo-5", "demo", "demo", "demo → reviewer (submit): markdown export with round-trip tests", false),
		item(110, "task.exit.handoff", "notes", "N-1", "r-rev-1", "reviewer", "reviewer", "reviewer → demo (changes_requested): escape pipes in tables", false),
		item(100, "task.exit.asked", "calorie", "CAL-5", "r-cm-2", "calorie-manager", "calorie-manager", "calorie-manager asked: Which nutrition data API may we pay for?", false),
		item(95, "message.created", "calorie", "CAL-5", "", "", "calorie-manager", "calorie-manager → owner (question): Which nutrition data API may we pay for? USDA is free but has no barcodes.", true),
		item(95, "task.parked.ask", "calorie", "CAL-5", "", "calorie-manager", "", "waits for the answer to m-ask-1", false),
		item(80, "task.retry", "calorie", "CAL-3", "r-ios-6", "calorie-ios", "", "CAL-3: run r-ios-6 ended failed; retry in 1m0s", false),
		item(70, "task.trigger.directive", "calorie", "CAL-3", "", "calorie-ios", "owner", "Keep the entry screen offline-first.", false),
		item(60, "owner.paused", "", "", "", "", "owner", "agent calorie-designer: waiting for the brand guide", true),
		item(55, "task.exit.submitted", "notes", "N-1", "r-demo-6", "demo", "demo", "demo → reviewer (submit): pipes escaped, tests added", false),
		item(45, "task.alert.no_progress", "notes", "N-2", "", "demo", "", "N-2: 3 runs without progress", true),
		item(40, "task.exit.handoff", "notes", "N-3", "r-mgr-2", "manager", "manager", "manager → demo (handoff): rebase N-2 on main before retrying", false),
		item(30, "task.escalation.stalled", "notes", "N-3", "", "manager", "", "N-3 waits on N-2 → owner", true),
		item(20, "approval.requested", "notes", "N-3", "r-mgr-3", "manager", "manager", "factory_release_publish: release 1.2 is approved by review", true),
		item(12, "task.started", "calorie", "CAL-3", "r-ios-7", "calorie-ios", "", "Build the meal entry screen", false),
		item(9, "run.command_refused", "calorie", "CAL-3", "r-ios-7", "calorie-ios", "", "git push --force refused by rule no-force-push", false),
		item(7, "node.low_disk", "", "", "", "", "node", "mac-mini: 4.1 GiB free on /", true),
		item(4, "task.started", "notes", "N-1", "r-rev-2", "reviewer", "", "Markdown export", false),
		item(2, "task.commented", "notes", "N-1", "", "reviewer", "owner", "Please check the export on a 10k-line note.", false),
	}
	f.Feed[19].EntityID = "agent:calorie-designer"
	f.Feed[27].EntityID = "node:mac-mini"

	// Runs and their events.
	run := func(id, agent, taskID, project, status, outcome string, startMin, endMin int, result string, usage storage.UsageList) storage.Run {
		r := storage.Run{Spec: protocol.RunSpec{ID: id, Agent: agent, TaskID: taskID, Project: project}, Status: status, Outcome: outcome, StartedAt: unix(startMin), Result: result, Usage: usage}
		if status == "ENDED" {
			r.FinishedAt = unix(endMin)
		}
		return r
	}
	opus := func(in, out int64, cost float64) storage.UsageList {
		return storage.UsageList{{Model: "claude-opus-4-7", Input: in, Output: out, CostUSD: cost, CostSource: "claude_estimate"}}
	}
	f.Runs["r-ios-6"] = run("r-ios-6", "calorie-ios", "CAL-3", "calorie", "ENDED", "failed", 100, 81, "xcodebuild: the simulator did not boot", opus(48000, 6200, 0.61))
	f.Runs["r-ios-7"] = run("r-ios-7", "calorie-ios", "CAL-3", "calorie", "RUNNING", "", 12, 0, "", opus(91000, 9800, 1.23))
	f.Runs["r-des-3"] = run("r-des-3", "calorie-designer", "CAL-2", "calorie", "ENDED", "succeeded", 240, 210, "mockups ready", opus(30000, 4100, 0.55))
	f.Runs["r-rev-2"] = run("r-rev-2", "reviewer", "N-1", "notes", "RUNNING", "", 4, 0, "", storage.UsageList{{Model: "gpt-5.5", Input: 22000, Output: 1800}})

	ev := func(id string, m int, kind string, p protocol.Event) RunEvent {
		p.RunID = id
		e := protocol.Wrap(kind, id, "", p)
		e.ID, e.At = fmt.Sprintf("%s-%d", id, len(f.RunEvents[id])+1), ago(m)
		return RunEvent{Seq: int64(len(f.RunEvents[id]) + 1), Envelope: e}
	}
	add := func(id string, m int, kind string, p protocol.Event) {
		f.RunEvents[id] = append(f.RunEvents[id], ev(id, m, kind, p))
	}
	claude := func(id string, m int, method string, data any) {
		b, _ := json.Marshal(data)
		add(id, m, "agent.progress", protocol.Event{Method: method, Data: b})
	}
	text := func(s string) map[string]any {
		return map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": s}}}}
	}
	toolUse := func(name string, input map[string]any) map[string]any {
		return map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": "tu-" + name, "name": name, "input": input}}}}
	}
	toolError := func(s string) map[string]any {
		return map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "is_error": true, "content": s}}}}
	}
	claude("r-ios-7", 12, "claude/system/init", map[string]any{"type": "system", "subtype": "init", "model": "claude-opus-4-7"})
	claude("r-ios-7", 12, "claude/assistant", text("Resuming CAL-3. The owner asked for offline-first; I'll check the store layer first."))
	claude("r-ios-7", 11, "claude/assistant", toolUse("mcp__factory__factory_task_get", map[string]any{"task": "CAL-2"}))
	claude("r-ios-7", 11, "claude/assistant", toolUse("Read", map[string]any{"file_path": "Calorie/MealEntry/MealStore.swift"}))
	claude("r-ios-7", 10, "claude/assistant", toolUse("Edit", map[string]any{"file_path": "Calorie/MealEntry/MealStore.swift"}))
	claude("r-ios-7", 10, "claude/assistant", toolUse("Bash", map[string]any{"command": "xcodebuild -scheme Calorie -destination 'platform=iOS Simulator,name=iPhone 16' test"}))
	claude("r-ios-7", 9, "claude/assistant", toolUse("Bash", map[string]any{"command": "git push --force origin af/CAL-3"}))
	b, _ := json.Marshal(map[string]any{"argv": []string{"git", "push", "--force", "origin", "af/CAL-3"}, "effect": "deny", "rule": "no-force-push"})
	add("r-ios-7", 9, "agent.progress", protocol.Event{Method: "factory/command_refused", Data: b})
	claude("r-ios-7", 9, "claude/user", toolError("command refused by policy no-force-push"))
	claude("r-ios-7", 8, "claude/assistant", text("Force push is not allowed; pushing a new commit instead."))
	claude("r-ios-7", 8, "claude/assistant", toolUse("Bash", map[string]any{"command": "git push origin af/CAL-3"}))
	claude("r-ios-7", 6, "claude/assistant", toolUse("mcp__factory__factory_message_send", map[string]any{"recipient": "calorie-manager", "body": "Portion picker done in grams; starting offline cache."}))
	claude("r-ios-7", 3, "claude/assistant", text("All 42 tests pass. Submitting for review."))
	claude("r-ios-7", 2, "claude/assistant", toolUse("mcp__factory__factory_exit_submit", map[string]any{"summary": "meal entry screen, offline-first store, 42 tests", "reviewer": "calorie-manager"}))
	add("r-ios-7", 2, "agent.progress", protocol.Event{Method: "factory/exit_guard", Data: json.RawMessage(`{"refusal":1,"reason":"submit needs a pushed branch ahead of main"}`)})
	f.ToolCalls["r-ios-7"] = []storage.ToolCall{
		{ID: "tc1", RunID: "r-ios-7", Agent: "calorie-ios", ProjectID: "calorie", Tool: "factory_task_get", Status: 200, CreatedAt: unix(11)},
		{ID: "tc2", RunID: "r-ios-7", Agent: "calorie-ios", ProjectID: "calorie", Tool: "factory_message_send", Status: 200, CreatedAt: unix(6)},
		{ID: "tc3", RunID: "r-ios-7", Agent: "calorie-ios", ProjectID: "calorie", Tool: "factory_exit_submit", Status: 409, CreatedAt: unix(2)},
	}
	codex := func(id string, m int, item map[string]any) {
		b, _ := json.Marshal(map[string]any{"item": item})
		add(id, m, "agent.progress", protocol.Event{Method: "item/completed", Data: b})
	}
	codex("r-rev-2", 4, map[string]any{"type": "agentMessage", "text": "Reviewing the second submission of N-1."})
	codex("r-rev-2", 3, map[string]any{"type": "commandExecution", "command": "git diff main...af/N-1 --stat", "exitCode": 0})
	codex("r-rev-2", 3, map[string]any{"type": "commandExecution", "command": "go test ./export/...", "exitCode": 0})
	codex("r-rev-2", 2, map[string]any{"type": "mcpToolCall", "server": "factory", "tool": "factory_task_comments", "arguments": map[string]any{"task": "N-1"}})
	codex("r-rev-2", 1, map[string]any{"type": "agentMessage", "text": "Testing the export on a 10k-line note as the owner asked."})
	claude("r-des-3", 240, "claude/assistant", text("Drafting the meal entry mockups."))
	claude("r-des-3", 212, "claude/assistant", toolUse("mcp__factory__factory_exit_done", map[string]any{"summary": "meal entry mockups in Figma v3"}))
	add("r-des-3", 210, "agent.completed", protocol.Event{Status: "ENDED", Outcome: "succeeded", ExitState: "accepted", Result: "mockups ready"})
	claude("r-ios-6", 100, "claude/assistant", toolUse("Bash", map[string]any{"command": "xcrun simctl boot 'iPhone 16'"}))
	add("r-ios-6", 81, "agent.completed", protocol.Event{Status: "ENDED", Outcome: "failed", ExitState: "none", Result: "xcodebuild: the simulator did not boot"})

	// The detail of CAL-3: attempts, costs, comments and its events.
	cal3 := TaskDetail{Task: ts[2], Attempts: []storage.Run{f.Runs["r-ios-6"], f.Runs["r-ios-7"]},
		Why:      tasks.Verdict{TaskID: "CAL-3", Status: "IN_PROGRESS", Reason: "live_run", Detail: "run r-ios-7 is running", Clearer: tasks.Clearer{Who: "calorie-ios", Action: "wait for the run"}, Pending: []tasks.Trigger{{Seq: 9, Kind: "directive", Actor: "owner", Authority: true}}},
		Comments: []storage.Comment{{ID: "c1", Author: "calorie-manager", Text: "Grams only for v1; ounces later.", CreatedAt: unix(150)}, {ID: "c2", Author: "owner", Text: "Keep the entry screen offline-first.", CreatedAt: unix(70)}},
		Costs:    storage.CostReport{Runs: []storage.RunCost{{RunID: "r-ios-6", TaskID: "CAL-3", Agent: "calorie-ios", Outcome: "failed", CostUSD: 0.61, CostSource: "reported"}}, Total: storage.CostGroup{Key: "total", Runs: 1, CostUSD: 0.61}},
	}
	for _, it := range f.Feed {
		if it.TaskID == "CAL-3" {
			cal3.Events = append(cal3.Events, it)
		}
	}
	f.Details["CAL-3"] = cal3
	return f
}

func demoNodes(at time.Time) []Node {
	health := func(load float64, free uint64, failing ...string) json.RawMessage {
		checks := []map[string]string{}
		for _, c := range failing {
			checks = append(checks, map[string]string{"check": c, "status": "fail"})
		}
		b, _ := json.Marshal(map[string]any{"load1": load, "free_disk_bytes": free, "disk": "/", "checks": checks})
		return b
	}
	return []Node{
		{Node: storage.Node{NodeRegistration: storage.NodeRegistration{NodeID: "n1", Name: "mac-studio", Epoch: 4, ProtocolVersion: 2, Slots: 4,
			Drivers: []storage.NodeDriver{{Name: "codex", OK: true}, {Name: "claude", OK: true}}, Resources: map[string]int{"simulator": 2}},
			Health: health(1.2, 120<<30), LastSeen: at.Add(-5 * time.Second).Unix(), Online: true}, InUse: storage.NodeUse{Runs: 2, Resources: map[string]int{"simulator": 1}}},
		{Node: storage.Node{NodeRegistration: storage.NodeRegistration{NodeID: "n2", Name: "mac-mini", Epoch: 9, ProtocolVersion: 2, Slots: 2,
			Drivers: []storage.NodeDriver{{Name: "codex", OK: true}}}, Health: health(0.1, 4<<30+100<<20, "disk"), LastSeen: at.Add(-7 * time.Minute).Unix(), Online: false},
			InUse: storage.NodeUse{Resources: map[string]int{}}},
	}
}

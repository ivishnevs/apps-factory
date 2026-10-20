package tui

import (
	"encoding/json"
	"fmt"
	"sort"
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

// DemoFixture is a small company at work: three projects, two teams under the owner, three live runs, a
// question and an approval waiting on the owner, an escalation, a paused agent and an offline node.
// factory ui --demo renders it.
func DemoFixture() Fixture {
	at := DemoAt
	ago := func(m int) time.Time { return at.Add(-time.Duration(m) * time.Minute) }
	unix := func(m int) int64 { return ago(m).Unix() }
	f := Fixture{Name: "demo", Details: map[string]TaskDetail{}, Runs: map[string]storage.Run{}, RunEvents: map[string][]RunEvent{}, ToolCalls: map[string][]storage.ToolCall{}}

	agents := []Agent{
		{Name: "calorie-designer", Role: "designer", ReportsTo: "calorie-manager", Provider: "claude", Model: "claude-opus-4-7"},
		{Name: "calorie-ios", Role: "iOS engineer", ReportsTo: "calorie-manager", Provider: "claude", Model: "claude-opus-4-7"},
		{Name: "calorie-manager", Role: "product lead", ReportsTo: "owner", Provider: "codex", Model: "gpt-5.5"},
		{Name: "calorie-qa", Role: "QA", ReportsTo: "calorie-manager", Provider: "codex", Model: "gpt-5.5"},
		{Name: "demo", Role: "engineer", ReportsTo: "manager", Provider: "codex", Model: "gpt-5.5"},
		{Name: "manager", Role: "engineering manager", ReportsTo: "owner", Provider: "codex", Model: "gpt-5.5"},
		{Name: "reviewer", Role: "code reviewer", ReportsTo: "manager", Provider: "codex", Model: "gpt-5.5"},
	}
	type spec struct {
		id, project, title, status, assignee, by, origin string
		prio, created, updated                           int
		reason, who, action                              string
	}
	specs := []spec{
		{"CAL-1", "calorie", "Ship calorie calculator v1", "READY", "calorie-manager", "owner", "", 9, 300, 40, "parked:children", "calorie-manager", "wait for CAL-3, CAL-4, CAL-5"},
		{"CAL-2", "calorie", "Design the meal entry screen", "DONE", "calorie-designer", "calorie-manager", "CAL-1", 7, 280, 210, "terminal", "nobody", "final state"},
		{"CAL-3", "calorie", "Build the meal entry screen", "IN_PROGRESS", "calorie-ios", "calorie-manager", "CAL-1", 7, 270, 12, "live_run", "calorie-ios", "run r-ios-7 is running"},
		{"CAL-4", "calorie", "Test meal entry on iPhone SE", "READY", "calorie-qa", "calorie-manager", "CAL-1", 6, 265, 265, "dependencies", "calorie-ios", "finish CAL-3"},
		{"CAL-5", "calorie", "Pick the nutrition data API", "READY", "calorie-manager", "calorie-manager", "CAL-1", 8, 200, 95, "parked:ask", "owner", "answer m-ask-1 in the inbox"},
		{"CAL-6", "calorie", "App Store screenshots", "BACKLOG", "calorie-designer", "owner", "", 2, 150, 150, "backlog", "owner", "release it to the board"},
		{"CAL-7", "calorie", "Barcode scanner spike", "READY", "calorie-ios", "calorie-manager", "CAL-1", 5, 180, 180, "capacity", "calorie-ios", "busy with CAL-3"},
		{"CAL-8", "calorie", "Onboarding copy", "DONE", "calorie-designer", "calorie-manager", "CAL-1", 4, 260, 230, "terminal", "nobody", "final state"},
		{"CAL-9", "calorie", "Crash on empty meal list", "READY", "calorie-qa", "calorie-ios", "CAL-3", 8, 25, 25, "runnable", "factory", "dispatch on the next tick"},
		{"N-1", "notes", "Markdown export", "IN_REVIEW", "reviewer", "manager", "N-3", 6, 240, 4, "live_run", "reviewer", "run r-rev-2 is running"},
		{"N-2", "notes", "Fix the sync conflict on rename", "READY", "demo", "manager", "N-3", 7, 230, 45, "backoff", "nobody", "retry at 14:34"},
		{"N-3", "notes", "Release notes app 1.2", "READY", "manager", "owner", "", 9, 320, 30, "parked:escalation", "owner", "answer m-esc-1 (unblock or cancel)"},
		{"N-4", "notes", "Dark mode icons", "BACKLOG", "calorie-designer", "manager", "", 3, 200, 200, "backlog", "owner", "release it to the board"},
		{"N-5", "notes", "Sync settings across devices", "CANCELLED", "demo", "manager", "N-3", 4, 400, 300, "terminal", "nobody", "final state"},
		{"N-6", "notes", "Full-text search index", "DONE", "demo", "manager", "N-3", 6, 500, 260, "terminal", "nobody", "final state"},
		{"W-1", "website", "Landing page copy", "DONE", "calorie-designer", "manager", "", 6, 600, 400, "terminal", "nobody", "final state"},
		{"W-2", "website", "Pricing page", "IN_PROGRESS", "demo", "manager", "W-1", 8, 120, 7, "live_run", "demo", "run r-web-4 is running"},
		{"W-3", "website", "SEO audit of the landing page", "READY", "reviewer", "manager", "W-1", 5, 110, 110, "capacity", "reviewer", "busy with N-1"},
		{"W-4", "website", "Blog engine", "BACKLOG", "demo", "owner", "", 3, 90, 90, "backlog", "owner", "release it to the board"},
		{"W-5", "website", "Cookie consent banner", "BLOCKED", "demo", "manager", "W-1", 6, 100, 50, "parked:block", "manager", "send the legal text"},
		{"W-6", "website", "Newsletter signup form", "IN_REVIEW", "reviewer", "manager", "W-1", 5, 85, 15, "capacity", "reviewer", "busy with N-1"},
	}
	ts := []Task{}
	for _, s := range specs {
		ts = append(ts, Task{Task: tasks.Task{ID: s.id, ProjectID: s.project, Title: s.title, Status: s.status, Assignee: s.assignee, CreatedBy: s.by, OriginTask: s.origin,
			Priority: s.prio, CreatedAt: unix(s.created), UpdatedAt: unix(s.updated), Version: 3, Dependencies: []string{}}, Reason: s.reason, Clearer: tasks.Clearer{Who: s.who, Action: s.action}})
	}
	byID := func(id string) *Task {
		for i := range ts {
			if ts[i].ID == id {
				return &ts[i]
			}
		}
		panic(id)
	}
	byID("CAL-3").Dependencies, byID("CAL-3").CurrentRunID, byID("CAL-3").Attempt = []string{"CAL-2"}, "r-ios-7", 2
	byID("CAL-4").Dependencies = []string{"CAL-3"}
	byID("N-1").Implementer, byID("N-1").Reviewer, byID("N-1").CurrentRunID = "demo", "reviewer", "r-rev-2"
	byID("W-2").CurrentRunID = "r-web-4"
	byID("W-6").Implementer, byID("W-6").Reviewer = "demo", "reviewer"
	byID("CAL-2").Description = "Mockups for the meal entry screen: portion picker, recent meals, barcode button."
	byID("CAL-3").Description = "Implement the meal entry screen from the CAL-2 mockups.\n\nAcceptance:\n- portion picker in grams\n- works offline\n- unit tests for the calorie sum"
	byID("W-2").Description = "Three tiers (Free, Pro, Team) with a monthly/yearly toggle; copy from W-1."
	ts = append(ts, Task{Task: tasks.Task{ID: "conv-notes-manager", ProjectID: "notes", Title: "Conversation with owner", Status: "READY", Assignee: "manager", Kind: "conversation", Counterpart: "owner", Version: 1, CreatedAt: unix(400)}, Reason: "idle", Clearer: tasks.Clearer{Who: "nobody", Action: "waits for a message"}})

	runs := map[string][]core.RunStatus{
		"calorie": {{ID: "r-ios-7", Agent: "calorie-ios", TaskID: "CAL-3", Status: "RUNNING", StartedAt: unix(12)}},
		"notes":   {{ID: "r-rev-2", Agent: "reviewer", TaskID: "N-1", Status: "RUNNING", StartedAt: unix(4)}},
		"website": {{ID: "r-web-4", Agent: "demo", TaskID: "W-2", Status: "RUNNING", StartedAt: unix(7)}},
	}
	names := map[string]string{"calorie": "Calorie Calculator", "notes": "Notes", "website": "Website"}
	status := []core.ProjectStatus{}
	for _, p := range []string{"calorie", "notes", "website"} {
		st := core.ProjectStatus{Project: tasks.Project{ID: p, Name: names[p]}, Runs: runs[p], Tasks: map[string]int{}, Waiting: []tasks.Verdict{}}
		for _, t := range ts {
			if t.ProjectID != p || t.Kind != "" {
				continue
			}
			st.Tasks[t.Status]++
			switch {
			case t.Reason == "runnable":
				st.Runnable++
			case t.Reason != "live_run" && t.Reason != "terminal":
				st.Waiting = append(st.Waiting, tasks.Verdict{TaskID: t.ID, Status: t.Status, Reason: t.Reason, Clearer: t.Clearer})
			}
		}
		status = append(status, st)
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

	// Cost history: the runs that ended in the last 24 h.
	recent := []storage.RunCost{}
	taskCost := map[string]float64{}
	for i, rc := range []struct {
		agent, task string
		min         int
		cost        float64
	}{
		{"calorie-designer", "CAL-8", 1300, 0.22}, {"demo", "N-6", 1250, 0.48}, {"calorie-designer", "W-1", 1100, 0.31}, {"demo", "N-6", 980, 0.36},
		{"manager", "N-3", 900, 0.09}, {"calorie-designer", "CAL-2", 700, 0.41}, {"calorie-manager", "CAL-1", 640, 0.05}, {"calorie-ios", "CAL-3", 420, 0.88},
		{"demo", "N-2", 300, 0.27}, {"calorie-ios", "CAL-3", 240, 0.95}, {"reviewer", "N-1", 200, 0.19}, {"demo", "N-1", 150, 0.52},
		{"demo", "N-2", 120, 0.31}, {"calorie-ios", "CAL-3", 81, 0.61}, {"reviewer", "N-1", 110, 0.12}, {"demo", "N-2", 60, 0.29},
		{"calorie-manager", "CAL-5", 96, 0.07}, {"manager", "N-3", 25, 0.04}, {"demo", "N-1", 55, 0.44},
	} {
		recent = append(recent, storage.RunCost{RunID: fmt.Sprintf("r-old-%d", i), TaskID: rc.task, Agent: rc.agent, Outcome: "succeeded", CostUSD: rc.cost, CostSource: "reported", RecordedAt: unix(rc.min)})
		taskCost[rc.task] += rc.cost
	}
	sort.Slice(recent, func(i, j int) bool { return recent[i].RecordedAt < recent[j].RecordedAt })
	agentCost := map[string]storage.CostGroup{}
	for _, r := range recent {
		if r.RecordedAt >= time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC).Unix() {
			g := agentCost[r.Agent]
			g.Key, g.Runs, g.CostUSD = r.Agent, g.Runs+1, g.CostUSD+r.CostUSD
			agentCost[r.Agent] = g
		}
	}
	costs := storage.CostReport{Total: storage.CostGroup{Key: "total", Sources: map[string]int{}}}
	for _, a := range agents {
		if g, ok := agentCost[a.Name]; ok {
			costs.Groups = append(costs.Groups, g)
			costs.Total.Runs += g.Runs
			costs.Total.CostUSD += g.CostUSD
		}
	}
	taskCosts := storage.CostReport{}
	for id, c := range taskCost {
		taskCosts.Groups = append(taskCosts.Groups, storage.CostGroup{Key: id, CostUSD: c})
	}
	sort.Slice(taskCosts.Groups, func(i, j int) bool { return taskCosts.Groups[i].Key < taskCosts.Groups[j].Key })

	// Runs and their events.
	run := func(id, agent, taskID, project, status, outcome string, startMin, endMin int, result string, usage storage.UsageList) storage.Run {
		r := storage.Run{Spec: protocol.RunSpec{ID: id, Agent: agent, TaskID: taskID, Project: project}, Status: status, Outcome: outcome, StartedAt: unix(startMin), Result: result, Usage: usage}
		if status == "ENDED" {
			r.FinishedAt = unix(endMin)
		}
		return r
	}
	opus := func(in, out, cache int64, cost float64) storage.UsageList {
		return storage.UsageList{{Model: "claude-opus-4-7", Input: in, Output: out, CacheRead: cache, CacheWrite: cache / 5, CostUSD: cost, CostSource: "claude_estimate"}}
	}
	gpt := func(in, out, cache int64) storage.UsageList {
		return storage.UsageList{{Model: "gpt-5.5", Input: in, Output: out, CacheRead: cache}}
	}
	f.Runs["r-ios-6"] = run("r-ios-6", "calorie-ios", "CAL-3", "calorie", "ENDED", "failed", 100, 81, "xcodebuild: the simulator did not boot", opus(48000, 6200, 21000, 0.61))
	f.Runs["r-ios-7"] = run("r-ios-7", "calorie-ios", "CAL-3", "calorie", "RUNNING", "", 12, 0, "", opus(45230, 12891, 23102, 1.24))
	f.Runs["r-des-3"] = run("r-des-3", "calorie-designer", "CAL-2", "calorie", "ENDED", "succeeded", 240, 210, "mockups ready", opus(30000, 4100, 9000, 0.41))
	f.Runs["r-rev-2"] = run("r-rev-2", "reviewer", "N-1", "notes", "RUNNING", "", 4, 0, "", gpt(22000, 1800, 64000))
	f.Runs["r-web-4"] = run("r-web-4", "demo", "W-2", "website", "RUNNING", "", 7, 0, "", gpt(81000, 7400, 152000))

	ev := func(id string, sec int, kind string, p protocol.Event) RunEvent {
		p.RunID = id
		e := protocol.Wrap(kind, id, "", p)
		e.ID, e.At = fmt.Sprintf("%s-%d", id, len(f.RunEvents[id])+1), at.Add(-time.Duration(sec)*time.Second)
		return RunEvent{Seq: int64(len(f.RunEvents[id]) + 1), Envelope: e}
	}
	add := func(id string, sec int, kind string, p protocol.Event) {
		f.RunEvents[id] = append(f.RunEvents[id], ev(id, sec, kind, p))
	}
	claude := func(id string, sec int, method string, data any) {
		b, _ := json.Marshal(data)
		add(id, sec, "agent.progress", protocol.Event{Method: method, Data: b})
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
	claude("r-ios-7", 720, "claude/system/init", map[string]any{"type": "system", "subtype": "init", "model": "claude-opus-4-7"})
	claude("r-ios-7", 715, "claude/assistant", text("Resuming CAL-3. The owner asked for offline-first, so I'll start from the store layer."))
	claude("r-ios-7", 690, "claude/assistant", toolUse("mcp__factory__factory_task_get", map[string]any{"task": "CAL-2"}))
	claude("r-ios-7", 660, "claude/assistant", toolUse("Read", map[string]any{"file_path": "Calorie/MealEntry/MealStore.swift"}))
	claude("r-ios-7", 600, "claude/assistant", toolUse("Edit", map[string]any{"file_path": "Calorie/MealEntry/MealStore.swift"}))
	claude("r-ios-7", 560, "claude/assistant", toolUse("Bash", map[string]any{"command": "xcodebuild -scheme Calorie -destination 'platform=iOS Simulator,name=iPhone 16' test"}))
	claude("r-ios-7", 520, "claude/assistant", toolUse("Bash", map[string]any{"command": "git push --force origin af/CAL-3"}))
	b, _ := json.Marshal(map[string]any{"argv": []string{"git", "push", "--force", "origin", "af/CAL-3"}, "effect": "deny", "rule": "no-force-push"})
	add("r-ios-7", 519, "agent.progress", protocol.Event{Method: "factory/command_refused", Data: b})
	claude("r-ios-7", 518, "claude/user", toolError("command refused by policy no-force-push"))
	claude("r-ios-7", 480, "claude/assistant", text("Force push is not allowed; pushing a new commit instead."))
	claude("r-ios-7", 470, "claude/assistant", toolUse("Bash", map[string]any{"command": "git push origin af/CAL-3"}))
	claude("r-ios-7", 380, "claude/assistant", toolUse("mcp__factory__factory_message_send", map[string]any{"recipient": "calorie-manager", "body": "Portion picker done in grams; starting the offline cache."}))
	claude("r-ios-7", 300, "claude/assistant", toolUse("Grep", map[string]any{"pattern": "refreshMeals", "path": "Calorie/**"}))
	claude("r-ios-7", 150, "claude/assistant", text("All 42 tests pass. Submitting for review."))
	claude("r-ios-7", 120, "claude/assistant", toolUse("mcp__factory__factory_exit_submit", map[string]any{"summary": "meal entry screen, offline-first store, 42 tests", "reviewer": "calorie-manager"}))
	add("r-ios-7", 118, "agent.progress", protocol.Event{Method: "factory/exit_guard", Data: json.RawMessage(`{"refusal":1,"reason":"submit needs a pushed branch ahead of main"}`)})
	claude("r-ios-7", 40, "claude/assistant", toolUse("Bash", map[string]any{"command": "swift test --filter MealStoreTests"}))
	f.ToolCalls["r-ios-7"] = []storage.ToolCall{
		{ID: "tc1", RunID: "r-ios-7", Agent: "calorie-ios", ProjectID: "calorie", Tool: "factory_task_get", Status: 200, CreatedAt: at.Add(-690 * time.Second).Unix()},
		{ID: "tc2", RunID: "r-ios-7", Agent: "calorie-ios", ProjectID: "calorie", Tool: "factory_message_send", Status: 200, CreatedAt: at.Add(-380 * time.Second).Unix()},
		{ID: "tc3", RunID: "r-ios-7", Agent: "calorie-ios", ProjectID: "calorie", Tool: "factory_exit_submit", Status: 409, CreatedAt: at.Add(-119 * time.Second).Unix()},
	}
	codex := func(id string, sec int, item map[string]any) {
		b, _ := json.Marshal(map[string]any{"item": item})
		add(id, sec, "agent.progress", protocol.Event{Method: "item/completed", Data: b})
	}
	codex("r-rev-2", 240, map[string]any{"type": "agentMessage", "text": "Reviewing the second submission of N-1."})
	codex("r-rev-2", 200, map[string]any{"type": "commandExecution", "command": "git diff main...af/N-1 --stat", "exitCode": 0})
	codex("r-rev-2", 150, map[string]any{"type": "commandExecution", "command": "go test ./export/...", "exitCode": 0})
	codex("r-rev-2", 90, map[string]any{"type": "mcpToolCall", "server": "factory", "tool": "factory_task_comments", "arguments": map[string]any{"task": "N-1"}})
	codex("r-rev-2", 30, map[string]any{"type": "agentMessage", "text": "Testing the export on a 10k-line note as the owner asked."})
	codex("r-web-4", 420, map[string]any{"type": "agentMessage", "text": "Building the pricing page from the W-1 copy."})
	codex("r-web-4", 380, map[string]any{"type": "fileChange", "changes": []any{map[string]any{"path": "src/pages/pricing.tsx"}, map[string]any{"path": "src/components/TierCard.tsx"}}})
	codex("r-web-4", 300, map[string]any{"type": "commandExecution", "command": "npm run build", "exitCode": 0})
	codex("r-web-4", 200, map[string]any{"type": "mcpToolCall", "server": "factory", "tool": "factory_message_send", "arguments": map[string]any{"recipient": "manager", "body": "Yearly discount: 15% or 20%?"}})
	codex("r-web-4", 60, map[string]any{"type": "commandExecution", "command": "npx playwright test pricing.spec.ts", "exitCode": 0})
	codex("r-web-4", 15, map[string]any{"type": "fileChange", "changes": []any{map[string]any{"path": "src/pages/pricing.tsx"}}})
	claude("r-des-3", 14400, "claude/assistant", text("Drafting the meal entry mockups."))
	claude("r-des-3", 12720, "claude/assistant", toolUse("mcp__factory__factory_exit_done", map[string]any{"summary": "meal entry mockups in Figma v3"}))
	add("r-des-3", 12600, "agent.completed", protocol.Event{Status: "ENDED", Outcome: "succeeded", ExitState: "accepted", Result: "mockups ready"})
	claude("r-ios-6", 6000, "claude/assistant", toolUse("Bash", map[string]any{"command": "xcrun simctl boot 'iPhone 16'"}))
	add("r-ios-6", 4860, "agent.completed", protocol.Event{Status: "ENDED", Outcome: "failed", ExitState: "none", Result: "xcodebuild: the simulator did not boot"})

	f.Overview = Overview{
		Health: Health{Status: "ok", Database: "postgres 17.6", Company: "acme", ExplicitExits: "on"},
		Status: status, Agents: agents, Tasks: ts,
		Pauses:    []storage.Pause{{Scope: "agent", Target: "calorie-designer", Reason: "waiting for the brand guide", Actor: "owner", CreatedAt: unix(60)}},
		Pending:   storage.PendingCounts{Inbox: 3, Approvals: 1, Alerts: 1, Reviews: 2, Total: 4},
		Nodes:     demoNodes(at),
		Inbox:     []messaging.Message{ask, appr, esc},
		Approvals: []approvals.Approval{approval},
		Costs:     costs, TaskCosts: taskCosts, RecentCosts: storage.CostReport{Runs: recent},
		LiveRuns: map[string]storage.Run{"r-ios-7": f.Runs["r-ios-7"], "r-rev-2": f.Runs["r-rev-2"], "r-web-4": f.Runs["r-web-4"]},
		At:       at,
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
		item(600, "task.created", "website", "W-1", "", "calorie-designer", "manager", "Landing page copy", false),
		item(500, "task.created", "notes", "N-6", "", "demo", "manager", "Full-text search index", false),
		item(400, "task.exit.done", "website", "W-1", "r-des-1", "calorie-designer", "calorie-designer", "calorie-designer done: landing copy v2 in the repo", false),
		item(320, "task.created", "notes", "N-3", "", "manager", "owner", "Release notes app 1.2", false),
		item(300, "task.created", "calorie", "CAL-1", "", "calorie-manager", "owner", "Ship calorie calculator v1", false),
		item(280, "task.created", "calorie", "CAL-2", "", "calorie-designer", "calorie-manager", "Design the meal entry screen", false),
		item(270, "task.created", "calorie", "CAL-3", "", "calorie-ios", "calorie-manager", "Build the meal entry screen", false),
		item(265, "task.created", "calorie", "CAL-4", "", "calorie-qa", "calorie-manager", "Test meal entry on iPhone SE", false),
		item(260, "task.exit.done", "notes", "N-6", "r-demo-2", "demo", "demo", "demo done: search index with 40 ms p95", false),
		item(240, "task.created", "notes", "N-1", "", "demo", "manager", "Markdown export", false),
		item(230, "task.created", "notes", "N-2", "", "demo", "manager", "Fix the sync conflict on rename", false),
		item(210, "task.exit.done", "calorie", "CAL-2", "r-des-3", "calorie-designer", "calorie-designer", "calorie-designer done: meal entry mockups in Figma v3", false),
		item(209, "task.trigger.dependency_done", "calorie", "CAL-3", "", "calorie-ios", "calorie-designer", "CAL-2 done", false),
		item(200, "task.created", "calorie", "CAL-5", "", "calorie-manager", "calorie-manager", "Pick the nutrition data API", false),
		item(180, "task.created", "calorie", "CAL-7", "", "calorie-ios", "calorie-manager", "Barcode scanner spike", false),
		item(160, "message.created", "calorie", "CAL-3", "", "", "calorie-ios", "calorie-ios → calorie-manager (question): Should the portion picker allow grams and ounces?", false),
		item(150, "message.created", "calorie", "CAL-3", "", "", "calorie-manager", "calorie-manager → calorie-ios (answer): Grams only for v1.", false),
		item(120, "task.created", "website", "W-2", "", "demo", "manager", "Pricing page", false),
		item(119, "task.exit.submitted", "notes", "N-1", "r-demo-5", "demo", "demo", "demo → reviewer (submit): markdown export with round-trip tests", false),
		item(110, "task.exit.handoff", "notes", "N-1", "r-rev-1", "reviewer", "reviewer", "reviewer → demo (changes_requested): escape pipes in tables", false),
		item(108, "task.created", "website", "W-3", "", "reviewer", "manager", "SEO audit of the landing page", false),
		item(100, "task.exit.asked", "calorie", "CAL-5", "r-cm-2", "calorie-manager", "calorie-manager", "calorie-manager asked: Which nutrition data API may we pay for?", false),
		item(95, "message.created", "calorie", "CAL-5", "", "", "calorie-manager", "calorie-manager → owner (question): Which nutrition data API may we pay for? USDA is free but has no barcodes.", true),
		item(95, "task.parked.ask", "calorie", "CAL-5", "", "calorie-manager", "", "waits for the answer to m-ask-1", false),
		item(85, "task.exit.submitted", "website", "W-6", "r-web-3", "demo", "demo", "demo → reviewer (submit): signup form with double opt-in", false),
		item(80, "task.retry", "calorie", "CAL-3", "r-ios-6", "calorie-ios", "", "CAL-3: run r-ios-6 ended failed; retry in 1m0s", false),
		item(70, "task.trigger.directive", "calorie", "CAL-3", "", "calorie-ios", "owner", "Keep the entry screen offline-first.", false),
		item(60, "owner.paused", "", "", "", "", "owner", "agent calorie-designer: waiting for the brand guide", true),
		item(55, "task.exit.submitted", "notes", "N-1", "r-demo-6", "demo", "demo", "demo → reviewer (submit): pipes escaped, tests added", false),
		item(50, "task.exit.blocked", "website", "W-5", "r-web-2", "demo", "demo", "demo blocked: needs the legal text for the banner", false),
		item(45, "task.alert.no_progress", "notes", "N-2", "", "demo", "", "N-2: 3 runs without progress", true),
		item(40, "task.exit.handoff", "notes", "N-3", "r-mgr-2", "manager", "manager", "manager → demo (handoff): rebase N-2 on main before retrying", false),
		item(30, "task.escalation.stalled", "notes", "N-3", "", "manager", "", "N-3 waits on N-2 → owner", true),
		item(25, "task.created", "calorie", "CAL-9", "", "calorie-qa", "calorie-ios", "Crash on empty meal list", false),
		item(20, "approval.requested", "notes", "N-3", "r-mgr-3", "manager", "manager", "factory_release_publish: release 1.2 is approved by review", true),
		item(12, "task.started", "calorie", "CAL-3", "r-ios-7", "calorie-ios", "", "Build the meal entry screen", false),
		item(9, "run.command_refused", "calorie", "CAL-3", "r-ios-7", "calorie-ios", "", "git push --force refused by rule no-force-push", false),
		item(7, "task.started", "website", "W-2", "r-web-4", "demo", "", "Pricing page", false),
		item(6, "node.low_disk", "", "", "", "", "node", "mac-mini: 4.1 GiB free on /", true),
		item(4, "task.started", "notes", "N-1", "r-rev-2", "reviewer", "", "Markdown export", false),
		item(3, "message.created", "website", "W-2", "", "", "demo", "demo → manager (question): Yearly discount: 15% or 20%?", false),
		item(2, "task.commented", "notes", "N-1", "", "reviewer", "owner", "Please check the export on a 10k-line note.", false),
		item(1, "message.created", "website", "W-2", "", "", "manager", "manager → demo (answer): 20%, rounded to whole dollars.", false),
	}
	for i := range f.Feed {
		switch f.Feed[i].Type {
		case "owner.paused":
			f.Feed[i].EntityID = "agent:calorie-designer"
		case "node.low_disk":
			f.Feed[i].EntityID = "node:mac-mini"
		}
	}

	// The detail of CAL-3: attempts, costs, comments and its events.
	cal3 := TaskDetail{Task: *byID("CAL-3"), Attempts: []storage.Run{f.Runs["r-ios-6"], f.Runs["r-ios-7"]},
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
			Health: health(1.2, 120<<30), LastSeen: at.Add(-5 * time.Second).Unix(), Online: true}, InUse: storage.NodeUse{Runs: 3, Resources: map[string]int{"simulator": 1}}},
		{Node: storage.Node{NodeRegistration: storage.NodeRegistration{NodeID: "n2", Name: "mac-mini", Epoch: 9, ProtocolVersion: 2, Slots: 2,
			Drivers: []storage.NodeDriver{{Name: "codex", OK: true}}}, Health: health(0.1, 4<<30+100<<20, "disk"), LastSeen: at.Add(-7 * time.Minute).Unix(), Online: false},
			InUse: storage.NodeUse{Resources: map[string]int{}}},
	}
}

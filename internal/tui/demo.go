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

// DemoFixture is a small company at work: three projects, a pm with a lead under it, three live runs, a
// question and an approval waiting on the owner, an escalation, a paused agent and an offline node.
// factory ui --demo renders it.
func DemoFixture() Fixture {
	at := DemoAt
	ago := func(m int) time.Time { return at.Add(-time.Duration(m) * time.Minute) }
	unix := func(m int) int64 { return ago(m).Unix() }
	f := Fixture{Name: "demo", Details: map[string]TaskDetail{}, Runs: map[string]storage.Run{}, RunEvents: map[string][]RunEvent{}, ToolCalls: map[string][]storage.ToolCall{}}

	agents := []Agent{
		{Name: "designer", Role: "designer", ReportsTo: "pm", Provider: "claude", Model: "claude-opus-4-7"},
		{Name: "developer-1", Role: "developer", ReportsTo: "pm", Provider: "claude", Model: "claude-opus-4-7"},
		{Name: "developer-2", Role: "developer", ReportsTo: "lead", Provider: "codex", Model: "gpt-5.5"},
		{Name: "lead", Role: "tech lead", ReportsTo: "pm", Provider: "codex", Model: "gpt-5.5"},
		{Name: "pm", Role: "project manager", ReportsTo: "owner", Provider: "codex", Model: "gpt-5.5"},
		{Name: "qa", Role: "QA engineer", ReportsTo: "pm", Provider: "codex", Model: "gpt-5.5"},
		{Name: "reviewer", Role: "code reviewer", ReportsTo: "lead", Provider: "codex", Model: "gpt-5.5"},
	}
	type spec struct {
		id, project, title, status, assignee, by, origin string
		prio, created, updated                           int
		reason, who, action                              string
	}
	specs := []spec{
		{"P1-1", "project-1", "Task 1", "READY", "pm", "owner", "", 9, 300, 40, "parked:children", "pm", "wait for P1-3, P1-4, P1-5"},
		{"P1-2", "project-1", "Task 2", "DONE", "designer", "pm", "P1-1", 7, 280, 210, "terminal", "nobody", "final state"},
		{"P1-3", "project-1", "Task 3", "IN_PROGRESS", "developer-1", "pm", "P1-1", 7, 270, 12, "live_run", "developer-1", "run r-d1-7 is running"},
		{"P1-4", "project-1", "Task 4", "READY", "qa", "pm", "P1-1", 6, 265, 265, "dependencies", "developer-1", "finish P1-3"},
		{"P1-5", "project-1", "Task 5", "READY", "pm", "pm", "P1-1", 8, 200, 95, "parked:ask", "owner", "answer m-ask-1 in the inbox"},
		{"P1-6", "project-1", "Task 6", "BACKLOG", "designer", "owner", "", 2, 150, 150, "backlog", "owner", "release it to the board"},
		{"P1-7", "project-1", "Task 7", "READY", "developer-1", "pm", "P1-1", 5, 180, 180, "capacity", "developer-1", "busy with P1-3"},
		{"P1-8", "project-1", "Task 8", "DONE", "designer", "pm", "P1-1", 4, 260, 230, "terminal", "nobody", "final state"},
		{"P1-9", "project-1", "Task 9", "READY", "qa", "developer-1", "P1-3", 8, 25, 25, "runnable", "factory", "dispatch on the next tick"},
		{"P2-1", "project-2", "Task 1", "IN_REVIEW", "reviewer", "lead", "P2-3", 6, 240, 4, "live_run", "reviewer", "run r-rev-2 is running"},
		{"P2-2", "project-2", "Task 2", "READY", "developer-2", "lead", "P2-3", 7, 230, 45, "backoff", "nobody", "retry at 14:34"},
		{"P2-3", "project-2", "Task 3", "READY", "lead", "owner", "", 9, 320, 30, "parked:escalation", "owner", "answer m-esc-1 (unblock or cancel)"},
		{"P2-4", "project-2", "Task 4", "BACKLOG", "designer", "lead", "", 3, 200, 200, "backlog", "owner", "release it to the board"},
		{"P2-5", "project-2", "Task 5", "CANCELLED", "developer-2", "lead", "P2-3", 4, 400, 300, "terminal", "nobody", "final state"},
		{"P2-6", "project-2", "Task 6", "DONE", "developer-2", "lead", "P2-3", 6, 500, 260, "terminal", "nobody", "final state"},
		{"P3-1", "project-3", "Task 1", "DONE", "designer", "lead", "", 6, 600, 400, "terminal", "nobody", "final state"},
		{"P3-2", "project-3", "Task 2", "IN_PROGRESS", "developer-2", "lead", "P3-1", 8, 120, 7, "live_run", "developer-2", "run r-d2-4 is running"},
		{"P3-3", "project-3", "Task 3", "READY", "reviewer", "lead", "P3-1", 5, 110, 110, "capacity", "reviewer", "busy with P2-1"},
		{"P3-4", "project-3", "Task 4", "BACKLOG", "developer-2", "owner", "", 3, 90, 90, "backlog", "owner", "release it to the board"},
		{"P3-5", "project-3", "Task 5", "BLOCKED", "developer-2", "lead", "P3-1", 6, 100, 50, "parked:block", "lead", "send the input data"},
		{"P3-6", "project-3", "Task 6", "IN_REVIEW", "reviewer", "lead", "P3-1", 5, 85, 15, "capacity", "reviewer", "busy with P2-1"},
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
	byID("P1-3").Dependencies, byID("P1-3").CurrentRunID, byID("P1-3").Attempt = []string{"P1-2"}, "r-d1-7", 2
	byID("P1-4").Dependencies = []string{"P1-3"}
	byID("P2-1").Implementer, byID("P2-1").Reviewer, byID("P2-1").CurrentRunID = "developer-2", "reviewer", "r-rev-2"
	byID("P3-2").CurrentRunID = "r-d2-4"
	byID("P3-6").Implementer, byID("P3-6").Reviewer = "developer-2", "reviewer"
	byID("P1-2").Description = "Mockups for module 1: layout, states and the empty view."
	byID("P1-3").Description = "Implement module 1 from the P1-2 mockups.\n\nAcceptance:\n- matches the mockups\n- works offline\n- unit tests for module 1"
	byID("P3-2").Description = "Build module 2 from the P3-1 spec; keep the public API unchanged."
	ts = append(ts, Task{Task: tasks.Task{ID: "conv-project-2-lead", ProjectID: "project-2", Title: "Conversation with owner", Status: "READY", Assignee: "lead", Kind: "conversation", Counterpart: "owner", Version: 1, CreatedAt: unix(400)}, Reason: "idle", Clearer: tasks.Clearer{Who: "nobody", Action: "waits for a message"}})

	runs := map[string][]core.RunStatus{
		"project-1": {{ID: "r-d1-7", Agent: "developer-1", TaskID: "P1-3", Status: "RUNNING", StartedAt: unix(12)}},
		"project-2": {{ID: "r-rev-2", Agent: "reviewer", TaskID: "P2-1", Status: "RUNNING", StartedAt: unix(4)}},
		"project-3": {{ID: "r-d2-4", Agent: "developer-2", TaskID: "P3-2", Status: "RUNNING", StartedAt: unix(7)}},
	}
	names := map[string]string{"project-1": "Project 1", "project-2": "Project 2", "project-3": "Project 3"}
	status := []core.ProjectStatus{}
	for _, p := range []string{"project-1", "project-2", "project-3"} {
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

	ask := messaging.Message{ID: "m-ask-1", ProjectID: "project-1", TaskID: "P1-5", Sender: "pm", Recipient: "owner", ConversationID: "owner-pm-project-1", MessageType: "question",
		Body: "Which option for Task 5: A or B? A is free but slower; B is faster but paid.", Blocking: true, Status: "PENDING", Version: 1, CreatedAt: unix(95), Deadline: at.Add(23 * time.Hour).Unix(),
		Questions: []messaging.Question{{ID: "answer", Type: "single_choice", Question: "Which option for Task 5?", Options: []string{"option-a", "option-b"}}}}
	esc := messaging.Message{ID: "m-esc-1", ProjectID: "project-2", TaskID: "P2-3", Sender: "factory", Recipient: "owner", ConversationID: "owner-lead-project-2", MessageType: "question",
		Body: "P2-3 stalled: it waits on P2-2, which failed 3 times without progress.", Status: "PENDING", Version: 1, CreatedAt: unix(30), EscalationReason: "stalled",
		Questions: []messaging.Question{{ID: "answer", Type: "single_choice", Question: "How should task P2-3 continue?", Options: []string{"unblock", "cancel"}}}}
	appr := messaging.Message{ID: "m-appr-1", ProjectID: "project-2", TaskID: "P2-3", Sender: "lead", Recipient: "owner", ConversationID: "owner-lead-project-2", MessageType: "question", ApprovalID: "ap-1",
		Body: "Approve release of Project 2?", Status: "PENDING", Version: 1, CreatedAt: unix(20)}
	approval := approvals.Approval{ID: "ap-1", ProjectID: "project-2", Agent: "lead", TaskID: "P2-3", RunID: "r-lead-3", MessageID: "m-appr-1", Status: "PENDING", Version: 1, CreatedAt: unix(20), ExpiresAt: at.Add(24 * time.Hour).Unix(),
		Reason: "release 1.2 of Project 2 passed review", Call: factorytools.Call{Name: "factory_release_publish", Arguments: map[string]any{"channel": "beta", "version": "1.2"}}}

	// Cost history: the runs that ended in the last 24 h.
	recent := []storage.RunCost{}
	taskCost := map[string]float64{}
	for i, rc := range []struct {
		agent, task string
		min         int
		cost        float64
	}{
		{"designer", "P1-8", 1300, 0.22}, {"developer-2", "P2-6", 1250, 0.48}, {"designer", "P3-1", 1100, 0.31}, {"developer-2", "P2-6", 980, 0.36},
		{"lead", "P2-3", 900, 0.09}, {"designer", "P1-2", 700, 0.41}, {"pm", "P1-1", 640, 0.05}, {"developer-1", "P1-3", 420, 0.88},
		{"developer-2", "P2-2", 300, 0.27}, {"developer-1", "P1-3", 240, 0.95}, {"reviewer", "P2-1", 200, 0.19}, {"developer-2", "P2-1", 150, 0.52},
		{"developer-2", "P2-2", 120, 0.31}, {"developer-1", "P1-3", 81, 0.61}, {"reviewer", "P2-1", 110, 0.12}, {"developer-2", "P2-2", 60, 0.29},
		{"pm", "P1-5", 96, 0.07}, {"lead", "P2-3", 25, 0.04}, {"developer-2", "P2-1", 55, 0.44},
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
	f.Runs["r-d1-6"] = run("r-d1-6", "developer-1", "P1-3", "project-1", "ENDED", "failed", 100, 81, "go test: the test database did not start", opus(48000, 6200, 21000, 0.61))
	f.Runs["r-d1-7"] = run("r-d1-7", "developer-1", "P1-3", "project-1", "RUNNING", "", 12, 0, "", opus(45230, 12891, 23102, 1.24))
	f.Runs["r-des-3"] = run("r-des-3", "designer", "P1-2", "project-1", "ENDED", "succeeded", 240, 210, "mockups ready", opus(30000, 4100, 9000, 0.41))
	f.Runs["r-rev-2"] = run("r-rev-2", "reviewer", "P2-1", "project-2", "RUNNING", "", 4, 0, "", gpt(22000, 1800, 64000))
	f.Runs["r-d2-4"] = run("r-d2-4", "developer-2", "P3-2", "project-3", "RUNNING", "", 7, 0, "", gpt(81000, 7400, 152000))

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
	claude("r-d1-7", 720, "claude/system/init", map[string]any{"type": "system", "subtype": "init", "model": "claude-opus-4-7"})
	claude("r-d1-7", 715, "claude/assistant", text("Resuming P1-3. The owner asked for offline support, so I'll start with the store layer."))
	claude("r-d1-7", 690, "claude/assistant", toolUse("mcp__factory__factory_task_get", map[string]any{"task": "P1-2"}))
	claude("r-d1-7", 660, "claude/assistant", toolUse("Read", map[string]any{"file_path": "src/module_1.go"}))
	claude("r-d1-7", 600, "claude/assistant", toolUse("Edit", map[string]any{"file_path": "src/module_1.go"}))
	claude("r-d1-7", 560, "claude/assistant", toolUse("Bash", map[string]any{"command": "go test ./... -run TestModule1 -count=1 -race -timeout 5m"}))
	claude("r-d1-7", 520, "claude/assistant", toolUse("Bash", map[string]any{"command": "git push --force origin af/P1-3"}))
	b, _ := json.Marshal(map[string]any{"argv": []string{"git", "push", "--force", "origin", "af/P1-3"}, "effect": "deny", "rule": "no-force-push"})
	add("r-d1-7", 519, "agent.progress", protocol.Event{Method: "factory/command_refused", Data: b})
	claude("r-d1-7", 518, "claude/user", toolError("command refused by policy no-force-push"))
	claude("r-d1-7", 480, "claude/assistant", text("Force push is not allowed; pushing a new commit instead."))
	claude("r-d1-7", 470, "claude/assistant", toolUse("Bash", map[string]any{"command": "git push origin af/P1-3"}))
	claude("r-d1-7", 380, "claude/assistant", toolUse("mcp__factory__factory_message_send", map[string]any{"recipient": "pm", "body": "Task 3: the store layer is done; starting the offline cache."}))
	claude("r-d1-7", 300, "claude/assistant", toolUse("Grep", map[string]any{"pattern": "loadItems", "path": "src/**"}))
	claude("r-d1-7", 150, "claude/assistant", text("All 42 tests pass. Submitting for review."))
	claude("r-d1-7", 120, "claude/assistant", toolUse("mcp__factory__factory_exit_submit", map[string]any{"summary": "module 1 with an offline store, 42 tests", "reviewer": "pm"}))
	add("r-d1-7", 118, "agent.progress", protocol.Event{Method: "factory/exit_guard", Data: json.RawMessage(`{"refusal":1,"reason":"submit needs a pushed branch ahead of main"}`)})
	claude("r-d1-7", 40, "claude/assistant", toolUse("Bash", map[string]any{"command": "go test ./src/... -run TestStore"}))
	f.ToolCalls["r-d1-7"] = []storage.ToolCall{
		{ID: "tc1", RunID: "r-d1-7", Agent: "developer-1", ProjectID: "project-1", Tool: "factory_task_get", Status: 200, CreatedAt: at.Add(-690 * time.Second).Unix()},
		{ID: "tc2", RunID: "r-d1-7", Agent: "developer-1", ProjectID: "project-1", Tool: "factory_message_send", Status: 200, CreatedAt: at.Add(-380 * time.Second).Unix()},
		{ID: "tc3", RunID: "r-d1-7", Agent: "developer-1", ProjectID: "project-1", Tool: "factory_exit_submit", Status: 409, CreatedAt: at.Add(-119 * time.Second).Unix()},
	}
	codex := func(id string, sec int, item map[string]any) {
		b, _ := json.Marshal(map[string]any{"item": item})
		add(id, sec, "agent.progress", protocol.Event{Method: "item/completed", Data: b})
	}
	codex("r-rev-2", 240, map[string]any{"type": "agentMessage", "text": "Reviewing the second submission of P2-1."})
	codex("r-rev-2", 200, map[string]any{"type": "commandExecution", "command": "git diff main...af/P2-1 --stat", "exitCode": 0})
	codex("r-rev-2", 150, map[string]any{"type": "commandExecution", "command": "go test ./...", "exitCode": 0})
	codex("r-rev-2", 90, map[string]any{"type": "mcpToolCall", "server": "factory", "tool": "factory_task_comments", "arguments": map[string]any{"task": "P2-1"}})
	codex("r-rev-2", 30, map[string]any{"type": "agentMessage", "text": "Testing Task 1 on a large input as the owner asked."})
	codex("r-d2-4", 420, map[string]any{"type": "agentMessage", "text": "Building module 2 from the P3-1 spec."})
	codex("r-d2-4", 380, map[string]any{"type": "fileChange", "changes": []any{map[string]any{"path": "src/module_2.ts"}, map[string]any{"path": "src/module_2_test.ts"}}})
	codex("r-d2-4", 300, map[string]any{"type": "commandExecution", "command": "npm run build", "exitCode": 0})
	codex("r-d2-4", 200, map[string]any{"type": "mcpToolCall", "server": "factory", "tool": "factory_message_send", "arguments": map[string]any{"recipient": "lead", "body": "Which option for Task 2: A or B?"}})
	codex("r-d2-4", 60, map[string]any{"type": "commandExecution", "command": "npm test -- module_2", "exitCode": 0})
	codex("r-d2-4", 15, map[string]any{"type": "fileChange", "changes": []any{map[string]any{"path": "src/module_2.ts"}}})
	claude("r-des-3", 14400, "claude/assistant", text("Drafting the mockups for module 1."))
	claude("r-des-3", 12720, "claude/assistant", toolUse("mcp__factory__factory_exit_done", map[string]any{"summary": "module 1 mockups v3"}))
	add("r-des-3", 12600, "agent.completed", protocol.Event{Status: "ENDED", Outcome: "succeeded", ExitState: "accepted", Result: "mockups ready"})
	claude("r-d1-6", 6000, "claude/assistant", toolUse("Bash", map[string]any{"command": "docker compose up -d db"}))
	add("r-d1-6", 4860, "agent.completed", protocol.Event{Status: "ENDED", Outcome: "failed", ExitState: "none", Result: "go test: the test database did not start"})

	f.Overview = Overview{
		Health: Health{Status: "ok", Database: "postgres 17.6", Company: "Company 1", ExplicitExits: "on"},
		Status: status, Agents: agents, Tasks: ts,
		Pauses:    []storage.Pause{{Scope: "agent", Target: "designer", Reason: "waiting for the style guide", Actor: "owner", CreatedAt: unix(60)}},
		Pending:   storage.PendingCounts{Inbox: 3, Approvals: 1, Alerts: 1, Reviews: 2, Total: 4},
		Nodes:     demoNodes(at),
		Inbox:     []messaging.Message{ask, appr, esc},
		Approvals: []approvals.Approval{approval},
		Costs:     costs, TaskCosts: taskCosts, RecentCosts: storage.CostReport{Runs: recent},
		LiveRuns: map[string]storage.Run{"r-d1-7": f.Runs["r-d1-7"], "r-rev-2": f.Runs["r-rev-2"], "r-d2-4": f.Runs["r-d2-4"]},
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
		item(600, "task.created", "project-3", "P3-1", "", "designer", "lead", "Task 1", false),
		item(500, "task.created", "project-2", "P2-6", "", "developer-2", "lead", "Task 6", false),
		item(400, "task.exit.done", "project-3", "P3-1", "r-des-1", "designer", "designer", "designer done: Task 1 spec v2 in the repo", false),
		item(320, "task.created", "project-2", "P2-3", "", "lead", "owner", "Task 3", false),
		item(300, "task.created", "project-1", "P1-1", "", "pm", "owner", "Task 1", false),
		item(280, "task.created", "project-1", "P1-2", "", "designer", "pm", "Task 2", false),
		item(270, "task.created", "project-1", "P1-3", "", "developer-1", "pm", "Task 3", false),
		item(265, "task.created", "project-1", "P1-4", "", "qa", "pm", "Task 4", false),
		item(260, "task.exit.done", "project-2", "P2-6", "r-d2-2", "developer-2", "developer-2", "developer-2 done: Task 6 merged, tests pass", false),
		item(240, "task.created", "project-2", "P2-1", "", "developer-2", "lead", "Task 1", false),
		item(230, "task.created", "project-2", "P2-2", "", "developer-2", "lead", "Task 2", false),
		item(210, "task.exit.done", "project-1", "P1-2", "r-des-3", "designer", "designer", "designer done: module 1 mockups v3", false),
		item(209, "task.trigger.dependency_done", "project-1", "P1-3", "", "developer-1", "designer", "P1-2 done", false),
		item(200, "task.created", "project-1", "P1-5", "", "pm", "pm", "Task 5", false),
		item(180, "task.created", "project-1", "P1-7", "", "developer-1", "pm", "Task 7", false),
		item(160, "message.created", "project-1", "P1-3", "", "", "developer-1", "developer-1 → pm (question): Should Task 3 support option A or both?", false),
		item(150, "message.created", "project-1", "P1-3", "", "", "pm", "pm → developer-1 (answer): Option A only for now.", false),
		item(120, "task.created", "project-3", "P3-2", "", "developer-2", "lead", "Task 2", false),
		item(119, "task.exit.submitted", "project-2", "P2-1", "r-d2-5", "developer-2", "developer-2", "developer-2 → reviewer (submit): Task 1 is ready for review", false),
		item(110, "task.exit.handoff", "project-2", "P2-1", "r-rev-1", "reviewer", "reviewer", "reviewer → developer-2 (changes_requested): add tests for edge cases", false),
		item(108, "task.created", "project-3", "P3-3", "", "reviewer", "lead", "Task 3", false),
		item(100, "task.exit.asked", "project-1", "P1-5", "r-pm-2", "pm", "pm", "pm asked: Which option for Task 5: A or B?", false),
		item(95, "message.created", "project-1", "P1-5", "", "", "pm", "pm → owner (question): Which option for Task 5: A or B? A is free but slower; B is faster but paid.", true),
		item(95, "task.parked.ask", "project-1", "P1-5", "", "pm", "", "waits for the answer to m-ask-1", false),
		item(85, "task.exit.submitted", "project-3", "P3-6", "r-d2-3", "developer-2", "developer-2", "developer-2 → reviewer (submit): Task 6 is ready for review", false),
		item(80, "task.retry", "project-1", "P1-3", "r-d1-6", "developer-1", "", "P1-3: run r-d1-6 ended failed; retry in 1m0s", false),
		item(70, "task.trigger.directive", "project-1", "P1-3", "", "developer-1", "owner", "Keep Task 3 working offline.", false),
		item(60, "owner.paused", "", "", "", "", "owner", "agent designer: waiting for the style guide", true),
		item(55, "task.exit.submitted", "project-2", "P2-1", "r-d2-6", "developer-2", "developer-2", "developer-2 → reviewer (submit): edge-case tests added", false),
		item(50, "task.exit.blocked", "project-3", "P3-5", "r-d2-1", "developer-2", "developer-2", "developer-2 blocked: needs the input data for Task 5", false),
		item(45, "task.alert.no_progress", "project-2", "P2-2", "", "developer-2", "", "P2-2: 3 runs without progress", true),
		item(40, "task.exit.handoff", "project-2", "P2-3", "r-lead-2", "lead", "lead", "lead → developer-2 (handoff): rebase P2-2 on main before retrying", false),
		item(30, "task.escalation.stalled", "project-2", "P2-3", "", "lead", "", "P2-3 waits on P2-2 → owner", true),
		item(25, "task.created", "project-1", "P1-9", "", "qa", "developer-1", "Task 9", false),
		item(20, "approval.requested", "project-2", "P2-3", "r-lead-3", "lead", "lead", "factory_release_publish: release 1.2 of Project 2 passed review", true),
		item(12, "task.started", "project-1", "P1-3", "r-d1-7", "developer-1", "", "Task 3", false),
		item(9, "run.command_refused", "project-1", "P1-3", "r-d1-7", "developer-1", "", "git push --force refused by rule no-force-push", false),
		item(7, "task.started", "project-3", "P3-2", "r-d2-4", "developer-2", "", "Task 2", false),
		item(6, "node.low_disk", "", "", "", "", "node", "mac-mini: 4.1 GiB free on /", true),
		item(4, "task.started", "project-2", "P2-1", "r-rev-2", "reviewer", "", "Task 1", false),
		item(3, "message.created", "project-3", "P3-2", "", "", "developer-2", "developer-2 → lead (question): Which option for Task 2: A or B?", false),
		item(2, "task.commented", "project-2", "P2-1", "", "reviewer", "owner", "Please check Task 1 on a large input.", false),
		item(1, "message.created", "project-3", "P3-2", "", "", "lead", "lead → developer-2 (answer): Option B, keep it behind a flag.", false),
	}
	for i := range f.Feed {
		switch f.Feed[i].Type {
		case "owner.paused":
			f.Feed[i].EntityID = "agent:designer"
		case "node.low_disk":
			f.Feed[i].EntityID = "node:mac-mini"
		}
	}

	// The detail of P1-3: attempts, costs, comments and its events.
	cal3 := TaskDetail{Task: *byID("P1-3"), Attempts: []storage.Run{f.Runs["r-d1-6"], f.Runs["r-d1-7"]},
		Why:      tasks.Verdict{TaskID: "P1-3", Status: "IN_PROGRESS", Reason: "live_run", Detail: "run r-d1-7 is running", Clearer: tasks.Clearer{Who: "developer-1", Action: "wait for the run"}, Pending: []tasks.Trigger{{Seq: 9, Kind: "directive", Actor: "owner", Authority: true}}},
		Comments: []storage.Comment{{ID: "c1", Author: "pm", Text: "Option A only for now; option B later.", CreatedAt: unix(150)}, {ID: "c2", Author: "owner", Text: "Keep Task 3 working offline.", CreatedAt: unix(70)}},
		Costs:    storage.CostReport{Runs: []storage.RunCost{{RunID: "r-d1-6", TaskID: "P1-3", Agent: "developer-1", Outcome: "failed", CostUSD: 0.61, CostSource: "reported"}}, Total: storage.CostGroup{Key: "total", Runs: 1, CostUSD: 0.61}},
	}
	for _, it := range f.Feed {
		if it.TaskID == "P1-3" {
			cal3.Events = append(cal3.Events, it)
		}
	}
	f.Details["P1-3"] = cal3
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

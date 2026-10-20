package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

func TestInteractionOf(t *testing.T) {
	for _, tc := range []struct {
		item               storage.FeedItem
		from, to, verb, tx string
		ok                 bool
	}{
		{storage.FeedItem{Type: "task.created", Agent: "demo", Actor: "manager", Summary: "Fix it"}, "manager", "demo", "create", "Fix it", true},
		{storage.FeedItem{Type: "task.created", Agent: "demo", Summary: "Owner task"}, "owner", "demo", "create", "Owner task", true},
		{storage.FeedItem{Type: "task.exit.handoff", Agent: "demo", Summary: "demo → reviewer (handoff): check the copy"}, "demo", "reviewer", "handoff", "check the copy", true},
		{storage.FeedItem{Type: "task.exit.handoff", Agent: "reviewer", Summary: "reviewer → demo (approved): ship it"}, "reviewer", "demo", "approved", "ship it", true},
		{storage.FeedItem{Type: "task.exit.submitted", Agent: "demo", Summary: "demo → review (submit): done"}, "demo", "review", "submit", "done", true},
		{storage.FeedItem{Type: "task.exit.done", Agent: "demo", Summary: "demo done: all good"}, "demo", "", "done", "all good", true},
		{storage.FeedItem{Type: "task.exit.asked", Agent: "demo", Summary: "demo asked: which API?"}, "demo", "", "asked", "which API?", true},
		{storage.FeedItem{Type: "message.created", Actor: "demo", Summary: "demo → manager (question): which API?"}, "demo", "manager", "question", "which API?", true},
		{storage.FeedItem{Type: "message.escalated", Summary: "demo → manager (question): which API?"}, "demo", "manager", "escalated", "which API?", true},
		{storage.FeedItem{Type: "message.read", Summary: "owner → demo (answer): yes"}, "", "", "", "", false},
		{storage.FeedItem{Type: "task.trigger.directive", Agent: "demo", Actor: "owner", Summary: "ship today"}, "owner", "demo", "directive", "ship today", true},
		{storage.FeedItem{Type: "task.trigger.dependency_done", Agent: "demo"}, "", "", "", "", false},
		{storage.FeedItem{Type: "task.reassigned", Agent: "qa", Actor: "pm", Summary: "x"}, "pm", "qa", "reassigned", "x", true},
		{storage.FeedItem{Type: "task.escalation.stalled", Agent: "manager", Summary: "N-3 waits on N-2 → owner"}, "manager", "owner", "escalate", "N-3 waits on N-2", true},
		{storage.FeedItem{Type: "approval.requested", Agent: "manager", Summary: "publish"}, "manager", "owner", "approval", "publish", true},
		{storage.FeedItem{Type: "task.retry", Agent: "demo"}, "", "", "", "", false},
		{storage.FeedItem{Type: "node.low_disk"}, "", "", "", "", false},
	} {
		in, ok := interactionOf(tc.item)
		if ok != tc.ok || ok && (in.From != tc.from || in.To != tc.to || in.Verb != tc.verb || in.Text != tc.tx) {
			t.Errorf("%s %q: got %v %+v", tc.item.Type, tc.item.Summary, ok, in)
		}
	}
}

func TestEdges(t *testing.T) {
	ins := interactions(DemoFixture().Feed, FlowFilter{Project: "project-2"})
	es := edges(ins)
	if len(es) == 0 || es[0].From != "lead" || es[0].To != "developer-2" || es[0].Total != 4 || es[0].verbsText() != "create×3 handoff" {
		t.Fatalf("%+v", es)
	}
	for _, in := range ins {
		if in.Project != "project-2" {
			t.Fatal(in)
		}
	}
	if got := interactions(DemoFixture().Feed, FlowFilter{Agent: "reviewer"}); len(got) != 6 {
		t.Fatalf("%d", len(got))
	}
}

func progress(method string, data any) protocol.Envelope {
	b, _ := json.Marshal(data)
	return protocol.Wrap("agent.progress", "r", "", protocol.Event{RunID: "r", Method: method, Data: b})
}

func TestDescribeRunEvent(t *testing.T) {
	for _, tc := range []struct {
		e    protocol.Envelope
		kind string
		text string
	}{
		{progress("item/completed", map[string]any{"item": map[string]any{"type": "agentMessage", "text": "hello\nworld"}}), lineSay, "hello world"},
		{progress("item/completed", map[string]any{"item": map[string]any{"type": "commandExecution", "command": []string{"go", "test"}, "exitCode": 0}}), lineShell, "$ go test"},
		{progress("item/completed", map[string]any{"item": map[string]any{"type": "commandExecution", "command": "make", "exitCode": 2}}), lineError, "$ make (exit 2)"},
		{progress("item/completed", map[string]any{"item": map[string]any{"type": "mcpToolCall", "server": "factory", "tool": "factory_exit_done", "arguments": map[string]any{"summary": "ok"}}}), lineExit, `summary=ok`},
		{progress("item/completed", map[string]any{"item": map[string]any{"type": "fileChange", "changes": []any{map[string]any{"path": "a.go"}}}}), lineTool, "a.go"},
		{progress("claude/assistant", map[string]any{"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "mcp__factory__factory_task_create", "input": map[string]any{"title": "x"}}}}}), lineMCP, `title=x`},
		{progress("claude/assistant", map[string]any{"message": map[string]any{"content": []any{map[string]any{"type": "tool_use", "name": "Grep", "input": map[string]any{"pattern": "TODO"}}}}}), lineTool, `"TODO"`},
		{progress("factory/command_refused", map[string]any{"argv": []string{"rm", "-rf", "/"}, "effect": "deny", "rule": "no-rm"}), lineRefusal, "command refused (deny, rule no-rm): rm -rf /"},
		{progress("codex/exit_guard", map[string]any{"refusal": 2}), lineRefusal, "exit guard refused the turn's end"},
		{progress("claude/shim/auth_error", nil), lineError, "runtime auth_error"},
		{protocol.Wrap("agent.completed", "r", "", protocol.Event{RunID: "r", Status: "ENDED", Outcome: "failed", ExitState: "refused", Result: "boom"}), lineEnd, "run ended failed (exit refused): boom"},
	} {
		lines := describeRunEvent(tc.e)
		if len(lines) != 1 || lines[0].Kind != tc.kind || lines[0].Text != tc.text && lines[0].Arg != tc.text {
			t.Errorf("%s: %+v", tc.text, lines)
		}
	}
	for _, quiet := range []protocol.Envelope{progress("item/agentMessage/delta", map[string]any{"delta": "x"}), progress("claude/shim/usage", nil), progress("claude/user", map[string]any{"message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "content": "ok"}}}})} {
		if lines := describeRunEvent(quiet); len(lines) != 0 {
			t.Errorf("%+v", lines)
		}
	}
	// Core's tool call records: a 4xx is a refusal.
	if l := describeToolCall(storage.ToolCall{Tool: "factory_exit_submit", Status: 423}); l.Kind != lineRefusal || !strings.Contains(l.Text, "refused 423") {
		t.Fatal(l)
	}
}

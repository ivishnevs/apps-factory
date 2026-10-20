package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// LogLine is one line of a run's activity as the live run view shows it.
type LogLine struct {
	At   time.Time
	Kind string // say, shell, mcp, exit, tool, refusal, error, info, end, core
	Text string
}

// Line kinds.
const (
	lineSay     = "say"
	lineShell   = "shell"
	lineMCP     = "mcp"
	lineExit    = "exit"
	lineTool    = "tool"
	lineRefusal = "refusal"
	lineError   = "error"
	lineInfo    = "info"
	lineEnd     = "end"
	lineCore    = "core"
)

// describeRunEvent turns one stored run event into activity lines: agent messages, tool calls (shell
// commands and factory_* MCP calls apart), refusals and the run's end. Deltas, usage and other runtime
// bookkeeping produce nothing.
func describeRunEvent(e protocol.Envelope) []LogLine {
	var p protocol.Event
	if json.Unmarshal(e.Payload, &p) != nil {
		return nil
	}
	at := e.At
	line := func(kind, text string) LogLine { return LogLine{At: at, Kind: kind, Text: oneLine(text)} }
	if p.Status == "ENDED" {
		text := "run ended " + p.Outcome
		if p.ExitState != "" {
			text += " (exit " + p.ExitState + ")"
		}
		if p.Result != "" {
			text += ": " + p.Result
		}
		return []LogLine{line(lineEnd, text)}
	}
	if p.Quota != nil {
		return []LogLine{line(lineError, "quota limit hit ("+p.Quota.Type+")")}
	}
	var data map[string]any
	_ = json.Unmarshal(p.Data, &data)
	switch {
	case p.Method == "claude/system/init":
		return []LogLine{line(lineInfo, "session started, model "+str(data, "model"))}
	case p.Method == "claude/assistant":
		return claudeContent(at, data, false)
	case p.Method == "claude/user":
		return claudeContent(at, data, true)
	case strings.HasPrefix(p.Method, "claude/shim/"):
		ev := strings.TrimPrefix(p.Method, "claude/shim/")
		switch ev {
		case "usage", "cost", "quota":
			return nil
		}
		return []LogLine{line(lineError, "runtime "+ev)}
	case p.Method == "item/completed":
		item, _ := data["item"].(map[string]any)
		return codexItem(at, item)
	case p.Method == "factory/command_refused":
		argv := []string{}
		if a, ok := data["argv"].([]any); ok {
			for _, s := range a {
				argv = append(argv, fmt.Sprint(s))
			}
		}
		return []LogLine{line(lineRefusal, fmt.Sprintf("command refused (%s, rule %s): %s", str(data, "effect"), str(data, "rule"), strings.Join(argv, " ")))}
	case strings.HasSuffix(p.Method, "/exit_guard"):
		text := "exit guard refused the turn's end"
		if r := str(data, "reason"); r != "" {
			text += ": " + r
		}
		return []LogLine{line(lineRefusal, text)}
	case p.Method == "codex/quota":
		return []LogLine{line(lineError, "quota limit hit")}
	}
	return nil
}

func claudeContent(at time.Time, data map[string]any, user bool) []LogLine {
	msg, _ := data["message"].(map[string]any)
	content, _ := msg["content"].([]any)
	out := []LogLine{}
	for _, c := range content {
		block, _ := c.(map[string]any)
		switch block["type"] {
		case "text":
			if !user {
				out = append(out, LogLine{At: at, Kind: lineSay, Text: oneLine(str(block, "text"))})
			}
		case "tool_use":
			input, _ := block["input"].(map[string]any)
			out = append(out, toolLine(at, str(block, "name"), input))
		case "tool_result":
			if isErr, _ := block["is_error"].(bool); isErr {
				out = append(out, LogLine{At: at, Kind: lineError, Text: "tool error: " + oneLine(resultText(block["content"]))})
			}
		}
	}
	return out
}

func resultText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		parts := []string{}
		for _, x := range c {
			if m, ok := x.(map[string]any); ok {
				parts = append(parts, str(m, "text"))
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// toolLine names a tool call: a shell command, a factory_* MCP call (an exit apart), or another tool.
func toolLine(at time.Time, name string, input map[string]any) LogLine {
	switch {
	case name == "Bash":
		return LogLine{At: at, Kind: lineShell, Text: "$ " + oneLine(str(input, "command"))}
	case strings.Contains(name, "factory_"):
		tool := name // mcp__factory__factory_x (Claude) or factory_x (Codex)
		if i := strings.LastIndex(tool, "__"); i >= 0 {
			tool = tool[i+2:]
		}
		kind := lineMCP
		if strings.HasPrefix(tool, "factory_exit_") {
			kind = lineExit
		}
		return LogLine{At: at, Kind: kind, Text: tool + " " + brief(input)}
	}
	arg := str(input, "file_path")
	if arg == "" {
		arg = str(input, "pattern")
	}
	if arg == "" {
		arg = brief(input)
	}
	return LogLine{At: at, Kind: lineTool, Text: name + " " + oneLine(arg)}
}

func codexItem(at time.Time, item map[string]any) []LogLine {
	switch item["type"] {
	case "agentMessage":
		return []LogLine{{At: at, Kind: lineSay, Text: oneLine(str(item, "text"))}}
	case "commandExecution":
		text := "$ " + oneLine(commandText(item["command"]))
		if code, ok := item["exitCode"].(float64); ok && code != 0 {
			return []LogLine{{At: at, Kind: lineError, Text: fmt.Sprintf("%s  (exit %d)", text, int(code))}}
		}
		return []LogLine{{At: at, Kind: lineShell, Text: text}}
	case "mcpToolCall":
		args, _ := item["arguments"].(map[string]any)
		return []LogLine{toolLine(at, str(item, "tool"), args)}
	case "fileChange":
		paths := []string{}
		if changes, ok := item["changes"].([]any); ok {
			for _, c := range changes {
				if m, ok := c.(map[string]any); ok {
					paths = append(paths, str(m, "path"))
				}
			}
		}
		return []LogLine{{At: at, Kind: lineTool, Text: "edit " + strings.Join(paths, ", ")}}
	}
	return nil
}

func commandText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		parts := make([]string, len(c))
		for i, s := range c {
			parts[i] = fmt.Sprint(s)
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// describeToolCall is Core's record of a Factory MCP call with its HTTP status: a 4xx is a refusal.
func describeToolCall(tc storage.ToolCall) LogLine {
	kind, verdict := lineCore, "ok"
	if tc.Status >= 400 {
		kind, verdict = lineRefusal, fmt.Sprintf("refused %d", tc.Status)
	}
	return LogLine{At: time.Unix(tc.CreatedAt, 0), Kind: kind, Text: fmt.Sprintf("core: %s %s", tc.Tool, verdict)}
}

// runLog merges a run's activity lines and Core's tool call records by time.
func runLog(events []RunEvent, calls []storage.ToolCall) []LogLine {
	out := []LogLine{}
	for _, e := range events {
		out = append(out, describeRunEvent(e.Envelope)...)
	}
	for _, tc := range calls {
		out = append(out, describeToolCall(tc))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

// brief is a short JSON of a tool input.
func brief(v map[string]any) string {
	if len(v) == 0 {
		return ""
	}
	b, _ := json.Marshal(v)
	return clip(string(b), 160)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

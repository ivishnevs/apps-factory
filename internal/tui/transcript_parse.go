package tui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// The full runtime transcript of a run (the session drill-down): the agent's text, its reasoning summaries,
// every tool call with its full input and full result, refusals and errors. Core keeps the runtime's event
// stream as run events (the node redacts credentials before they leave it), so Core is the source; a local
// Claude or Codex session file is read when the UI runs on the node's machine (localtx.go).

// TxEntry is one entry of a transcript.
type TxEntry struct {
	At   time.Time
	Kind string // say, think, tool, refusal, error, info, end
	// Tool is the tool's name (Bash, Read, factory_task_get, ...); ID pairs a call with its result.
	Tool string
	ID   string
	// Input is the call's full input (the command of a shell call, indented JSON otherwise); Brief its one-line form.
	Input string
	Brief string
	// Result is the full result; Done says a result arrived (a call without one is still running).
	Result  string
	Done    bool
	IsError bool
	// Text is the text of say, think, refusal, error, info and end entries.
	Text string
}

// Transcript entry kinds.
const (
	txSay     = "say"
	txThink   = "think"
	txTool    = "tool"
	txRefusal = "refusal"
	txError   = "error"
	txInfo    = "info"
	txEnd     = "end"
)

// isFactoryTool: a Factory MCP tool (mcp__factory__factory_x for Claude, factory_x for Codex).
func isFactoryTool(name string) bool { return strings.Contains(name, "factory_") }

// shortTool is a tool name without the MCP server prefix.
func shortTool(name string) string {
	if i := strings.LastIndex(name, "__"); i >= 0 && strings.HasPrefix(name, "mcp__") {
		return name[i+2:]
	}
	return name
}

// pretty is a value as indented JSON, a string as itself.
func pretty(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// txBuilder collects entries and pairs tool calls with their results.
type txBuilder struct {
	out   []TxEntry
	calls map[string]int // tool call id → index in out
}

func newTxBuilder() *txBuilder { return &txBuilder{calls: map[string]int{}} }

func (b *txBuilder) add(e TxEntry) {
	if e.Kind == txTool && e.ID != "" {
		if i, ok := b.calls[e.ID]; ok {
			// A completed item replaces its started form (Codex), keeping the original time.
			e.At = b.out[i].At
			b.out[i] = e
			return
		}
		b.calls[e.ID] = len(b.out)
	}
	b.out = append(b.out, e)
}

// result attaches a tool result to its call; a result without a call becomes its own entry.
func (b *txBuilder) result(at time.Time, id, text string, isErr bool) {
	if i, ok := b.calls[id]; ok {
		b.out[i].Result, b.out[i].Done, b.out[i].IsError = text, true, isErr
		return
	}
	b.out = append(b.out, TxEntry{At: at, Kind: txTool, Tool: "result", ID: id, Result: text, Done: true, IsError: isErr})
}

// toolCall is a tool call entry: a shell command shows as its command, other input as indented JSON.
func toolCall(at time.Time, id, name string, input map[string]any) TxEntry {
	e := TxEntry{At: at, Kind: txTool, Tool: shortTool(name), ID: id}
	switch {
	case name == "Bash" || name == "shell" || name == "local_shell":
		e.Tool = "Bash"
		cmd := commandText(input["command"])
		// Codex wraps commands as ["bash", "-lc", "<script>"]: the script is the command.
		if argv, ok := input["command"].([]any); ok && len(argv) == 3 && (argv[1] == "-lc" || argv[1] == "-c") {
			cmd = fmt.Sprint(argv[2])
		}
		e.Input, e.Brief = cmd, "$ "+oneLine(cmd)
		if d := str(input, "description"); d != "" {
			e.Input += "\n# " + d
		}
	default:
		e.Input = pretty(input)
		e.Brief = toolLine(at, name, input).Arg
	}
	return e
}

// claudeMessage adds the content blocks of a Claude assistant or user message (stream-json and the session
// file share this shape).
func (b *txBuilder) claudeMessage(at time.Time, data map[string]any) {
	msg, _ := data["message"].(map[string]any)
	role := str(msg, "role")
	if role == "" {
		role = str(data, "type")
	}
	switch content := msg["content"].(type) {
	case string:
		if role == "assistant" && strings.TrimSpace(content) != "" {
			b.add(TxEntry{At: at, Kind: txSay, Text: content})
		}
	case []any:
		for _, c := range content {
			block, _ := c.(map[string]any)
			switch block["type"] {
			case "text":
				if role == "assistant" {
					b.add(TxEntry{At: at, Kind: txSay, Text: str(block, "text")})
				}
			case "thinking":
				if t := str(block, "thinking"); strings.TrimSpace(t) != "" {
					b.add(TxEntry{At: at, Kind: txThink, Text: t})
				}
			case "redacted_thinking":
				b.add(TxEntry{At: at, Kind: txThink, Text: "(reasoning redacted by the provider)"})
			case "tool_use":
				input, _ := block["input"].(map[string]any)
				b.add(toolCall(at, str(block, "id"), str(block, "name"), input))
			case "tool_result":
				isErr, _ := block["is_error"].(bool)
				b.result(at, str(block, "tool_use_id"), fullResult(block["content"]), isErr)
			}
		}
	}
}

// fullResult is a tool result's whole text (a string, or text blocks; images are named).
func fullResult(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		parts := []string{}
		for _, x := range c {
			m, ok := x.(map[string]any)
			if !ok {
				continue
			}
			switch m["type"] {
			case "text", nil:
				parts = append(parts, str(m, "text"))
			case "image":
				parts = append(parts, "[image]")
			default:
				parts = append(parts, pretty(m))
			}
		}
		return strings.Join(parts, "\n")
	case nil:
		return ""
	}
	return pretty(v)
}

// codexItem adds a Codex app-server item (item/started: a running call; item/completed: its end).
func (b *txBuilder) codexItem(at time.Time, item map[string]any, done bool) {
	id := str(item, "id")
	switch item["type"] {
	case "agentMessage":
		if done {
			b.add(TxEntry{At: at, Kind: txSay, Text: str(item, "text")})
		}
	case "reasoning":
		if done {
			if t := reasoningText(item); t != "" {
				b.add(TxEntry{At: at, Kind: txThink, Text: t})
			}
		}
	case "commandExecution":
		e := toolCall(at, id, "Bash", map[string]any{"command": item["command"]})
		if done {
			e.Done, e.Result = true, str(item, "aggregatedOutput")
			if code, ok := item["exitCode"].(float64); ok && code != 0 {
				e.IsError = true
				e.Result = strings.TrimRight(e.Result, "\n") + fmt.Sprintf("\n(exit %d)", int(code))
			}
			if s := str(item, "status"); s == "failed" || s == "declined" {
				e.IsError = true
			}
		}
		b.add(e)
	case "mcpToolCall":
		args, _ := item["arguments"].(map[string]any)
		e := toolCall(at, id, str(item, "tool"), args)
		if done {
			e.Done = true
			if errv, ok := item["error"].(map[string]any); ok && errv != nil {
				e.IsError, e.Result = true, str(errv, "message")
			} else if res, ok := item["result"].(map[string]any); ok {
				e.Result = fullResult(res["content"])
				if isErr, _ := res["isError"].(bool); isErr {
					e.IsError = true
				}
			}
			if str(item, "status") == "failed" {
				e.IsError = true
			}
		}
		b.add(e)
	case "fileChange":
		e := TxEntry{At: at, Kind: txTool, Tool: "Edit", ID: id}
		paths, diffs := []string{}, []string{}
		if changes, ok := item["changes"].([]any); ok {
			for _, c := range changes {
				if m, ok := c.(map[string]any); ok {
					paths = append(paths, str(m, "path"))
					if d := str(m, "diff"); d != "" {
						diffs = append(diffs, d)
					}
				}
			}
		}
		e.Brief, e.Input = strings.Join(paths, ", "), strings.Join(paths, "\n")
		if done {
			e.Done, e.Result = true, strings.Join(diffs, "\n")
			if s := str(item, "status"); s == "failed" || s == "declined" {
				e.IsError = true
			}
		}
		b.add(e)
	case "webSearch":
		e := TxEntry{At: at, Kind: txTool, Tool: "WebSearch", ID: id, Input: str(item, "query"), Brief: str(item, "query"), Done: done}
		b.add(e)
	}
}

// reasoningText is a Codex reasoning item's summary (or its content when there is no summary).
func reasoningText(item map[string]any) string {
	for _, k := range []string{"summary", "content"} {
		parts := []string{}
		if list, ok := item[k].([]any); ok {
			for _, x := range list {
				switch v := x.(type) {
				case string:
					parts = append(parts, v)
				case map[string]any:
					parts = append(parts, str(v, "text"))
				}
			}
		}
		if t := strings.TrimSpace(strings.Join(parts, "\n")); t != "" {
			return t
		}
	}
	return ""
}

// event adds one stored run event.
func (b *txBuilder) event(e protocol.Envelope) {
	var p protocol.Event
	if json.Unmarshal(e.Payload, &p) != nil {
		return
	}
	at := e.At
	if p.Status == "ENDED" {
		text := "run ended " + p.Outcome
		if p.ExitState != "" {
			text += " (exit " + p.ExitState + ")"
		}
		if p.Result != "" {
			text += ": " + p.Result
		}
		b.add(TxEntry{At: at, Kind: txEnd, Text: text})
		return
	}
	if p.Quota != nil {
		b.add(TxEntry{At: at, Kind: txError, Text: "quota limit hit (" + p.Quota.Type + ")"})
		return
	}
	var data map[string]any
	_ = json.Unmarshal(p.Data, &data)
	switch {
	case p.Method == "claude/system/init":
		b.add(TxEntry{At: at, Kind: txInfo, Text: "session started, model " + str(data, "model")})
	case p.Method == "claude/assistant" || p.Method == "claude/user":
		b.claudeMessage(at, data)
	case strings.HasPrefix(p.Method, "claude/shim/"):
		ev := strings.TrimPrefix(p.Method, "claude/shim/")
		switch ev {
		case "usage", "cost", "quota", "session":
			return
		}
		b.add(TxEntry{At: at, Kind: txError, Text: "runtime " + ev})
	case p.Method == "item/started" || p.Method == "item/completed":
		item, _ := data["item"].(map[string]any)
		b.codexItem(at, item, p.Method == "item/completed")
	case p.Method == "factory/command_refused":
		argv := []string{}
		if a, ok := data["argv"].([]any); ok {
			for _, s := range a {
				argv = append(argv, fmt.Sprint(s))
			}
		}
		b.add(TxEntry{At: at, Kind: txRefusal, Tool: "command", Text: fmt.Sprintf("command refused (%s, rule %s): %s", str(data, "effect"), str(data, "rule"), strings.Join(argv, " "))})
	case strings.HasSuffix(p.Method, "/exit_guard"):
		text := "exit guard refused the turn's end"
		if r := str(data, "reason"); r != "" {
			text += ": " + r
		}
		b.add(TxEntry{At: at, Kind: txRefusal, Tool: "exit_guard", Text: text})
	case p.Method == "codex/quota":
		b.add(TxEntry{At: at, Kind: txError, Text: "quota limit hit"})
	case p.Method == "error":
		msg := str(data, "message")
		if e, ok := data["error"].(map[string]any); ok && msg == "" {
			msg = str(e, "message")
		}
		b.add(TxEntry{At: at, Kind: txError, Text: "runtime error: " + msg})
	}
}

// coreCalls adds Core's refusals of Factory MCP calls (HTTP status 400 and above).
func (b *txBuilder) coreCalls(calls []storage.ToolCall) {
	for _, tc := range calls {
		if tc.Status >= 400 {
			b.out = append(b.out, TxEntry{At: time.Unix(tc.CreatedAt, 0), Kind: txRefusal, Tool: "core", Text: fmt.Sprintf("Core refused %s (%d)", tc.Tool, tc.Status)})
		}
	}
}

func (b *txBuilder) entries() []TxEntry {
	sort.SliceStable(b.out, func(i, j int) bool { return b.out[i].At.Before(b.out[j].At) })
	return b.out
}

// transcriptFromEvents builds a run's transcript from Core's run events and tool call records.
func transcriptFromEvents(events []RunEvent, calls []storage.ToolCall) []TxEntry {
	b := newTxBuilder()
	for _, e := range events {
		b.event(e.Envelope)
	}
	b.coreCalls(calls)
	return b.entries()
}

// errTally counts a run's tool errors and refusals: tool results with is_error (failed shell commands,
// failed MCP calls), command guard refusals (exit 126: the refused command's failed result is the same
// incident), exit guard refusals and Core's refusals of Factory MCP calls (status ≥ 400; the runtime's
// failed result of that call is the same incident).
type errTally struct {
	N    int
	Last string
	At   time.Time
}

func tallyErrors(entries []TxEntry) errTally {
	var t errTally
	core := false
	for _, e := range entries {
		if e.Kind == txRefusal && e.Tool == "core" {
			core = true
		}
	}
	note := func(e TxEntry, text string) {
		t.N++
		t.Last, t.At = oneLine(text), e.At
	}
	// A command guard refusal and the refused command's failed result (exit 126) are one incident: the
	// refusal event and the result come within seconds of each other, in either order.
	refusals := []time.Time{}
	for _, e := range entries {
		if e.Kind == txRefusal && e.Tool == "command" {
			refusals = append(refusals, e.At)
		}
	}
	sameRefusal := func(at time.Time) bool {
		for i, r := range refusals {
			if d := at.Sub(r); d > -10*time.Second && d < 10*time.Second {
				refusals = append(refusals[:i], refusals[i+1:]...)
				return true
			}
		}
		return false
	}
	for _, e := range entries {
		switch {
		case e.Kind == txRefusal:
			note(e, e.Text)
		case e.Kind == txTool && e.IsError:
			if core && isFactoryTool(e.Tool) {
				continue
			}
			if e.Tool == "Bash" && sameRefusal(e.At) {
				continue
			}
			text := e.Tool + ": " + firstLine(e.Result)
			if e.Brief != "" && e.Tool == "Bash" {
				text = e.Brief + ": " + firstLine(e.Result)
			}
			note(e, text)
		}
	}
	return t
}

func firstLine(s string) string {
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(l) != "" {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

// readJSONL calls on with each JSON object line of r (long lines allowed; bad lines skipped).
func readJSONL(r io.Reader, on func(map[string]any)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 64<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var v map[string]any
		if json.Unmarshal(line, &v) == nil {
			on(v)
		}
	}
	return sc.Err()
}

func lineTime(v map[string]any) time.Time {
	if s := str(v, "timestamp"); s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// parseClaudeSession reads a Claude Code session file (<config>/projects/<dir>/<session>.jsonl).
func parseClaudeSession(r io.Reader) ([]TxEntry, error) {
	b := newTxBuilder()
	err := readJSONL(r, func(v map[string]any) {
		switch str(v, "type") {
		case "assistant", "user":
			if side, _ := v["isSidechain"].(bool); side {
				return // a subagent's own conversation
			}
			if meta, _ := v["isMeta"].(bool); meta {
				return
			}
			b.claudeMessage(lineTime(v), v)
		case "system":
			if c := str(v, "content"); c != "" && str(v, "level") == "error" {
				b.add(TxEntry{At: lineTime(v), Kind: txError, Text: c})
			}
		}
	})
	return b.entries(), err
}

// parseCodexSession reads a Codex rollout file ($CODEX_HOME/sessions/YYYY/MM/DD/rollout-…-<thread>.jsonl).
func parseCodexSession(r io.Reader) ([]TxEntry, error) {
	b := newTxBuilder()
	err := readJSONL(r, func(v map[string]any) {
		at := lineTime(v)
		p, _ := v["payload"].(map[string]any)
		switch str(v, "type") {
		case "response_item":
		case "event_msg":
			if str(p, "type") == "error" {
				b.add(TxEntry{At: at, Kind: txError, Text: "runtime error: " + str(p, "message")})
			}
			return
		default:
			return
		}
		switch str(p, "type") {
		case "message":
			if str(p, "role") != "assistant" {
				return
			}
			parts := []string{}
			if list, ok := p["content"].([]any); ok {
				for _, x := range list {
					if m, ok := x.(map[string]any); ok {
						parts = append(parts, str(m, "text"))
					}
				}
			}
			if t := strings.TrimSpace(strings.Join(parts, "\n")); t != "" {
				b.add(TxEntry{At: at, Kind: txSay, Text: t})
			}
		case "reasoning":
			if t := reasoningText(p); t != "" {
				b.add(TxEntry{At: at, Kind: txThink, Text: t})
			}
		case "function_call", "custom_tool_call":
			args := map[string]any{}
			if s := str(p, "arguments"); s != "" {
				if json.Unmarshal([]byte(s), &args) != nil {
					args = map[string]any{"arguments": s}
				}
			} else if s := str(p, "input"); s != "" {
				args = map[string]any{"input": s}
			}
			b.add(toolCall(at, str(p, "call_id"), str(p, "name"), args))
		case "local_shell_call":
			action, _ := p["action"].(map[string]any)
			b.add(toolCall(at, str(p, "call_id"), "local_shell", map[string]any{"command": action["command"]}))
		case "function_call_output", "custom_tool_call_output":
			text, isErr := codexOutput(p["output"])
			b.result(at, str(p, "call_id"), text, isErr)
		case "web_search_call":
			action, _ := p["action"].(map[string]any)
			b.add(TxEntry{At: at, Kind: txTool, Tool: "WebSearch", Input: str(action, "query"), Brief: str(action, "query"), Done: true})
		}
	})
	return b.entries(), err
}

// codexOutput is a function call output: a string (often JSON {"output","metadata":{"exit_code"}}) or an
// object {content, success}.
func codexOutput(v any) (string, bool) {
	switch o := v.(type) {
	case string:
		var j struct {
			Output   string `json:"output"`
			Metadata struct {
				ExitCode *int `json:"exit_code"`
			} `json:"metadata"`
		}
		if json.Unmarshal([]byte(o), &j) == nil && (j.Output != "" || j.Metadata.ExitCode != nil) {
			if j.Metadata.ExitCode != nil && *j.Metadata.ExitCode != 0 {
				return strings.TrimRight(j.Output, "\n") + fmt.Sprintf("\n(exit %d)", *j.Metadata.ExitCode), true
			}
			return j.Output, false
		}
		return o, false
	case map[string]any:
		text := fullResult(o["content"])
		if text == "" {
			text = pretty(o)
		}
		ok, has := o["success"].(bool)
		return text, has && !ok
	}
	return pretty(v), false
}

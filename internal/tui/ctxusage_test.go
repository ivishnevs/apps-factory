package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"appfactory/internal/protocol"
)

func ctxEvents(t *testing.T, methods []string, data []string) []RunEvent {
	t.Helper()
	out := []RunEvent{}
	for i, m := range methods {
		e := protocol.Wrap("agent.progress", "r1", "", protocol.Event{RunID: "r1", Method: m, Data: json.RawMessage(data[i])})
		out = append(out, RunEvent{Seq: int64(i + 1), Envelope: e})
	}
	return out
}

// The context is the last call's prompt, not the run's cumulative usage (705% before the fix).
func TestContextIsLastCallNotRunTotal(t *testing.T) {
	assistant := func(in, cr, cc int, parent string) string {
		return `{"type":"assistant","parent_tool_use_id":` + parent + `,"message":{"model":"claude-opus-5-5","content":[],"usage":{"input_tokens":` +
			itoa(in) + `,"cache_read_input_tokens":` + itoa(cr) + `,"cache_creation_input_tokens":` + itoa(cc) + `,"output_tokens":9}}}`
	}
	// Three turns whose cache reads sum to 1.4M; the last prompt is 2 + 120,000 + 3,000.
	events := ctxEvents(t,
		[]string{"claude/system/init", "claude/assistant", "claude/assistant", "claude/assistant", "claude/assistant", "claude/shim/usage"},
		[]string{`{"model":"claude-opus-5-5"}`, assistant(20, 600000, 40000, "null"), assistant(20, 690000, 30000, "null"),
			assistant(2, 120000, 3000, "null"), assistant(5, 900000, 0, `"toolu_sub"`), // a subagent's call: another context
			`{"model":"claude-opus-5-5","input":44,"cache_read":1410634,"cache_write":76769}`})
	c := contextOf(events)
	if !c.Known || c.Tokens != 123002 || c.Model != "claude-opus-5-5" {
		t.Fatalf("%+v", c)
	}
	if w := c.windowOf(""); w != 1_000_000 {
		t.Fatal(w)
	}
	if s := ansi.Strip(contextText(c, "", 30)); !strings.HasSuffix(s, " 12%") {
		t.Fatal(s)
	}
	// The runtime-reported window (result modelUsage contextWindow) wins over the table.
	events = append(events, ctxEvents(t, []string{"claude/result/success"}, []string{`{"modelUsage":{"claude-opus-5-5":{"contextWindow":200000}}}`})...)
	if c = contextOf(events); c.windowOf("") != 200_000 {
		t.Fatalf("%+v", c)
	}
	if s := ansi.Strip(contextText(c, "", 30)); !strings.HasSuffix(s, " 62%") {
		t.Fatal(s)
	}
}

func TestContextCodexLastAndWindow(t *testing.T) {
	events := ctxEvents(t,
		[]string{"thread/tokenUsage/updated", "thread/tokenUsage/updated"},
		[]string{`{"tokenUsage":{"total":{"inputTokens":9000},"last":{"inputTokens":9000,"cachedInputTokens":0}}}`,
			`{"tokenUsage":{"total":{"inputTokens":500000,"cachedInputTokens":400000},"last":{"inputTokens":300000,"cachedInputTokens":290000},"modelContextWindow":258400}}`})
	c := contextOf(events)
	if c.Tokens != 300000 || c.windowOf("gpt-6-sol") != 258400 {
		t.Fatalf("%+v", c)
	}
	// Over the window: clamped at 100% and flagged.
	if s := ansi.Strip(contextText(c, "gpt-6-sol", 30)); !strings.HasSuffix(s, "100%!") {
		t.Fatal(s)
	}
}

func TestContextUnknown(t *testing.T) {
	// Only cumulative usage: n/a rather than a wrong percentage.
	c := contextOf(ctxEvents(t, []string{"claude/shim/usage"}, []string{`{"input":44,"cache_read":1410634}`}))
	if s := ansi.Strip(contextText(c, "claude-opus-5-5", 30)); s != "n/a" {
		t.Fatal(s)
	}
	// An unknown model without a reported window: tokens, no percentage.
	c = ctxUsage{Tokens: 4200, Known: true, Model: "mystery-1"}
	if s := ansi.Strip(contextText(c, "", 30)); s != "4,200 tokens" {
		t.Fatal(s)
	}
	for model, want := range map[string]int64{"claude-opus-5-5": 1_000_000, "claude-opus-5-5[1m]": 1_000_000, "claude-sonnet-5-5": 1_000_000,
		"claude-haiku-5-5": 1_000_000, "claude-haiku-4-5": 200_000, "gpt-6-sol": 272_000, "gpt-6.1-sol": 500_000, "gpt-5.5": 272_000, "llama": 0} {
		if got := contextWindow(model); got != want {
			t.Error(model, got)
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

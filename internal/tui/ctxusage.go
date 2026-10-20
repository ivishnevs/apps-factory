package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"appfactory/internal/protocol"
)

// ctxUsage is how full a run's context is: the prompt size of its most recent model call, read from the
// run's event stream. A run's usage rows are cumulative (summed over every turn), so they cannot say this.
type ctxUsage struct {
	// Tokens is the last call's prompt: input + cache read + cache creation (Claude), inputTokens of the
	// last call including cached (Codex). Known reports that a per-call usage was seen at all.
	Tokens int64
	Known  bool
	// Model is the model of that call; Window the context size the runtime reported (0: not reported).
	Model  string
	Window int64
	// windows are the runtime-reported windows per model (Claude result modelUsage).
	windows map[string]int64
}

// observe takes one stored or streamed run event, oldest first.
func (c *ctxUsage) observe(e protocol.Envelope) {
	var p protocol.Event
	if json.Unmarshal(e.Payload, &p) != nil || len(p.Data) == 0 {
		return
	}
	switch {
	case p.Method == "claude/assistant":
		var d struct {
			Parent  *string `json:"parent_tool_use_id"`
			Message struct {
				Model string `json:"model"`
				Usage *struct {
					Input       int64 `json:"input_tokens"`
					CacheRead   int64 `json:"cache_read_input_tokens"`
					CacheCreate int64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		// A subagent's message (parent_tool_use_id set) is another context.
		if json.Unmarshal(p.Data, &d) != nil || d.Parent != nil || d.Message.Usage == nil {
			return
		}
		u := d.Message.Usage
		c.Tokens, c.Known = u.Input+u.CacheRead+u.CacheCreate, true
		if d.Message.Model != "" {
			c.Model = d.Message.Model
		}
		c.Window = c.windows[c.Model]
	case strings.HasPrefix(p.Method, "claude/result"):
		var d struct {
			ModelUsage map[string]struct {
				ContextWindow int64 `json:"contextWindow"`
			} `json:"modelUsage"`
		}
		if json.Unmarshal(p.Data, &d) != nil {
			return
		}
		for model, u := range d.ModelUsage {
			if u.ContextWindow > 0 {
				if c.windows == nil {
					c.windows = map[string]int64{}
				}
				c.windows[model] = u.ContextWindow
			}
		}
		c.Window = c.windows[c.Model]
	case p.Method == "thread/tokenUsage/updated":
		var d struct {
			TokenUsage struct {
				Last *struct {
					Input int64 `json:"inputTokens"`
				} `json:"last"`
				Window int64 `json:"modelContextWindow"`
			} `json:"tokenUsage"`
		}
		if json.Unmarshal(p.Data, &d) != nil || d.TokenUsage.Last == nil {
			return
		}
		c.Tokens, c.Known = d.TokenUsage.Last.Input, true
		if d.TokenUsage.Window > 0 {
			c.Window = d.TokenUsage.Window
		}
	}
}

// contextOf is the context of a run's events.
func contextOf(events []RunEvent) ctxUsage {
	var c ctxUsage
	for _, e := range events {
		c.observe(e.Envelope)
	}
	return c
}

// windowOf is the window to measure against: the runtime-reported one, else the model's from the table
// (model is the run's model when the events named none); 0 when unknown.
func (c ctxUsage) windowOf(model string) int64 {
	if c.Window > 0 {
		return c.Window
	}
	if c.Model != "" {
		model = c.Model
	}
	return contextWindow(model)
}

// contextWindow is a model's context size when the runtime does not report it; 0 for an unknown model.
// Claude: platform.claude.com model table (1M from the 4.6 generation on); Codex: its models cache.
func contextWindow(model string) int64 {
	model, _, _ = strings.Cut(model, "[") // claude-opus-5-5[1m]
	switch {
	case model == "":
		return 0
	case strings.HasPrefix(model, "claude-"):
		for _, old := range []string{"claude-haiku-4-5", "claude-sonnet-4-5", "claude-opus-4-5", "claude-opus-4-1", "claude-opus-4-0", "claude-sonnet-4-0", "claude-3"} {
			if strings.HasPrefix(model, old) {
				return 200_000
			}
		}
		return 1_000_000
	case strings.HasPrefix(model, "gpt-6.1"):
		return 500_000
	case strings.HasPrefix(model, "gpt-6"), strings.HasPrefix(model, "gpt-5"):
		return 272_000
	}
	return 0
}

// contextText renders the context gauge: n/a without a per-call usage, tokens alone without a window,
// clamped at 100% in the warning color when the prompt is over the window.
func contextText(c ctxUsage, model string, w int) string {
	if !c.Known {
		return stFaint.Render("n/a")
	}
	window := c.windowOf(model)
	if window <= 0 {
		return thousands(c.Tokens) + stFaint.Render(" tokens")
	}
	frac := float64(c.Tokens) / float64(window)
	pct := fmt.Sprintf(" %3.0f%%", min(frac, 1)*100)
	if frac > 1 {
		pct = stRedBold.Render(pct + "!")
	}
	return gauge(frac, max(w-6, 4)) + pct
}

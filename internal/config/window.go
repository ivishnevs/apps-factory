package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"appfactory/internal/protocol"

	"gopkg.in/yaml.v3"
)

// Context window per run kind (work-and-conversation-runs): spec.runtime.context_window is one value for every run
// ("200k") or {work, conversation}. A value is a token count, with k or M, or max (the model's maximum: no
// override). An unset kind keeps the runtime's default.

// Context window bounds: Claude Code raises a window below 100k to 100k and caps it at the model's window (1M).
const (
	MinContextWindow = 100_000
	MaxContextWindow = 1_000_000
)

// ContextWindow is spec.runtime.context_window.
type ContextWindow struct {
	Work         string `yaml:"work" json:"work,omitempty"`
	Conversation string `yaml:"conversation" json:"conversation,omitempty"`
}

// UnmarshalYAML reads one value for both kinds or the {work, conversation} mapping.
func (w *ContextWindow) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		w.Work, w.Conversation = n.Value, n.Value
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("context_window: a value (200k, max) or {work, conversation} expected")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i].Value, n.Content[i+1]
		if v.Kind != yaml.ScalarNode {
			return fmt.Errorf("context_window.%s: a value (200k, max) expected", k)
		}
		switch k {
		case "work":
			w.Work = v.Value
		case "conversation":
			w.Conversation = v.Value
		default:
			return fmt.Errorf("context_window: unknown key %q (work, conversation)", k)
		}
	}
	return nil
}

// UnmarshalJSON reads the same shapes from JSON (a string, a number or the object).
func (w *ContextWindow) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch x := v.(type) {
	case nil:
		*w = ContextWindow{}
	case string:
		w.Work, w.Conversation = x, x
	case float64:
		s := strconv.FormatFloat(x, 'f', -1, 64)
		w.Work, w.Conversation = s, s
	case map[string]any:
		*w = ContextWindow{}
		for k, e := range x {
			s := fmt.Sprint(e)
			if f, ok := e.(float64); ok {
				s = strconv.FormatFloat(f, 'f', -1, 64)
			}
			switch k {
			case "work":
				w.Work = s
			case "conversation":
				w.Conversation = s
			default:
				return fmt.Errorf("context_window: unknown key %q (work, conversation)", k)
			}
		}
	default:
		return fmt.Errorf("context_window: a value or {work, conversation} expected")
	}
	return nil
}

// ParseWindow reads one window value: tokens (0 for max or empty: no override).
func ParseWindow(s string) (int, error) {
	t := strings.TrimSpace(s)
	if t == "" || strings.EqualFold(t, "max") {
		return 0, nil
	}
	mult := 1
	switch {
	case strings.HasSuffix(t, "k") || strings.HasSuffix(t, "K"):
		mult, t = 1000, t[:len(t)-1]
	case strings.HasSuffix(t, "M") || strings.HasSuffix(t, "m"):
		mult, t = 1_000_000, t[:len(t)-1]
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("context window %q: tokens (200000, 200k, 1M) or max expected", s)
	}
	return int(f * float64(mult)), nil
}

// Tokens is the window of a run of the kind in tokens (0: the runtime's default). Values are validated at load.
func (w ContextWindow) Tokens(conversation bool) int {
	v := w.Work
	if conversation {
		v = w.Conversation
	}
	n, _ := ParseWindow(v)
	return n
}

// Validate checks both values against the provider's bounds.
func (w ContextWindow) Validate(p protocol.RuntimeProvider) error {
	for _, k := range []struct{ name, v string }{{"work", w.Work}, {"conversation", w.Conversation}} {
		n, err := ParseWindow(k.v)
		if err != nil {
			return fmt.Errorf("runtime context_window.%s: %w", k.name, err)
		}
		if n != 0 && (n < MinContextWindow || n > MaxContextWindow) {
			return fmt.Errorf("runtime context_window.%s %s: %s compacts between 100k and 1M tokens (or max)", k.name, k.v, p)
		}
	}
	return nil
}

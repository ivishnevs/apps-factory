package claude

import (
	"slices"
	"testing"
)

// work-and-conversation-runs 2.2: the run's context window reaches Claude Code as CLAUDE_CODE_AUTO_COMPACT_WINDOW,
// a plain token count that replaces one the agent's secrets set; without the option the environment is unchanged.
func TestCompactWindowEnv(t *testing.T) {
	env := []string{"PATH=/bin", "CLAUDE_CODE_AUTO_COMPACT_WINDOW=0000200000"}
	got := withCompactWindow(env, map[string]any{"context_window": float64(400000)})
	if !slices.Equal(got, []string{"PATH=/bin", "CLAUDE_CODE_AUTO_COMPACT_WINDOW=400000"}) {
		t.Fatalf("env %v", got)
	}
	if got := withCompactWindow(env, map[string]any{"model": "default"}); !slices.Equal(got, env) {
		t.Fatalf("env without a window %v", got)
	}
	if got := withCompactWindow(nil, map[string]any{"context_window": 200000}); len(got) < 2 || got[len(got)-1] != "CLAUDE_CODE_AUTO_COMPACT_WINDOW=200000" {
		t.Fatalf("nil env must keep the node's environment: %v", got)
	}
	if err := ValidateOptions(map[string]any{"context_window": 200000.0}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{0, 1.5, "200k"} {
		if err := ValidateOptions(map[string]any{"context_window": v}); err == nil {
			t.Fatalf("context_window %v accepted", v)
		}
	}
}

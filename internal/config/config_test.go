package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/protocol"
)

func TestRuntimeOptions(t *testing.T) {
	if err := ValidateRuntimeOptions(protocol.RuntimeCodex, map[string]any{"model": "gpt", "max_turns": 3}); err == nil || !strings.Contains(err.Error(), `"max_turns" is not supported by codex`) {
		t.Fatal("codex accepted max_turns:", err)
	}
	if err := ValidateRuntimeOptions(protocol.RuntimeCodex, map[string]any{"model": "gpt", "effort": "low"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimeOptions(protocol.RuntimeClaude, map[string]any{"model": "default", "effort": "high", "max_turns": 5}); err != nil {
		t.Fatal(err)
	}
	for _, o := range []map[string]any{{"effort": "huge"}, {"max_turns": -1}} {
		if err := ValidateRuntimeOptions(protocol.RuntimeClaude, o); err == nil {
			t.Fatal("accepted", o)
		}
	}
	a := Agent{}
	a.Spec.Runtime.Model, a.Spec.Runtime.Effort, a.Spec.Runtime.MaxTurns = "haiku", "low", 8
	if o := a.RuntimeOptions(); len(o) != 3 || o["model"] != "haiku" || o["effort"] != "low" || o["max_turns"] != 8 {
		t.Fatal(o)
	}
	a.Spec.Runtime.Effort, a.Spec.Runtime.MaxTurns = "", 0
	if o := a.RuntimeOptions(); len(o) != 1 {
		t.Fatal("unset options sent:", o)
	}
	if err := ValidateRuntimeOptions("imaginary", map[string]any{}); err == nil {
		t.Fatal("unknown provider accepted")
	}
}

// TestModelFits: a task's model override must fit the assignee's runtime provider (runtime-drivers 2.7).
func TestModelFits(t *testing.T) {
	for _, c := range []struct {
		provider protocol.RuntimeProvider
		model    string
		fits     bool
	}{
		{protocol.RuntimeClaude, "default", true},
		{protocol.RuntimeClaude, "opus", true},
		{protocol.RuntimeClaude, "sonnet[1m]", true},
		{protocol.RuntimeClaude, "claude-opus-4-1", true},
		{protocol.RuntimeClaude, "gpt-5-codex", false},
		{protocol.RuntimeClaude, "o3", false},
		{protocol.RuntimeCodex, "default", true},
		{protocol.RuntimeCodex, "gpt-5-codex", true},
		{protocol.RuntimeCodex, "o4-mini", true},
		{protocol.RuntimeCodex, "opus", false},
		{protocol.RuntimeCodex, "claude-sonnet-4-5", false},
		{protocol.RuntimeCodex, "gpt-5[1m]", false},
		{protocol.RuntimeCodex, "", false},
		{protocol.RuntimeCodex, "gpt 5; rm", false},
		{"imaginary", "gpt-5", false},
	} {
		if got := ModelFits(c.provider, c.model); got != c.fits {
			t.Errorf("ModelFits(%s, %q) = %v", c.provider, c.model, got)
		}
	}
}

func TestLoad(t *testing.T) {
	agents, err := Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	if agents["demo"].Timeout.Seconds() != 300 || agents["demo"].Instructions == "" {
		t.Fatal("agent not loaded")
	}
	root := t.TempDir()
	if err = os.MkdirAll(filepath.Join(root, "configs/agents"), 0700); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../configs/agents/demo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{strings.Replace(string(b), "provider: codex", "provider: imaginary", 1), strings.Replace(string(b), "provider: codex", "provider: codex\n    max_turns: 3", 1), string(b) + "unknown: true\n", string(b) + "---\nkind: Agent\n", strings.Replace(string(b), "file: prompts/demo.md", "file: ../../secret", 1)} {
		if err = os.WriteFile(filepath.Join(root, "configs/agents/a.yaml"), []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = Load(root); err == nil {
			t.Fatal("accepted invalid config")
		}
	}
}

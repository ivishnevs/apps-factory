package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/protocol"
)

func TestRuntimeOptions(t *testing.T) {
	if err := ValidateRuntimeOptions(protocol.RuntimeCodex, map[string]any{"model": "gpt", "effort": "high"}); err == nil || !strings.Contains(err.Error(), `"effort" is not supported by codex`) {
		t.Fatal("codex accepted effort:", err)
	}
	if err := ValidateRuntimeOptions(protocol.RuntimeClaude, map[string]any{"model": "default", "effort": "high"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntimeOptions("imaginary", map[string]any{}); err == nil {
		t.Fatal("unknown provider accepted")
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
	for _, input := range []string{strings.Replace(string(b), "provider: codex", "provider: imaginary", 1), strings.Replace(string(b), "provider: codex", "provider: codex\n    effort: high", 1), string(b) + "unknown: true\n", string(b) + "---\nkind: Agent\n", strings.Replace(string(b), "file: prompts/demo.md", "file: ../../secret", 1)} {
		if err = os.WriteFile(filepath.Join(root, "configs/agents/a.yaml"), []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = Load(root); err == nil {
			t.Fatal("accepted invalid config")
		}
	}
}

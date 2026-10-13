package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	for _, input := range []string{strings.Replace(string(b), "provider: codex", "provider: imaginary", 1), string(b) + "unknown: true\n", string(b) + "---\nkind: Agent\n", strings.Replace(string(b), "file: prompts/demo.md", "file: ../../secret", 1)} {
		if err = os.WriteFile(filepath.Join(root, "configs/agents/a.yaml"), []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = Load(root); err == nil {
			t.Fatal("accepted invalid config")
		}
	}
}

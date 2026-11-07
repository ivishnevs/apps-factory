package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetDriverKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "node.yaml")
	in := "name: \"n\"\ndrivers:\n  codex: {enabled: true, binary: \"/x/codex\"}\n  claude:\n    enabled: true\n"
	if err := os.WriteFile(p, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := setDriverKey(p, "codex", "home", "/n/runtimes/codex"); err != nil {
		t.Fatal(err)
	}
	if err := setDriverKey(p, "claude", "config_dir", "/n/runtimes/claude"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `codex: {enabled: true, binary: "/x/codex", home: "/n/runtimes/codex"}`) || !strings.Contains(string(b), "  claude:\n    config_dir: \"/n/runtimes/claude\"\n    enabled: true") {
		t.Fatal(string(b))
	}
	if err := setDriverKey(p, "codex", "home", "/again"); err == nil {
		t.Fatal("set twice")
	}
}

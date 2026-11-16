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
	if err := setDriverKey(p, "claude", "config_dir", "/again"); err == nil {
		t.Fatal("set twice in a block")
	}
	// A login again (another subscription) into the node's own directory keeps node.yaml as it is.
	before, _ := os.ReadFile(p)
	if err := setDriverKey(p, "codex", "home", "/n/runtimes/codex"); err != nil {
		t.Fatal(err)
	}
	if err := setDriverKey(p, "claude", "config_dir", "/n/runtimes/claude"); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(p); string(after) != string(before) {
		t.Fatal(string(after))
	}
}

// TestLoginCodexDeviceAuth: -device-auth signs the node's Codex in with a code (a server without a browser), in the
// node's own directory, and a login again keeps node.yaml.
func TestLoginCodexDeviceAuth(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$CODEX_HOME $*\" >> "+args+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(cfg, []byte("name: \"n\"\nstate_dir: \""+filepath.Join(dir, "state")+"\"\ndrivers:\n  codex: {enabled: true, binary: \""+bin+"\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if code, err := runtimeLogin("codex", []string{"-config", cfg, "-device-auth"}); code != 0 || err != nil {
			t.Fatal(code, err)
		}
	}
	home := filepath.Join(dir, "runtimes", "codex")
	if b, _ := os.ReadFile(args); string(b) != home+" login --device-auth\n"+home+" login --device-auth\n" {
		t.Fatalf("%q", b)
	}
	if b, _ := os.ReadFile(cfg); !strings.Contains(string(b), `home: "`+home+`"`) {
		t.Fatal(string(b))
	}
	if code, _ := runtimeLogin("claude", []string{"-config", cfg, "-device-auth"}); code != 2 {
		t.Fatal("device-auth accepted for claude")
	}
}

// TestAccountsAcceptFlagOrder: accept RUNTIME works with -config before or after it; an unknown runtime is refused.
func TestAccountsAcceptFlagOrder(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	cfg := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(cfg, []byte("name: \"n\"\nstate_dir: \""+state+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(state, "accounts", "codex.json")
	for _, args := range [][]string{{"-config", cfg, "accept", "codex"}, {"accept", "codex", "-config", cfg}} {
		if err := os.MkdirAll(filepath.Dir(rec), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rec, []byte(`{"runtime":"codex","hash":"old"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if code, err := accounts(args); code != 0 || err != nil {
			t.Fatal(args, code, err)
		}
		if fileExists(rec) {
			t.Fatal(args, "old account still recorded")
		}
	}
	if code, _ := accounts([]string{"accept", "gemini", "-config", cfg}); code != 2 {
		t.Fatal("unknown runtime accepted")
	}
}

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

// TestLoginClaudeExitAfterLogin: Claude Code exiting non-zero after /login (a declined folder trust question) still
// records the node's directory when `claude auth status` there shows the subscription login; without that login,
// or with only a token from the environment, the login fails and node.yaml stays as it is.
func TestLoginClaudeExitAfterLogin(t *testing.T) {
	for _, c := range []struct {
		name, status string
		ok           bool
	}{
		{"saved", `{"loggedIn": true, "authMethod": "claude.ai", "subscriptionType": "max"}`, true},
		{"none", `{"loggedIn": false, "authMethod": "none"}`, false},
		{"env token", `{"loggedIn": true, "authMethod": "oauth_token"}`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "claude")
			script := "#!/bin/sh\nif [ \"$1\" = auth ]; then echo '" + c.status + "'; exit 0; fi\nexit 1\n"
			if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			cfg := filepath.Join(dir, "node.yaml")
			in := "name: \"n\"\nstate_dir: \"" + filepath.Join(dir, "state") + "\"\ndrivers:\n  claude: {enabled: true, executable: \"" + bin + "\"}\n"
			if err := os.WriteFile(cfg, []byte(in), 0o600); err != nil {
				t.Fatal(err)
			}
			code, err := runtimeLogin("claude", []string{"-config", cfg})
			b, _ := os.ReadFile(cfg)
			saved := strings.Contains(string(b), `config_dir: "`+filepath.Join(dir, "runtimes", "claude")+`"`)
			if c.ok != (code == 0 && err == nil) || c.ok != saved {
				t.Fatalf("code %d err %v node.yaml %s", code, err, b)
			}
		})
	}
}

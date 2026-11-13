package codex

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"appfactory/internal/node"
)

// TestProbeClassifiesTransient: `codex --version` or `codex login status` killed by the timeout is a
// transient failure; a wrong version or a logged-out account is definitive.
func TestProbeClassifiesTransient(t *testing.T) {
	killed := exec.Command("sh", "-c", "kill -9 $$").Run()
	if killed == nil {
		t.Fatal("sh survived kill -9")
	}
	for _, c := range []struct {
		name           string
		version, login func() (string, string, error)
		check          string
		transient      bool
	}{
		{"version killed", func() (string, string, error) { return "", "", killed }, nil, "codex.version", true},
		{"login killed", nil, func() (string, string, error) { return "", "", killed }, "codex.auth", true},
		{"wrong version", func() (string, string, error) { return "codex-cli 0.1.0\n", "", nil }, nil, "codex.version", false},
		{"logged out", nil, func() (string, string, error) { return "Not logged in\n", "", errors.New("exit status 1") }, "codex.auth", false},
	} {
		version, login := c.version, c.login
		if version == nil {
			version = func() (string, string, error) { return SupportedVersion + "\n", "", nil }
		}
		if login == nil {
			login = func() (string, string, error) { return "Logged in using ChatGPT\n", "", nil }
		}
		h := node.Host{
			LookPath: func(string) (string, error) { return "/usr/local/bin/codex", nil },
			Run: func(_ context.Context, _ string, args ...string) (string, string, error) {
				if len(args) == 1 && args[0] == "--version" {
					return version()
				}
				return login()
			},
		}
		var fail *node.CheckResult
		for _, r := range (Driver{Host: &h}).Probe(context.Background()) {
			if r.Status == node.Fail {
				fail = &r
			}
		}
		if fail == nil || fail.Check != c.check || fail.Transient != c.transient {
			t.Errorf("%s: %+v", c.name, fail)
		}
	}
}

// TestProbeCodeModeHost: a Codex installed from codex.lock (.codex.pin beside it) without codex-code-mode-host beside
// it fails the probe although `codex --version` passes (every run would fail at start); with the host it passes; a
// Codex the person installed is not checked for it.
func TestProbeCodeModeHost(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
	for _, f := range []string{bin, filepath.Join(dir, ".codex.pin")} {
		if err := os.WriteFile(f, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h := node.Host{
		LookPath: func(string) (string, error) { return bin, nil },
		Stat:     func(p string) (fs.FileInfo, error) { return os.Stat(p) },
		Run: func(_ context.Context, _ string, args ...string) (string, string, error) {
			if len(args) == 1 && args[0] == "--version" {
				return SupportedVersion + "\n", "", nil
			}
			return "Logged in using ChatGPT\n", "", nil
		},
	}
	status := func() node.Status {
		for _, r := range (Driver{Host: &h}).Probe(context.Background()) {
			if r.Check == "codex.code_mode_host" {
				return r.Status
			}
		}
		return ""
	}
	if s := status(); s != node.Fail {
		t.Fatal("a pinned Codex without its code-mode host:", s)
	}
	if err := os.WriteFile(filepath.Join(dir, "codex-code-mode-host"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if s := status(); s != node.Pass {
		t.Fatal("a pinned Codex with its code-mode host:", s)
	}
	_ = os.Remove(filepath.Join(dir, ".codex.pin"))
	_ = os.Remove(filepath.Join(dir, "codex-code-mode-host"))
	if s := status(); s != "" {
		t.Fatal("the person's own Codex was checked for the host:", s)
	}
}

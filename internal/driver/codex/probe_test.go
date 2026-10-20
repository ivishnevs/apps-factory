package codex

import (
	"context"
	"errors"
	"os/exec"
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

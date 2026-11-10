package claude

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"appfactory/internal/node"
)

// linuxHost is a Linux node with the single executable whose probe passes; bwrapOK says whether the sandbox runs.
func linuxHost(onPath map[string]bool, bwrapOK bool) node.Host {
	return node.Host{GOOS: "linux",
		Getenv: func(string) string { return "" },
		LookPath: func(n string) (string, error) {
			if onPath[n] {
				return "/usr/bin/" + n, nil
			}
			return "", errors.New("not on PATH")
		},
		Stat: func(p string) (fs.FileInfo, error) {
			if p == "/opt/af/bin/factory-claude" {
				return os.Stat("probe_test.go")
			}
			return nil, fs.ErrNotExist
		},
		Run: func(_ context.Context, name string, args ...string) (string, string, error) {
			switch name {
			case "/opt/af/bin/factory-claude":
				return `{"event":"probe","claude_code_version":"` + SupportedClaudeCode + `","sdk_version":"` + SupportedSDK + `","account":{"subscriptionType":"max","apiKeySource":"none"}}` + "\n", "", nil
			case "bwrap":
				if bwrapOK {
					return "", "", nil
				}
				return "", "bwrap: setting up uid map: Permission denied\n", errors.New("exit status 1")
			case "sysctl":
				return "1\n", "", nil
			}
			return "", "", errors.New("unexpected command " + name)
		}}
}

func failing(rs []node.CheckResult) map[string]node.CheckResult {
	out := map[string]node.CheckResult{}
	for _, r := range rs {
		if r.Status == node.Fail {
			out[r.Check] = r
		}
	}
	return out
}

// TestProbeRefusesRoot (node-readiness 3.1): on a Linux node running as root the Claude driver is not usable, with
// the --os-user fix, before anything runs; a non-root node passes.
func TestProbeRefusesRoot(t *testing.T) {
	h := linuxHost(nil, true)
	d := Driver{Host: &h, BinDir: "/opt/af/bin", Euid: func() int { return 0 }}
	f := failing(d.Probe(context.Background()))
	if r, ok := f["claude.root"]; !ok || !strings.Contains(r.Hint, "--os-user factory") || len(f) != 1 {
		t.Fatalf("root node: %+v", f)
	}
	d.Euid = func() int { return 1000 }
	if f = failing(d.Probe(context.Background())); len(f) != 0 {
		t.Fatalf("user node: %+v", f)
	}
}

// TestProbeRunsSandbox (node-readiness 3.2): with sandbox: worktree on Linux the probe runs bwrap with a user
// namespace and needs socat; a failure disables the driver with the cause and the fix. Without the sandbox setting
// it is not checked (seen 2026-11-08 on hetzner-hel1: the probe passed and every confined run failed).
func TestProbeRunsSandbox(t *testing.T) {
	user := func() int { return 1000 }
	h := linuxHost(map[string]bool{"bwrap": true}, true)
	d := Driver{Host: &h, BinDir: "/opt/af/bin", Euid: user}
	if f := failing(d.Probe(context.Background())); len(f) != 0 {
		t.Fatalf("sandbox off: %+v", f)
	}
	d.Sandbox = "worktree"
	f := failing(d.Probe(context.Background()))
	if r, ok := f["claude.sandbox"]; !ok || !strings.Contains(r.Detail, "socat") || !strings.Contains(r.Hint, "apt-get install -y bubblewrap socat") {
		t.Fatalf("no socat: %+v", f)
	}
	h = linuxHost(map[string]bool{"bwrap": true, "socat": true}, false)
	f = failing(d.Probe(context.Background()))
	if r, ok := f["claude.sandbox"]; !ok || !strings.Contains(r.Hint, "apparmor_parser -r /etc/apparmor.d/bwrap") {
		t.Fatalf("apparmor: %+v", f)
	}
	h = linuxHost(map[string]bool{"bwrap": true, "socat": true}, true)
	if f = failing(d.Probe(context.Background())); len(f) != 0 {
		t.Fatalf("sandbox works: %+v", f)
	}
}

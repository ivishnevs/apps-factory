package node

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestRootCheck (node-readiness 3.1): only a Linux node running as root fails, with the --os-user fix.
func TestRootCheck(t *testing.T) {
	if r := RootCheck("claude.root", "darwin", 0); r != nil {
		t.Fatalf("macOS root: %+v", r)
	}
	if r := RootCheck("claude.root", "linux", 1000); r != nil {
		t.Fatalf("linux user: %+v", r)
	}
	r := RootCheck("claude.root", "linux", 0)
	if r == nil || r.Status != Fail || !strings.Contains(r.Hint, "--os-user factory") {
		t.Fatalf("linux root: %+v", r)
	}
}

func sandboxHost(path map[string]bool, bwrapErr, sysctl string) Host {
	return Host{GOOS: "linux",
		LookPath: func(n string) (string, error) {
			if path[n] {
				return "/usr/bin/" + n, nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, name string, args ...string) (string, string, error) {
			switch name {
			case "bwrap":
				if bwrapErr != "" {
					return "", bwrapErr + "\n", errors.New("exit status 1")
				}
				return "", "", nil
			case "sysctl":
				return sysctl + "\n", "", nil
			}
			return "", "", errors.New("unexpected " + name)
		}}
}

// TestSandboxCheck (node-readiness 3.2): missing packages, an AppArmor-restricted user namespace and a working
// sandbox each give their own result and fix; macOS is not checked.
func TestSandboxCheck(t *testing.T) {
	ctx := context.Background()
	if r := SandboxCheck(ctx, Host{GOOS: "darwin"}, "claude.sandbox"); r != nil {
		t.Fatalf("macOS: %+v", r)
	}
	r := SandboxCheck(ctx, sandboxHost(map[string]bool{"bwrap": true}, "", ""), "claude.sandbox")
	if r == nil || r.Status != Fail || !strings.Contains(r.Detail, "socat") || !strings.Contains(r.Hint, "bubblewrap socat") {
		t.Fatalf("no socat: %+v", r)
	}
	r = SandboxCheck(ctx, sandboxHost(map[string]bool{"bwrap": true, "socat": true}, "bwrap: setting up uid map: Permission denied", "1"), "claude.sandbox")
	if r == nil || r.Status != Fail || !strings.Contains(r.Detail, "uid map") || !strings.Contains(r.Hint, "/etc/apparmor.d/bwrap") || !strings.Contains(r.Hint, "userns,") || r.Transient {
		t.Fatalf("apparmor: %+v", r)
	}
	r = SandboxCheck(ctx, sandboxHost(map[string]bool{"bwrap": true, "socat": true}, "bwrap: No permissions to create new namespace", "0"), "claude.sandbox")
	if r == nil || r.Status != Fail || strings.Contains(r.Hint, "apparmor") {
		t.Fatalf("userns off: %+v", r)
	}
	r = SandboxCheck(ctx, sandboxHost(map[string]bool{"bwrap": true, "socat": true}, "", ""), "claude.sandbox")
	if r == nil || r.Status != Pass {
		t.Fatalf("ok: %+v", r)
	}
}

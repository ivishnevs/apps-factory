package node

import (
	"context"
	"strings"
)

// Linux readiness checks of a runtime driver (node-readiness 3.1, 3.2): what makes runs fail on a node whose probe
// otherwise passes. Seen 2026-11-07/08: a root node (Claude Code refuses --dangerously-skip-permissions as root) and
// a sandboxed node without bubblewrap/socat or with user namespaces restricted by AppArmor.

// RootHint is the fix for a node installed as root.
const RootHint = "reinstall the node for a separate user: curl -fsSL <core>/install.sh | sh -s -- --os-user factory"

// RootCheck fails check (e.g. "claude.root") on a Linux node running as root (euid 0); nil elsewhere. Agents must not
// have root on the host, and Claude Code does not run agents as root at all.
func RootCheck(check, goos string, euid int) *CheckResult {
	if goos != "linux" || euid != 0 {
		return nil
	}
	return &CheckResult{Check: check, Status: Fail, Detail: "the node runs as root; Claude Code refuses to run agents as root/sudo", Hint: RootHint}
}

// SandboxPackagesHint installs what Claude Code's Linux sandbox needs.
const SandboxPackagesHint = "sudo apt-get install -y bubblewrap socat (Fedora: sudo dnf install bubblewrap socat)"

// BwrapAppArmorProfile lets bwrap create user namespaces on Ubuntu 23.10+ (kernel.apparmor_restrict_unprivileged_userns=1).
const BwrapAppArmorProfile = `abi <abi/4.0>,
include <tunables/global>
profile bwrap /usr/bin/bwrap flags=(unconfined) {
  userns,
  include if exists <local/bwrap>
}`

// SandboxAppArmorHint is the fix for restricted user namespaces: the profile for /usr/bin/bwrap, loaded.
const SandboxAppArmorHint = "allow bwrap user namespaces: write this to /etc/apparmor.d/bwrap and run sudo apparmor_parser -r /etc/apparmor.d/bwrap:\n" + BwrapAppArmorProfile

// SandboxCheck runs the Linux sandbox once (bwrap with a user namespace) and looks for socat, as Claude Code's
// `sandbox: worktree` needs them; nil when not on Linux. check names the result (e.g. "claude.sandbox").
func SandboxCheck(ctx context.Context, h Host, check string) *CheckResult {
	if h.GOOS != "linux" {
		return nil
	}
	var missing []string
	for _, b := range []string{"bwrap", "socat"} {
		if _, err := h.LookPath(b); err != nil {
			missing = append(missing, b)
		}
	}
	if len(missing) > 0 {
		return &CheckResult{Check: check, Status: Fail, Detail: "sandbox required but unavailable: " + strings.Join(missing, ", ") + " not installed", Hint: SandboxPackagesHint}
	}
	out, errOut, err := h.Run(ctx, "bwrap", "--unshare-user", "--ro-bind", "/", "/", "true")
	if err != nil {
		detail := "sandbox required but unavailable: bwrap cannot create a user namespace: " + FirstLine(errOut+out)
		if FirstLine(errOut+out) == "" {
			detail += err.Error()
		}
		hint := "check that unprivileged user namespaces are allowed (sysctl kernel.unprivileged_userns_clone=1)"
		if v, _ := h.Output(ctx, "sysctl", "-n", "kernel.apparmor_restrict_unprivileged_userns"); v == "1" {
			hint = SandboxAppArmorHint
		}
		return &CheckResult{Check: check, Status: Fail, Detail: detail, Hint: hint, Transient: TransientRun(out, errOut, err)}
	}
	return &CheckResult{Check: check, Status: Pass, Detail: "bwrap user namespace and socat available"}
}

// Package readiness tells a node's environment failures from a task's own (node-readiness): the causes that make a
// run end before its agent starts because of the machine (repository access, worktree, runtime binary, sandbox,
// missing tool), and the per-project check a node runs so Core offers a project's runs only to ready nodes. The
// node and Core share it: the node classifies its own failures, Core classifies what older nodes report as text.
package readiness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Fix hints shown on the node's card and in the task's wait reason.
const (
	FixRepoAccess = "give this node's user access to the repository: gh auth login && gh auth setup-git, an SSH key or credential helper, or share the repository with the node's GitHub account; then Check again"
	FixWorktree   = "check the node's workspace root (free disk, permissions, git); factory-node doctor shows the details"
	FixRuntime    = "reinstall or update the node package (install.sh) so the runtime binary is next to it, or set its path in node.yaml; factory-node doctor"
	FixRoot       = "Claude Code does not run agents as root: reinstall the node with install.sh --os-user factory"
	FixSandbox    = "install the sandbox tools: sudo apt-get install -y bubblewrap socat; on Ubuntu 23.10+ also add the AppArmor profile for /usr/bin/bwrap (docs/node-install.md); factory-node doctor"
)

// pattern is one known node-side failure text.
type pattern struct {
	kind, fix string
	match     []string // lower-case substrings
	// started: the text also identifies a run whose runtime was launched (outcome failed): the runtime refused
	// to start. Repository and worktree texts are trusted only before the runtime started (setup_failed).
	started bool
}

var patterns = []pattern{
	{kind: protocol.FaultRuntime, fix: FixRoot, started: true, match: []string{"cannot be used with root/sudo privileges"}},
	{kind: protocol.FaultSandbox, fix: FixSandbox, started: true, match: []string{"sandbox required but unavailable", "bubblewrap (bwrap) not installed", "bwrap: setting up uid map", "bwrap: no permissions to create new namespace"}},
	{kind: protocol.FaultRuntime, fix: FixRuntime, started: true, match: []string{"native cli binary for", "claude code native binary not found"}},
	{kind: protocol.FaultRepoAccess, fix: FixRepoAccess, match: []string{"repository not found", "could not read from remote repository", "permission denied (publickey)",
		"authentication failed for", "could not read username for", "terminal prompts disabled", "the requested url returned error: 403",
		"the requested url returned error: 401", "host key verification failed"}},
}

// FixNetwork is the fix of a run that could not reach its runtime's API host.
const FixNetwork = "the node cannot resolve or reach the runtime's API host: check its VPN or proxy (Cloudflare WARP), a stale resolver (Tailscale DNS) and its DNS settings; factory-node doctor shows net.dns and net.api; runbook dns-and-proxy of the factory-setup skill"

// networkErrors are the runtime's network error texts (Node.js and Go); networkHosts the runtimes' API hosts. A run
// is a network fault only when its end names both (setup-assistant 4.2: ENOTFOUND for api.anthropic.com on
// 2026-11-11).
var (
	networkErrors = []string{"enotfound", "econnrefused", "eai_again", "getaddrinfo", "no such host", "connection refused", "could not resolve host"}
	networkHosts  = []string{"api.anthropic.com", "chatgpt.com", "api.openai.com", "auth.openai.com"}
)

// NetworkFault is a run end caused by the node's network to the runtime's API host (nil when result does not say
// so). It holds after the agent worked too: the runtime's own request failed, so the attempt is the node's.
func NetworkFault(outcome, result string) *protocol.NodeFault {
	if outcome != "setup_failed" && outcome != "failed" {
		return nil
	}
	m := strings.ToLower(result)
	if !containsAny(m, networkErrors) || !containsAny(m, networkHosts) {
		return nil
	}
	return &protocol.NodeFault{Kind: protocol.FaultNetwork, Detail: excerpt(result), Fix: FixNetwork}
}

func containsAny(s string, subs []string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// Classify says whether a run's end is the node's fault (nil when it is not or the text is unknown). outcome is
// the run's outcome: setup_failed (nothing of the agent ran) trusts every known cause and a worktree failure;
// failed trusts only runtime start refusals, and only when the runtime used nothing (usedTokens false), since an
// agent's own final text may quote anything. A network error to the API host (NetworkFault) counts either way.
func Classify(outcome, result string, usedTokens bool) *protocol.NodeFault {
	if f := NetworkFault(outcome, result); f != nil {
		return f
	}
	if outcome != "setup_failed" && (outcome != "failed" || usedTokens) {
		return nil
	}
	if strings.HasPrefix(result, "workspace_diverged") {
		return nil // the task's worktree changed outside any run: the task's problem (task-worktrees D4)
	}
	m := strings.ToLower(result)
	for _, p := range patterns {
		if outcome == "failed" && !p.started {
			continue
		}
		for _, s := range p.match {
			if strings.Contains(m, s) {
				return &protocol.NodeFault{Kind: p.kind, Detail: excerpt(result), Fix: p.fix}
			}
		}
	}
	if outcome == "setup_failed" && strings.HasPrefix(result, "worktree: ") {
		return &protocol.NodeFault{Kind: protocol.FaultWorktree, Detail: excerpt(result), Fix: FixWorktree}
	}
	return nil
}

func excerpt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}

// Run executes a command for a check and returns its stdout and stderr (node.Host.Run).
type Run func(ctx context.Context, name string, args ...string) (stdout, stderr string, err error)

// Key identifies a check's contents: a node runs it again when the key changes (a new repository, new
// requirements, or Core's new generation).
func Key(c protocol.ProjectCheck) string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// Check runs a project's readiness check: repository access (git ls-remote, never prompting) and each required
// tool. It returns the faults; none means ready.
func Check(ctx context.Context, run Run, c protocol.ProjectCheck) []protocol.NodeFault {
	var out []protocol.NodeFault
	if c.Repo != "" {
		if f := RepoAccess(ctx, run, c.Repo); f != nil {
			out = append(out, *f)
		}
	}
	return append(out, Requirements(ctx, run, c.Requires)...)
}

// RepoAccess checks that the node's user can read repo without a prompt (forge.repo); nil when it can.
func RepoAccess(ctx context.Context, run Run, repo string) *protocol.NodeFault {
	// env: a credential prompt must fail at once instead of waiting for a terminal nobody watches.
	stdout, stderr, err := run(ctx, "env", "GIT_TERMINAL_PROMPT=0", "git", "ls-remote", "--exit-code", repo, "HEAD")
	if err == nil {
		return nil
	}
	why := firstLine(stderr)
	if why == "" {
		why = firstLine(stdout)
	}
	if why == "" {
		why = err.Error()
	}
	return &protocol.NodeFault{Kind: protocol.FaultRepoAccess, Detail: "cannot read " + repo + ": " + why, Fix: FixRepoAccess}
}

// Requirements checks each tool: on PATH and, with a version, at least that version.
func Requirements(ctx context.Context, run Run, rs []tasks.Requirement) []protocol.NodeFault {
	var out []protocol.NodeFault
	for _, r := range rs {
		if f := Requirement(ctx, run, r); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// Requirement checks one tool; nil when the node has it.
func Requirement(ctx context.Context, run Run, r tasks.Requirement) *protocol.NodeFault {
	if r.Validate() != nil {
		return &protocol.NodeFault{Kind: protocol.FaultTool, Detail: "invalid requirement " + r.String(), Fix: "fix the project's requires"}
	}
	args := r.VersionArgs
	if len(args) == 0 {
		args = []string{"--version"}
	}
	fix := "install " + r.String() + " for the node's user (on its PATH), then Check again"
	stdout, stderr, err := run(ctx, r.Command, args...)
	if err != nil && strings.TrimSpace(stdout+stderr) == "" {
		return &protocol.NodeFault{Kind: protocol.FaultTool, Detail: r.Command + " not found or not runnable: " + err.Error(), Fix: fix}
	}
	if r.Version == "" {
		if err != nil {
			return &protocol.NodeFault{Kind: protocol.FaultTool, Detail: r.Command + " " + strings.Join(args, " ") + " failed: " + firstLine(stderr+stdout), Fix: fix}
		}
		return nil
	}
	have := FindVersion(stdout + "\n" + stderr)
	if have == "" {
		return &protocol.NodeFault{Kind: protocol.FaultTool, Detail: fmt.Sprintf("%s: no version in the output of %s %s", r.Command, r.Command, strings.Join(args, " ")), Fix: fix}
	}
	if CompareVersions(have, r.Version) < 0 {
		return &protocol.NodeFault{Kind: protocol.FaultTool, Detail: fmt.Sprintf("%s %s is older than the required %s", r.Command, have, r.Version), Fix: "update " + r.Command + " to " + r.Version + " or newer for the node's user, then Check again"}
	}
	return nil
}

var (
	dottedRE = regexp.MustCompile(`[0-9]+(\.[0-9]+)+`)
	numberRE = regexp.MustCompile(`[0-9]+`)
)

// FindVersion is the first dotted version in s ("" when none): "Godot Engine v4.7.2.stable" is 4.7.2.
func FindVersion(s string) string {
	if v := dottedRE.FindString(s); v != "" {
		return v
	}
	return numberRE.FindString(s)
}

// CompareVersions compares dotted numeric versions: -1, 0 or 1; missing parts are 0.
func CompareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// Summary is one line naming the faults: "cannot read …; godot not found …".
func Summary(fs []protocol.NodeFault) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = f.Detail
	}
	return strings.Join(parts, "; ")
}

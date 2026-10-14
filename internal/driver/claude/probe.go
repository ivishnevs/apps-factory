package claude

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"appfactory/internal/node"
)

func (d Driver) host() node.Host {
	if d.Host != nil {
		return *d.Host
	}
	return node.LocalHost(runtime.GOOS)
}

func (d Driver) binDir() string {
	if d.BinDir == "" {
		return "bin"
	}
	return d.BinDir
}

func (d Driver) nodeBinary() string {
	if d.Node == "" {
		return "node"
	}
	return d.Node
}

// shimPath is the shim Launch runs: Shim, else the build next to BinDir (runner.Drivers' default);
// with neither set it is DefaultShim.
func (d Driver) shimPath() string {
	if d.Shim != "" {
		return d.Shim
	}
	return filepath.Join(d.binDir(), "..", "workers", "claude", "dist", "src", "shim.js")
}

// Installed reports whether what Launch runs is present: the Node binary and the built shim.
func (d Driver) Installed() bool {
	h := d.host()
	return d.nodeFound(h) && h.Exists(d.shimPath())
}

func (d Driver) nodeFound(h node.Host) bool {
	bin := d.nodeBinary()
	if strings.ContainsRune(bin, filepath.Separator) {
		return h.Exists(bin)
	}
	_, err := h.LookPath(bin)
	return err == nil
}

// Probe checks what Launch runs: Node 22+ (Node), the built shim (Shim) and, through `shim.js --probe`, the
// Claude Code the shim drives (Executable, CLAUDE_CODE_EXECUTABLE or the SDK-bundled one): the supported
// version pair and its login. The probe asks accountInfo without a prompt, so it never makes a model call.
func (d Driver) Probe(ctx context.Context) []node.CheckResult {
	h := d.host()
	nodeCheck := d.nodeCheck(ctx, h)
	r := []node.CheckResult{nodeCheck}
	shim := filepath.Clean(d.shimPath())
	if !h.Exists(shim) {
		return append(r, node.CheckResult{Check: "claude.shim", Status: node.Fail, Detail: shim + " missing", Hint: "make build (npm ci + tsc in workers/claude)"})
	}
	r = append(r, node.CheckResult{Check: "claude.shim", Status: node.Pass, Detail: shim})
	if nodeCheck.Status == node.Fail {
		return r // the shim cannot run
	}
	p, err := d.probe(ctx, h, shim)
	if err != nil {
		hint := "check CLAUDE_CODE_EXECUTABLE or rebuild workers/claude (make build)"
		if e := (*Error)(nil); errors.As(err, &e) && e.Class == "startup_timeout" {
			hint = "Claude Code did not answer accountInfo; run claude once and check its login"
		}
		return append(r, node.CheckResult{Check: "claude.version", Status: node.Fail, Detail: err.Error(), Hint: hint})
	}
	if v, err := p.version(); err != nil {
		r = append(r, node.CheckResult{Check: "claude.version", Status: node.Fail, Detail: err.Error(),
			Hint: "unset CLAUDE_CODE_EXECUTABLE (the shim then runs the SDK-bundled Claude Code) or rebuild workers/claude"})
	} else {
		r = append(r, node.CheckResult{Check: "claude.version", Status: node.Pass, Detail: v})
	}
	return append(r, d.auth(h, p))
}

// probe runs `node shim.js --probe` on the host, pinning Executable like CheckVersion does.
func (d Driver) probe(ctx context.Context, h node.Host, shim string) (probeLine, error) {
	name, args := d.nodeBinary(), []string{shim, "--probe"}
	if d.Executable != "" {
		name, args = "env", append([]string{"CLAUDE_CODE_EXECUTABLE=" + d.Executable, d.nodeBinary()}, args...)
	}
	out, errOut, err := h.Run(ctx, name, args...)
	p, perr := parseProbe(out, err)
	if perr != nil && strings.TrimSpace(out) == "" && strings.TrimSpace(errOut) != "" {
		return p, &Error{Class: "probe", Message: node.FirstLine(errOut)}
	}
	return p, perr
}

func (d Driver) nodeCheck(ctx context.Context, h node.Host) node.CheckResult {
	hint := "install Node.js 22 or newer"
	v, err := h.Output(ctx, d.nodeBinary(), "--version")
	if err != nil {
		return node.CheckResult{Check: "claude.node", Status: node.Fail, Detail: "node: " + err.Error(), Hint: hint}
	}
	major, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(v, "v"), ".", 2)[0])
	if major < 22 {
		return node.CheckResult{Check: "claude.node", Status: node.Fail, Detail: "node " + v + "; required >= 22", Hint: hint}
	}
	return node.CheckResult{Check: "claude.node", Status: node.Pass, Detail: "node " + v}
}

// auth judges the probed accountInfo as a run would: the shim refuses an API-key source unless
// AllowAPIKey (or CLAUDE_ALLOW_API_KEY=1). The probe keeps ANTHROPIC_API_KEY/ANTHROPIC_AUTH_TOKEN,
// which a run without AllowAPIKey removes, so those only warn. The account email is never reported.
func (d Driver) auth(h node.Host, p probeLine) node.CheckResult {
	login := "run claude, then /login with a Claude subscription"
	a := p.Account
	allowKey := d.AllowAPIKey || h.Getenv("CLAUDE_ALLOW_API_KEY") == "1"
	set := func(s string) bool { return s != "" && s != "none" }
	switch {
	case a.APIProvider != "" && a.APIProvider != "firstParty":
		return node.CheckResult{Check: "claude.auth", Status: node.Pass, Detail: "external provider " + a.APIProvider}
	case set(a.APIKeySource) && allowKey:
		return node.CheckResult{Check: "claude.auth", Status: node.Pass, Detail: "API key (" + a.APIKeySource + ")"}
	case a.APIKeySource == "ANTHROPIC_API_KEY" || a.APIKeySource == "ANTHROPIC_AUTH_TOKEN":
		return node.CheckResult{Check: "claude.auth", Status: node.Warn, Detail: a.APIKeySource + " is set and runs remove it; the subscription login was not verified",
			Hint: "unset " + a.APIKeySource + " for factory-node, or set CLAUDE_ALLOW_API_KEY=1"}
	case set(a.APIKeySource):
		return node.CheckResult{Check: "claude.auth", Status: node.Fail, Detail: "API key source " + a.APIKeySource + "; the shim refuses API-key auth", Hint: login + ", or set CLAUDE_ALLOW_API_KEY=1"}
	case a.SubscriptionType != "" || set(a.TokenSource) || a.Email != "" || a.Organization != "":
		detail := "logged in"
		if a.SubscriptionType != "" {
			detail += " (" + a.SubscriptionType + ")"
		} else if set(a.TokenSource) {
			detail += " (" + a.TokenSource + ")"
		}
		return node.CheckResult{Check: "claude.auth", Status: node.Pass, Detail: detail}
	}
	return node.CheckResult{Check: "claude.auth", Status: node.Fail, Detail: "not logged in", Hint: login}
}

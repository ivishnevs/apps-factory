package node

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
)

// Runtime probes live with their drivers (codex.Driver.Probe); ClaudeProbe stays here until internal/driver/claude
// owns it (runtime-drivers 3.1).

// ClaudeVersion is the Claude Code version bundled with the pinned Agent SDK (docs/claude-runtime.md).
const ClaudeVersion = "2.1.284"

// ClaudeProbe checks what workers/claude needs: Node 22+, the built worker and factory-workspace, and the
// Claude Code login. The CLI (CLAUDE_CODE_EXECUTABLE or claude on PATH) reports the login without a model call;
// without it the worker's SDK-bundled Claude Code checks authentication only at run start.
type ClaudeProbe struct {
	Host   Host
	BinDir string
}

func (p ClaudeProbe) cli() (string, bool) {
	if v := p.Host.Getenv("CLAUDE_CODE_EXECUTABLE"); v != "" {
		return v, true
	}
	path, err := p.Host.LookPath("claude")
	return path, err == nil
}
func (p ClaudeProbe) worker() string {
	return filepath.Join(p.BinDir, "..", "workers", "claude", "dist", "src", "main.js")
}
func (p ClaudeProbe) Installed() bool {
	_, ok := p.cli()
	return ok || exists(p.Host, p.worker())
}

func (p ClaudeProbe) Probe(ctx context.Context) []CheckResult {
	r := []CheckResult{p.node(ctx)}
	if !exists(p.Host, p.worker()) {
		r = append(r, CheckResult{Check: "claude.worker", Status: Fail, Detail: filepath.Clean(p.worker()) + " missing", Hint: "make build (npm ci + tsc in workers/claude)"})
	} else {
		r = append(r, CheckResult{Check: "claude.worker", Status: Pass, Detail: filepath.Clean(p.worker())})
	}
	r = append(r, binary("factory-workspace", p.BinDir, p.Host))
	cli, ok := p.cli()
	if !ok {
		return append(r, CheckResult{Check: "claude.auth", Status: Warn, Detail: "claude CLI not found; login is checked only at run start",
			Hint: "install Claude Code and run claude, then /login with a Claude subscription"})
	}
	pinned := p.Host.Getenv("CLAUDE_CODE_EXECUTABLE") != ""
	v, err := p.Host.Output(ctx, cli, "--version")
	switch {
	case err != nil:
		r = append(r, CheckResult{Check: "claude.version", Status: Fail, Detail: err.Error(), Hint: "check CLAUDE_CODE_EXECUTABLE or reinstall Claude Code"})
	case pinned && !strings.HasPrefix(v, ClaudeVersion):
		r = append(r, CheckResult{Check: "claude.version", Status: Warn, Detail: v + "; verified: " + ClaudeVersion,
			Hint: "CLAUDE_CODE_EXECUTABLE overrides the SDK-bundled Claude Code; unset it or use " + ClaudeVersion})
	case pinned:
		r = append(r, CheckResult{Check: "claude.version", Status: Pass, Detail: v})
	default:
		r = append(r, CheckResult{Check: "claude.version", Status: Pass, Detail: v + " (CLI; worker runs SDK-bundled " + ClaudeVersion + ")"})
	}
	return append(r, p.auth(ctx, cli))
}

func (p ClaudeProbe) node(ctx context.Context) CheckResult {
	hint := "install Node.js 22 or newer"
	v, err := p.Host.Output(ctx, "node", "--version")
	if err != nil {
		return CheckResult{Check: "claude.node", Status: Fail, Detail: "node: " + err.Error(), Hint: hint}
	}
	major, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(v, "v"), ".", 2)[0])
	if major < 22 {
		return CheckResult{Check: "claude.node", Status: Fail, Detail: "node " + v + "; required >= 22", Hint: hint}
	}
	return CheckResult{Check: "claude.node", Status: Pass, Detail: "node " + v}
}

func (p ClaudeProbe) auth(ctx context.Context, cli string) CheckResult {
	login := "run claude, then /login with a Claude subscription"
	out, errOut, err := p.Host.Run(ctx, cli, "auth", "status")
	var s struct {
		LoggedIn         bool   `json:"loggedIn"`
		AuthMethod       string `json:"authMethod"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &s); jerr != nil {
		msg := FirstLine(errOut + "\n" + out)
		if err == nil {
			msg = "unreadable output: " + jerr.Error()
		}
		return CheckResult{Check: "claude.auth", Status: Warn, Detail: "cannot read `claude auth status`: " + msg, Hint: "update Claude Code; login is checked at run start"}
	}
	if !s.LoggedIn {
		return CheckResult{Check: "claude.auth", Status: Fail, Detail: "not logged in", Hint: login}
	}
	detail := "logged in (" + s.AuthMethod
	if s.SubscriptionType != "" {
		detail += ", " + s.SubscriptionType
	}
	detail += ")"
	if s.AuthMethod != "claude.ai" && p.Host.Getenv("CLAUDE_ALLOW_API_KEY") != "1" {
		return CheckResult{Check: "claude.auth", Status: Fail, Detail: detail + "; the worker refuses API-key auth", Hint: login + ", or set CLAUDE_ALLOW_API_KEY=1"}
	}
	return CheckResult{Check: "claude.auth", Status: Pass, Detail: detail}
}

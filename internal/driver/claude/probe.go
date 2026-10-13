package claude

import (
	"context"
	"encoding/json"
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

// executable is the pinned Claude Code binary: Executable, else CLAUDE_CODE_EXECUTABLE.
func (d Driver) executable(h node.Host) string {
	if d.Executable != "" {
		return d.Executable
	}
	return h.Getenv("CLAUDE_CODE_EXECUTABLE")
}

// cli is the Claude Code binary that reports the login: the pinned one, else claude on PATH.
func (d Driver) cli(h node.Host) (string, bool) {
	if v := d.executable(h); v != "" {
		return v, true
	}
	path, err := h.LookPath("claude")
	return path, err == nil
}

func (d Driver) binDir() string {
	if d.BinDir == "" {
		return "bin"
	}
	return d.BinDir
}
func (d Driver) worker() string {
	return filepath.Join(d.binDir(), "..", "workers", "claude", "dist", "src", "main.js")
}

// Installed reports whether a Claude Code CLI or the built worker exists.
func (d Driver) Installed() bool {
	h := d.host()
	_, ok := d.cli(h)
	return ok || h.Exists(d.worker())
}

// Probe checks what workers/claude needs: Node 22+, the built worker and factory-workspace, and the Claude Code
// login. The CLI (Executable, CLAUDE_CODE_EXECUTABLE or claude on PATH) reports the login without a model call;
// without it the worker's SDK-bundled Claude Code checks authentication only at run start.
func (d Driver) Probe(ctx context.Context) []node.CheckResult {
	h := d.host()
	r := []node.CheckResult{d.nodeCheck(ctx, h)}
	if !h.Exists(d.worker()) {
		r = append(r, node.CheckResult{Check: "claude.worker", Status: node.Fail, Detail: filepath.Clean(d.worker()) + " missing", Hint: "make build (npm ci + tsc in workers/claude)"})
	} else {
		r = append(r, node.CheckResult{Check: "claude.worker", Status: node.Pass, Detail: filepath.Clean(d.worker())})
	}
	r = append(r, h.BinaryCheck("factory-workspace", d.binDir()))
	cli, ok := d.cli(h)
	if !ok {
		return append(r, node.CheckResult{Check: "claude.auth", Status: node.Warn, Detail: "claude CLI not found; login is checked only at run start",
			Hint: "install Claude Code and run claude, then /login with a Claude subscription"})
	}
	pinned := d.executable(h) != ""
	v, err := h.Output(ctx, cli, "--version")
	switch {
	case err != nil:
		r = append(r, node.CheckResult{Check: "claude.version", Status: node.Fail, Detail: err.Error(), Hint: "check CLAUDE_CODE_EXECUTABLE or reinstall Claude Code"})
	case pinned && !strings.HasPrefix(v, SupportedClaudeCode):
		r = append(r, node.CheckResult{Check: "claude.version", Status: node.Warn, Detail: v + "; verified: " + SupportedClaudeCode,
			Hint: "CLAUDE_CODE_EXECUTABLE overrides the SDK-bundled Claude Code; unset it or use " + SupportedClaudeCode})
	case pinned:
		r = append(r, node.CheckResult{Check: "claude.version", Status: node.Pass, Detail: v})
	default:
		r = append(r, node.CheckResult{Check: "claude.version", Status: node.Pass, Detail: v + " (CLI; worker runs SDK-bundled " + SupportedClaudeCode + ")"})
	}
	return append(r, d.auth(ctx, h, cli))
}

func (d Driver) nodeCheck(ctx context.Context, h node.Host) node.CheckResult {
	hint := "install Node.js 22 or newer"
	bin := d.Node
	if bin == "" {
		bin = "node"
	}
	v, err := h.Output(ctx, bin, "--version")
	if err != nil {
		return node.CheckResult{Check: "claude.node", Status: node.Fail, Detail: "node: " + err.Error(), Hint: hint}
	}
	major, _ := strconv.Atoi(strings.SplitN(strings.TrimPrefix(v, "v"), ".", 2)[0])
	if major < 22 {
		return node.CheckResult{Check: "claude.node", Status: node.Fail, Detail: "node " + v + "; required >= 22", Hint: hint}
	}
	return node.CheckResult{Check: "claude.node", Status: node.Pass, Detail: "node " + v}
}

func (d Driver) auth(ctx context.Context, h node.Host, cli string) node.CheckResult {
	login := "run claude, then /login with a Claude subscription"
	out, errOut, err := h.Run(ctx, cli, "auth", "status")
	var s struct {
		LoggedIn         bool   `json:"loggedIn"`
		AuthMethod       string `json:"authMethod"`
		SubscriptionType string `json:"subscriptionType"`
	}
	if jerr := json.Unmarshal([]byte(strings.TrimSpace(out)), &s); jerr != nil {
		msg := node.FirstLine(errOut + "\n" + out)
		if err == nil {
			msg = "unreadable output: " + jerr.Error()
		}
		return node.CheckResult{Check: "claude.auth", Status: node.Warn, Detail: "cannot read `claude auth status`: " + msg, Hint: "update Claude Code; login is checked at run start"}
	}
	if !s.LoggedIn {
		return node.CheckResult{Check: "claude.auth", Status: node.Fail, Detail: "not logged in", Hint: login}
	}
	detail := "logged in (" + s.AuthMethod
	if s.SubscriptionType != "" {
		detail += ", " + s.SubscriptionType
	}
	detail += ")"
	if s.AuthMethod != "claude.ai" && !d.AllowAPIKey && h.Getenv("CLAUDE_ALLOW_API_KEY") != "1" {
		return node.CheckResult{Check: "claude.auth", Status: node.Fail, Detail: detail + "; the worker refuses API-key auth", Hint: login + ", or set CLAUDE_ALLOW_API_KEY=1"}
	}
	return node.CheckResult{Check: "claude.auth", Status: node.Pass, Detail: detail}
}

package codex

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"

	"appfactory/internal/node"
	"appfactory/internal/node/runtimes"
)

func (d Driver) host() node.Host {
	if d.Host != nil {
		return *d.Host
	}
	return node.LocalHost(runtime.GOOS)
}

// Installed reports whether the Codex binary is on PATH.
func (d Driver) Installed() bool {
	_, err := d.host().LookPath(d.binary())
	return err == nil
}

// Probe checks the binary, its exact supported version and the ChatGPT login (`codex login status`); no model call.
func (d Driver) Probe(ctx context.Context) []node.CheckResult {
	h := d.host()
	install := "npm install -g @openai/codex@0.162.1 (or set CODEX_BIN)"
	bin, err := h.LookPath(d.binary())
	if err != nil {
		return []node.CheckResult{{Check: "codex.binary", Status: node.Fail, Detail: d.binary() + " not found", Hint: install}}
	}
	r := []node.CheckResult{{Check: "codex.binary", Status: node.Pass, Detail: bin}}
	if c, ok := codeModeHost(h, bin); ok {
		if c.Status == node.Fail {
			return append(r, c)
		}
		r = append(r, c)
	}
	out, errOut, err := h.Run(ctx, bin, "--version")
	if err != nil {
		detail := err.Error()
		if l := node.FirstLine(errOut); l != "" {
			detail += ": " + l
		}
		return append(r, node.CheckResult{Check: "codex.version", Status: node.Fail, Detail: detail, Hint: install, Transient: node.TransientRun(out, errOut, err)})
	}
	v := strings.TrimSpace(out)
	if v != SupportedVersion {
		return append(r, node.CheckResult{Check: "codex.version", Status: node.Fail, Detail: v + "; supported: exactly " + SupportedVersion,
			Hint: install + "; a newer version needs `make schema` and a verified app-server protocol first"})
	}
	r = append(r, node.CheckResult{Check: "codex.version", Status: node.Pass, Detail: v})
	if d.Home != "" {
		out, errOut, err = h.Run(ctx, "env", "CODEX_HOME="+d.Home, bin, "login", "status")
	} else {
		out, errOut, err = h.Run(ctx, bin, "login", "status")
	}
	msg := node.FirstLine(out + "\n" + errOut)
	if err != nil {
		if node.TransientRun(out, errOut, err) {
			return append(r, node.CheckResult{Check: "codex.auth", Status: node.Fail, Detail: "login status: " + err.Error(), Hint: "codex login status was killed or did not start; retried", Transient: true})
		}
		return append(r, node.CheckResult{Check: "codex.auth", Status: node.Fail, Detail: "not logged in: " + msg, Hint: "codex login (ChatGPT account)"})
	}
	r = append(r, node.CheckResult{Check: "codex.auth", Status: node.Pass, Detail: msg})
	if d.AccountsDir != "" {
		// node-runtime-accounts: a different account than the one recorded disables Codex on this node.
		r = append(r, node.CheckAccount(d.AccountsDir, node.CodexAccount(d.Home)))
	}
	if d.Home == "" && d.AccountsDir != "" {
		r = append(r, node.CheckResult{Check: "codex.login", Status: node.Warn, Detail: "runs use this user's own Codex login: logging in to another account in the terminal changes the agents' account too",
			Hint: "give the node its own login: factory-node login codex"})
	}
	// setup-assistant 4.1: Codex reaches its endpoint through the system resolver, like this check.
	return append(r, d.Net.NetChecks(ctx, node.CodexAPIHost, "net.api")...)
}

// codeModeHost checks the code-mode host beside a Codex the node package installed from codex.lock (a .codex.pin
// beside it): Codex runs it from the file next to its executable and fails at the start of every run without it, while
// `codex --version` still passes. ok is false for a Codex the person installed (npm keeps the host elsewhere).
func codeModeHost(h node.Host, bin string) (c node.CheckResult, ok bool) {
	dir := filepath.Dir(bin)
	if h.Stat == nil || !h.Exists(filepath.Join(dir, "."+runtimes.CodexBin+".pin")) {
		return c, false
	}
	path := filepath.Join(dir, runtimes.CodexHelper)
	if !h.Exists(path) {
		return node.CheckResult{Check: "codex.code_mode_host", Status: node.Fail, Detail: path + " missing: every Codex run fails at start",
			Hint: "factory-node runtimes update codex (a package whose codex.lock pins " + runtimes.CodexHelper + ")"}, true
	}
	return node.CheckResult{Check: "codex.code_mode_host", Status: node.Pass, Detail: path}, true
}

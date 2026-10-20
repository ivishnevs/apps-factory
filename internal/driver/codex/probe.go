package codex

import (
	"context"
	"runtime"
	"strings"

	"appfactory/internal/node"
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
	out, errOut, err = h.Run(ctx, bin, "login", "status")
	msg := node.FirstLine(out + "\n" + errOut)
	if err != nil {
		if node.TransientRun(out, errOut, err) {
			return append(r, node.CheckResult{Check: "codex.auth", Status: node.Fail, Detail: "login status: " + err.Error(), Hint: "codex login status was killed or did not start; retried", Transient: true})
		}
		return append(r, node.CheckResult{Check: "codex.auth", Status: node.Fail, Detail: "not logged in: " + msg, Hint: "codex login (ChatGPT account)"})
	}
	return append(r, node.CheckResult{Check: "codex.auth", Status: node.Pass, Detail: msg})
}

// Package shim enforces command-level access rules (node-agent D6): the executor puts a per-run directory
// first on the runtime PATH with one link per guarded command to factory-shim. This is a guardrail, not a
// security boundary: an absolute path bypasses it.
package shim

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"appfactory/internal/policies"
)

const (
	// EnvDir names the run's shim directory; it holds RulesFile.
	EnvDir    = "FACTORY_SHIM_DIR"
	RulesFile = "rules.json"
	// Refused is the exit status of a refused command.
	Refused = 126
	// CommandPath is the Core endpoint deciding a command with the run token (see docs/policies.md).
	CommandPath = "/agent/command"
)

// Install writes the command rules to dir and links each guarded command to binary. It returns the
// guarded commands; none means the run needs no shims.
func Install(dir, binary string, rules []policies.Rule) ([]string, error) {
	cmds := []policies.Rule{}
	for _, r := range rules {
		if r.Type == "access" && len(r.Commands) > 0 {
			cmds = append(cmds, r)
		}
	}
	names := policies.Guarded(cmds)
	if len(names) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(cmds)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(dir, RulesFile), b, 0o600); err != nil {
		return nil, err
	}
	for _, n := range names {
		if err = os.Symlink(binary, filepath.Join(dir, n)); err != nil {
			return nil, err
		}
	}
	return names, nil
}

// Env puts dir first on PATH and names it in EnvDir.
func Env(env []string, dir string) []string {
	out := make([]string, 0, len(env)+2)
	path := ""
	for _, v := range env {
		k, val, _ := strings.Cut(v, "=")
		switch k {
		case "PATH":
			path = val
			continue
		case EnvDir:
			continue
		}
		out = append(out, v)
	}
	if path != "" {
		path = string(os.PathListSeparator) + path
	}
	return append(out, "PATH="+dir+path, EnvDir+"="+dir)
}

// LookPath finds the real executable name on path, skipping the shim directory.
func LookPath(name, path, dir string) (string, error) {
	skip := same(dir)
	for _, d := range filepath.SplitList(path) {
		if d == "" || skip(d) {
			continue
		}
		p := filepath.Join(d, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s: command not found outside the Factory shim directory", name)
}
func same(dir string) func(string) bool {
	clean := filepath.Clean(dir)
	real, _ := filepath.EvalSymlinks(clean)
	return func(d string) bool {
		if filepath.Clean(d) == clean {
			return true
		}
		r, err := filepath.EvalSymlinks(d)
		return err == nil && real != "" && r == real
	}
}

// Check is Core's answer for one command (POST CommandPath).
type Check struct {
	Effect      string `json:"effect"`
	Rule        string `json:"rule,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	ApprovalID  string `json:"approval_id,omitempty"`
}

// Ask sends the exact argv to Core. Core records refusals on the run and consumes an approved grant
// once; 200 allows, 403 denies, 423 needs approval.
func Ask(ctx context.Context, client *http.Client, base, token string, argv []string) (Check, int, error) {
	var c Check
	if base == "" || token == "" {
		return c, 0, errors.New("FACTORY_URL and FACTORY_RUN_TOKEN required")
	}
	b, _ := json.Marshal(map[string][]string{"argv": argv})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+CommandPath, bytes.NewReader(b))
	if err != nil {
		return c, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return c, 0, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if json.Unmarshal(body, &c) != nil || c.Effect == "" {
		return c, res.StatusCode, fmt.Errorf("Core HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	return c, res.StatusCode, nil
}

// GrantsDir (under the shim directory) holds executions a runtime approved with Core's single-use grant
// before running them (Codex approval requests); the shim takes one instead of asking Core again.
const GrantsDir = "grants"

// GrantTTL bounds a handed-over grant: the runtime runs the command right after accepting it.
const GrantTTL = 2 * time.Minute

func grantPath(dir string, argv []string) string {
	b, _ := json.Marshal(append([]string{filepath.Base(argv[0])}, argv[1:]...))
	sum := sha256.Sum256(b)
	return filepath.Join(dir, GrantsDir, hex.EncodeToString(sum[:]))
}

// Grant hands one approved execution of the exact argv (argv[0] reduced to its basename) to the shim.
func Grant(dir string, argv []string) error {
	p := grantPath(dir, argv)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// take consumes a handed-over grant for argv; an expired one is removed and refused.
func take(dir string, argv []string) bool {
	p := grantPath(dir, argv)
	info, err := os.Stat(p)
	if err != nil || os.Remove(p) != nil {
		return false
	}
	return time.Since(info.ModTime()) < GrantTTL
}

// ApprovalHint tells the agent how to obtain a single-use grant for the exact argv.
func ApprovalHint(argv []string, instruction string) string {
	exact, _ := json.Marshal(struct {
		Name      string              `json:"name"`
		Arguments map[string][]string `json:"arguments"`
	}{"command", map[string][]string{"argv": argv}})
	return strings.TrimSpace(instruction + " Request owner approval with factory_owner_request_approval and call " + string(exact) + ", then finish the turn; an APPROVED grant runs exactly this command once.")
}

// Shim runs one shimmed command. Exec replaces the process with the real binary (syscall.Exec).
type Shim struct {
	Getenv  func(string) string
	Stderr  io.Writer
	Client  *http.Client
	Exec    func(path string, argv, env []string) error
	Environ func() []string
}

// Main decides args (os.Args) and returns the exit status when it does not exec.
func (s Shim) Main(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return Refused
	}
	name := filepath.Base(args[0])
	fail := func(format string, v ...any) int {
		fmt.Fprintf(s.Stderr, "factory-shim: "+format+"\n", v...)
		return Refused
	}
	dir := s.Getenv(EnvDir)
	if dir == "" {
		return fail("%s is unset; refusing %s", EnvDir, name)
	}
	var rules []policies.Rule
	b, err := os.ReadFile(filepath.Join(dir, RulesFile))
	if err == nil {
		err = json.Unmarshal(b, &rules)
	}
	if err != nil {
		return fail("command rules unreadable (%v); refusing %s", err, name)
	}
	argv := append([]string{name}, args[1:]...)
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	ask := func() (Check, int, error) {
		return Ask(ctx, client, s.Getenv("FACTORY_URL"), s.Getenv("FACTORY_RUN_TOKEN"), argv)
	}
	d := policies.Command(rules, argv)
	switch d.Effect {
	case "deny":
		_, _, _ = ask() // best effort: Core records the refusal on the run
		return fail("%s is denied by policy rule %s. %s", name, d.Rule, d.Instruction)
	case "approval":
		if take(dir, argv) {
			break
		}
		c, status, err := ask()
		if err != nil {
			return fail("%s requires owner approval (rule %s) and the approval check failed: %v", name, d.Rule, err)
		}
		if status != http.StatusOK || c.Effect != "allow" {
			hint := c.Instruction
			if hint == "" || c.Effect == "approval" {
				hint = ApprovalHint(argv, d.Instruction)
			}
			return fail("%s refused (%s, rule %s). %s", name, c.Effect, d.Rule, hint)
		}
	}
	real, err := LookPath(name, s.Getenv("PATH"), dir)
	if err != nil {
		fmt.Fprintln(s.Stderr, "factory-shim:", err)
		return 127
	}
	if err = s.Exec(real, args, s.Environ()); err != nil {
		fmt.Fprintln(s.Stderr, "factory-shim:", err)
		return Refused
	}
	return 0
}

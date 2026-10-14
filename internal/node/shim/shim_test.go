package shim

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"appfactory/internal/policies"
)

var rules = []policies.Rule{
	{Name: "host-ui-input", Type: "access", Commands: []string{"cliclick"}, Effect: "deny", Instruction: "Use UI tests."},
	{Name: "push", Type: "access", Commands: []string{"git push *"}, Effect: "approval", Instruction: "Ask before pushing."},
	{Name: "ops", Type: "access", Operations: []string{"factory.x"}, Effect: "deny", Instruction: "x"},
}

func executable(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInstallEnvLookPath(t *testing.T) {
	dir, bin := t.TempDir(), t.TempDir()
	names, err := Install(dir, "/opt/factory/bin/factory-shim", rules)
	if err != nil || strings.Join(names, ",") != "cliclick,git" {
		t.Fatal(names, err)
	}
	for _, n := range names {
		if target, err := os.Readlink(filepath.Join(dir, n)); err != nil || target != "/opt/factory/bin/factory-shim" {
			t.Fatal(n, target, err)
		}
	}
	var saved []policies.Rule
	b, _ := os.ReadFile(filepath.Join(dir, RulesFile))
	if json.Unmarshal(b, &saved) != nil || len(saved) != 2 {
		t.Fatalf("%s", b)
	}
	if names, err = Install(t.TempDir(), "x", []policies.Rule{{Name: "a", Type: "access", Commands: []string{"open"}, Effect: "allow"}}); err != nil || names != nil {
		t.Fatal("allow-only rules need no shims", names)
	}

	env := Env([]string{"HOME=/h", "PATH=/usr/bin:/bin", EnvDir + "=/old"}, dir)
	if strings.Join(env, ";") != "HOME=/h;PATH="+dir+":/usr/bin:/bin;"+EnvDir+"="+dir {
		t.Fatal(env)
	}

	real := executable(t, bin, "git")
	executable(t, dir, "git2")
	link := filepath.Join(t.TempDir(), "shims")
	if err = os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	path := strings.Join([]string{dir, link + "/", "", bin}, string(os.PathListSeparator))
	if got, err := LookPath("git", path, dir); err != nil || got != real {
		t.Fatal(got, err)
	}
	if _, err := LookPath("git2", path, dir); err == nil {
		t.Fatal("found a command in the shim directory")
	}
}

// core fakes POST /agent/command with one approved single-use grant for an exact argv.
type core struct {
	mu    sync.Mutex
	grant string
	seen  []string
}

func (c *core) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Argv []string `json:"argv"`
	}
	if r.Method != "POST" || r.URL.Path != CommandPath || r.Header.Get("Authorization") != "Bearer run.tok" || json.NewDecoder(r.Body).Decode(&p) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := strings.Join(p.Argv, "\x00")
	c.seen = append(c.seen, strings.Join(p.Argv, " "))
	d := policies.Command(rules, p.Argv)
	switch {
	case d.Effect == "deny":
		w.WriteHeader(403)
		_ = json.NewEncoder(w).Encode(Check{Effect: "deny", Rule: d.Rule, Instruction: d.Instruction})
	case d.Effect == "approval" && key == c.grant:
		c.grant = ""
		_ = json.NewEncoder(w).Encode(Check{Effect: "allow", Rule: d.Rule, ApprovalID: "ap1"})
	case d.Effect == "approval":
		w.WriteHeader(423)
		_ = json.NewEncoder(w).Encode(Check{Effect: "approval", Rule: d.Rule, Instruction: "request it"})
	default:
		_ = json.NewEncoder(w).Encode(Check{Effect: "allow"})
	}
}

func TestShimDecisions(t *testing.T) {
	dir, bin := t.TempDir(), t.TempDir()
	if _, err := Install(dir, "/x/factory-shim", rules); err != nil {
		t.Fatal(err)
	}
	real := executable(t, bin, "git")
	executable(t, bin, "cliclick")
	fake := &core{grant: "git\x00push\x00origin\x00main"}
	server := httptest.NewServer(fake)
	defer server.Close()
	vars := map[string]string{EnvDir: dir, "PATH": dir + ":" + bin, "FACTORY_URL": server.URL, "FACTORY_RUN_TOKEN": "run.tok"}
	run := func(args ...string) (int, string, []string) {
		var stderr bytes.Buffer
		var execd []string
		s := Shim{Getenv: func(k string) string { return vars[k] }, Stderr: &stderr, Environ: func() []string { return []string{"A=1"} },
			Exec: func(path string, argv, env []string) error {
				execd = append([]string{path}, argv...)
				return nil
			}}
		code := s.Main(context.Background(), args)
		return code, stderr.String(), execd
	}

	code, msg, execd := run(filepath.Join(dir, "cliclick"), "c:1,1")
	if code != Refused || execd != nil || !strings.Contains(msg, "denied by policy rule host-ui-input. Use UI tests.") || len(fake.seen) != 1 || fake.seen[0] != "cliclick c:1,1" {
		t.Fatal(code, msg, execd, fake.seen)
	}
	if code, _, execd = run("git", "status"); code != 0 || strings.Join(execd, " ") != real+" git status" || len(fake.seen) != 1 {
		t.Fatal("unguarded arguments must pass without Core", code, execd, fake.seen)
	}
	if code, msg, execd = run("git", "push", "origin", "dev"); code != Refused || execd != nil || !strings.Contains(msg, `{"name":"command","arguments":{"argv":["git","push","origin","dev"]}}`) {
		t.Fatal("changed arguments passed", code, msg)
	}
	if code, _, execd = run("git", "push", "origin", "main"); code != 0 || strings.Join(execd, " ") != real+" git push origin main" {
		t.Fatal("approved command refused", code, execd)
	}
	if code, msg, execd = run("git", "push", "origin", "main"); code != Refused || execd != nil || !strings.Contains(msg, "Ask before pushing.") {
		t.Fatal("grant used twice", code, msg)
	}

	vars["FACTORY_URL"] = "http://127.0.0.1:1"
	if code, msg, _ = run("git", "push", "origin", "main"); code != Refused || !strings.Contains(msg, "approval check failed") {
		t.Fatal(code, msg)
	}
	// A runtime that consumed the grant hands it over once, for the exact argv only.
	if err := Grant(dir, []string{"/usr/bin/git", "push", "origin", "main"}); err != nil {
		t.Fatal(err)
	}
	if code, _, _ = run("git", "push", "origin", "dev"); code != Refused {
		t.Fatal("handed-over grant matched other arguments")
	}
	if code, _, execd = run("git", "push", "origin", "main"); code != 0 || execd == nil {
		t.Fatal("handed-over grant refused", code)
	}
	if code, _, _ = run("git", "push", "origin", "main"); code != Refused {
		t.Fatal("handed-over grant used twice")
	}
	_ = Grant(dir, []string{"git", "push", "origin", "main"})
	old := time.Now().Add(-GrantTTL - time.Second)
	_ = os.Chtimes(grantPath(dir, []string{"git", "push", "origin", "main"}), old, old)
	if code, _, _ = run("git", "push", "origin", "main"); code != Refused {
		t.Fatal("expired grant accepted")
	}
	if code, msg, _ = run("cliclick"); code != Refused || !strings.Contains(msg, "Use UI tests.") {
		t.Fatal("deny must not depend on Core", code, msg)
	}
	vars["PATH"] = dir
	if code, _, _ = run("git", "status"); code != 127 {
		t.Fatal(code)
	}
	vars[EnvDir] = ""
	if code, _, _ = run("git", "status"); code != Refused {
		t.Fatal(code)
	}
}

// TestDecideMode: `factory-shim --decide` judges whole command lines with the same rules, consumes a
// Core grant only for a single literal gated part and hands it to the PATH shim; failures deny.
func TestDecideMode(t *testing.T) {
	dir := t.TempDir()
	if _, err := Install(dir, "/x/factory-shim", rules); err != nil {
		t.Fatal(err)
	}
	if bin, err := Executable(dir, rules); err != nil || bin != "/x/factory-shim" {
		t.Fatal(bin, err)
	}
	fake := &core{grant: "git\x00push\x00origin\x00main"}
	server := httptest.NewServer(fake)
	defer server.Close()
	vars := map[string]string{EnvDir: dir, "FACTORY_URL": server.URL, "FACTORY_RUN_TOKEN": "run.tok"}
	decide := func(command string) Decision {
		t.Helper()
		in, _ := json.Marshal(DecideRequest{Command: command})
		var out, stderr bytes.Buffer
		s := Shim{Getenv: func(k string) string { return vars[k] }, Stdin: bytes.NewReader(in), Stdout: &out, Stderr: &stderr}
		if code := s.Main(context.Background(), []string{"/x/" + Binary, DecideFlag}); code != 0 {
			t.Fatal(code, stderr.String())
		}
		var d Decision
		if err := json.Unmarshal(out.Bytes(), &d); err != nil {
			t.Fatal(out.String())
		}
		return d
	}
	if d := decide("cd app && cliclick c:1"); d.Decision != "deny" || !strings.Contains(d.Reason, "denied by policy rule host-ui-input. Use UI tests.") {
		t.Fatal(d)
	}
	if d := decide("git status && ls | wc -l"); d.Decision != "allow" {
		t.Fatal(d)
	}
	if d := decide("git push origin main && git push origin dev"); d.Decision != "deny" || !strings.Contains(d.Reason, "separate command") {
		t.Fatal(d)
	}
	if d := decide(`git push origin "$BRANCH"`); d.Decision != "deny" || !strings.Contains(d.Reason, "literal arguments") {
		t.Fatal(d)
	}
	if len(fake.seen) != 1 { // only the denied cliclick was reported
		t.Fatal(fake.seen)
	}
	if d := decide("git push origin dev"); d.Decision != "deny" || !strings.Contains(d.Reason, `"argv":["git","push","origin","dev"]`) {
		t.Fatal(d)
	}
	if d := decide("/usr/bin/git push origin main"); d.Decision != "allow" {
		t.Fatal(d)
	}
	if !take(dir, []string{"git", "push", "origin", "main"}) {
		t.Fatal("consumed grant not handed to the PATH shim")
	}
	if d := decide("git push origin main"); d.Decision != "deny" {
		t.Fatal("grant used twice", d)
	}
	vars[EnvDir] = ""
	if d := decide("ls"); d.Decision != "deny" || !strings.Contains(d.Reason, EnvDir) {
		t.Fatal(d)
	}
	var stderr bytes.Buffer
	if code := (Shim{Getenv: func(string) string { return "" }, Stderr: &stderr}).Main(context.Background(), []string{Binary}); code != Refused {
		t.Fatal(code)
	}
}

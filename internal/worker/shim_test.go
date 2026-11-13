package worker

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"appfactory/internal/driver/fake"
	"appfactory/internal/node/shim"
	"appfactory/internal/policies"
	"appfactory/internal/protocol"
)

// buildShim builds factory-shim: runs of catalog agents always carry the mandatory command guards.
func buildShim(t *testing.T) string {
	t.Helper()
	// The package links factory-shim to factory-node (node-packaging-updates A1).
	dir := t.TempDir()
	bin := filepath.Join(dir, "factory-shim")
	if out, err := exec.Command("go", "build", "-o", filepath.Join(dir, "factory-node"), "appfactory/cmd/factory-node").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := os.Symlink("factory-node", bin); err != nil {
		t.Fatal(err)
	}
	return bin
}

// Scripted shell commands run through the per-run shim directory built from the captured rules.
func TestCommandGuards(t *testing.T) {
	bin := buildShim(t)
	tools := t.TempDir()
	for name, body := range map[string]string{"cliclick": "echo clicked", "mytool": `echo "real $*"`} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	rules := append(policies.HostUI().Spec.Rules, policies.Rule{Name: "push", Type: "access", Commands: []string{"mytool push *"}, Effect: "approval", Instruction: "Ask first."})
	snapshot, _ := json.Marshal(map[string]any{"rules": rules})
	d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "done", Activity: []fake.Activity{
		{Exec: []string{"cliclick", "c:1,1"}}, {Exec: []string{"mytool", "build"}}, {Exec: []string{"mytool", "push", "x"}},
	}}}
	r, events := runDriver(t, Worker{Driver: d, ShimBinary: bin}, false, stored(func(s *protocol.RunSpec) { s.ConfigSnapshot = snapshot }))
	if r.Outcome != "succeeded" {
		t.Fatalf("%+v", r)
	}
	got := []fake.ExecEvent{}
	for _, e := range events {
		var p protocol.Event
		var x fake.ExecEvent
		if json.Unmarshal(e.Payload, &p) == nil && p.Method == fake.ExecMethod && json.Unmarshal(p.Data, &x) == nil {
			got = append(got, x)
		}
	}
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	if got[0].Exit != shim.Refused || got[0].Stdout != "" || !strings.Contains(got[0].Stderr, "accessibility identifier") {
		t.Fatalf("cliclick %+v", got[0])
	}
	if got[1].Exit != 0 || got[1].Stdout != "real build\n" {
		t.Fatalf("pass-through %+v", got[1])
	}
	// Without a grant (no Core command endpoint or no approval) the approval command is refused.
	if got[2].Exit != shim.Refused || got[2].Stdout != "" || !strings.Contains(got[2].Stderr, "rule push") {
		t.Fatalf("approval %+v", got[2])
	}
	l := d.Launches()[0]
	var dir, path string
	for _, v := range l.Env {
		k, val, _ := strings.Cut(v, "=")
		switch k {
		case shim.EnvDir:
			dir = val
		case "PATH":
			path = val
		}
	}
	if dir == "" || !strings.HasPrefix(path, dir+string(os.PathListSeparator)) || l.MCP != nil {
		t.Fatalf("env %v", l.Env)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("shim directory left after the run", err)
	}
}

// TestShimEnvReachesMCP: a runtime that gives the Factory MCP server only the variables named in MCP.EnvVars
// (Codex) still gives it the shim PATH and FACTORY_SHIM_DIR, so git run by the adapter for the exit's facts passes
// the shim instead of being refused.
func TestShimEnvReachesMCP(t *testing.T) {
	snapshot, _ := json.Marshal(map[string]any{"rules": policies.HostUI().Spec.Rules})
	d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "done"}, MCP: true}
	r, _ := runDriver(t, Worker{Driver: d, ShimBinary: buildShim(t), MCPBinary: fakeMCP(t)}, false, stored(func(s *protocol.RunSpec) { s.ConfigSnapshot = snapshot }))
	if r.Outcome != "succeeded" {
		t.Fatalf("%+v", r)
	}
	l := d.Launches()[0]
	if l.MCP == nil {
		t.Fatal("no Factory MCP server")
	}
	for _, name := range []string{"PATH", shim.EnvDir} {
		if !slices.Contains(l.MCP.EnvVars, name) {
			t.Errorf("%s not passed to the MCP server: %v", name, l.MCP.EnvVars)
		}
	}
}

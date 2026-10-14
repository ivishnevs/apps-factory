package worker

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
	bin := filepath.Join(t.TempDir(), "factory-shim")
	if out, err := exec.Command("go", "build", "-o", bin, "appfactory/cmd/factory-shim").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return bin
}

// Scripted shell commands run through the per-run shim directory built from the captured rules.
func TestJetStreamCommandGuards(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
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
	r, events := runDriver(t, url, Worker{Driver: d, ShimBinary: bin}, false, stored(func(s *protocol.RunSpec) { s.ConfigSnapshot = snapshot }))
	if r.Status != "COMPLETED" {
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

package readiness

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// The 2026-11-07/08 failures of the new nodes are node faults; a task's own failures are not.
func TestClassify(t *testing.T) {
	cases := []struct {
		outcome, result string
		tokens          bool
		kind            string
	}{
		{"setup_failed", "worktree: git clone https://github.com/ivishnevs/gnomys: exit status 128: remote: Repository not found.", false, protocol.FaultRepoAccess},
		{"setup_failed", "project setup failed before runtime startup: fatal: could not read Username for 'https://github.com': terminal prompts disabled", false, protocol.FaultRepoAccess},
		{"setup_failed", "worktree: mkdir /srv/factory/worktrees/x: permission denied", false, protocol.FaultWorktree},
		{"failed", "Native CLI binary for linux-x64 not found", false, protocol.FaultRuntime},
		{"failed", "--dangerously-skip-permissions cannot be used with root/sudo privileges for security reasons", false, protocol.FaultRuntime},
		{"failed", "Sandbox required but unavailable: bubblewrap (bwrap) not installed, socat not installed", false, protocol.FaultSandbox},
		// Not the node's: a diverged worktree, an agent's failure quoting a node text, an unknown setup failure.
		{"setup_failed", "workspace_diverged: worktree: Repository not found", false, ""},
		{"failed", "Sandbox required but unavailable", true, ""},
		{"failed", "Repository not found", false, ""},
		{"setup_failed", "missing run credential", false, ""},
		{"succeeded", "Repository not found", false, ""},
	}
	for _, c := range cases {
		f := Classify(c.outcome, c.result, c.tokens)
		got := ""
		if f != nil {
			got = f.Kind
			if f.Fix == "" || f.Detail == "" {
				t.Errorf("%q: fault without detail or fix: %+v", c.result, f)
			}
		}
		if got != c.kind {
			t.Errorf("Classify(%s, %q, %v) = %q, want %q", c.outcome, c.result, c.tokens, got, c.kind)
		}
	}
}

func TestRequirementShortForm(t *testing.T) {
	var p tasks.Project
	if err := jsonUnmarshal(`{"requires":["godot 4.7.2","openspec",{"command":"node","version":"20","version_args":["-v"]}]}`, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Requires) != 3 || p.Requires[0].String() != "godot 4.7.2" || p.Requires[1].Command != "openspec" || p.Requires[2].VersionArgs[0] != "-v" {
		t.Fatalf("requires = %+v", p.Requires)
	}
	if err := tasks.ValidateRequirements(p.Requires); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]tasks.Requirement{{{Command: "/usr/bin/godot"}}, {{Command: "rm -rf"}}, {{Command: "godot", Version: "latest"}}, {{Command: "a"}, {Command: "a"}}, {{Command: "x", VersionArgs: []string{"$(id)"}}}} {
		if tasks.ValidateRequirements(bad) == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestCheck(t *testing.T) {
	run := func(ctx context.Context, name string, args ...string) (string, string, error) {
		switch name {
		case "env":
			if strings.Contains(strings.Join(args, " "), "private") {
				return "", "remote: Repository not found.\nfatal: repository 'https://github.com/o/private/' not found", errors.New("exit status 128")
			}
			return "abc\tHEAD", "", nil
		case "godot":
			return "4.6.1.stable.official.abc\n", "", nil
		case "openspec":
			return "0.9.0", "", nil
		}
		return "", "", errors.New("exec: \"" + name + "\": executable file not found in $PATH")
	}
	ok := Check(context.Background(), run, protocol.ProjectCheck{Repo: "https://github.com/o/public", Requires: []tasks.Requirement{{Command: "godot", Version: "4.6"}, {Command: "openspec"}}})
	if len(ok) != 0 {
		t.Fatalf("faults on a ready node: %+v", ok)
	}
	faults := Check(context.Background(), run, protocol.ProjectCheck{Repo: "https://github.com/o/private", Requires: []tasks.Requirement{{Command: "godot", Version: "4.7.2"}, {Command: "gh"}}})
	if len(faults) != 3 || faults[0].Kind != protocol.FaultRepoAccess || !strings.Contains(faults[0].Detail, "Repository not found") ||
		!strings.Contains(faults[1].Detail, "4.6.1 is older than the required 4.7.2") || !strings.Contains(faults[2].Detail, "gh not found") {
		t.Fatalf("faults = %+v", faults)
	}
	if CompareVersions("4.10", "4.9.9") != 1 || CompareVersions("20", "20.0.0") != 0 || CompareVersions("1.2", "1.10") != -1 {
		t.Fatal("CompareVersions")
	}
	if Key(protocol.ProjectCheck{Project: "p", Generation: 1}) == Key(protocol.ProjectCheck{Project: "p", Generation: 2}) {
		t.Fatal("Key ignores the generation")
	}
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

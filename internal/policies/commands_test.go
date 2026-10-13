package policies

import (
	"strings"
	"testing"
)

func TestMatchCommand(t *testing.T) {
	for _, c := range []struct {
		pattern string
		argv    []string
		want    bool
	}{
		{"cliclick", []string{"cliclick", "c:1,1"}, true},
		{"cliclick", []string{"/usr/local/bin/cliclick"}, true},
		{"factory", []string{"factory-shim"}, false},
		{"xcrun simctl io * screenshot*", []string{"xcrun", "simctl", "io", "booted", "screenshot", "/tmp/a b.png"}, true},
		{"xcrun simctl *", []string{"xcrun", "clang"}, false},
		{"git push *", []string{"git", "push", "origin", "main"}, true},
		{"git push *", []string{"git", "push"}, false},
		{"git pu?h", []string{"git", "push"}, true},
		{"cliclick", nil, false},
	} {
		if got := MatchCommand(c.pattern, c.argv); got != c.want {
			t.Errorf("%q %q = %v", c.pattern, c.argv, got)
		}
	}
}

func TestCommandDecisionAndGuarded(t *testing.T) {
	rules := []Rule{
		{Name: "git-ok", Type: "access", Commands: []string{"git *"}, Effect: "allow", Instruction: "ok"},
		{Name: "push", Type: "access", Commands: []string{"git push *"}, Effect: "approval", Instruction: "ask first"},
		{Name: "force", Type: "access", Commands: []string{"git push --force*"}, Effect: "deny", Instruction: "never force"},
		{Name: "ops", Type: "access", Operations: []string{"factory.*"}, Effect: "deny", Instruction: "x"},
	}
	for argv, want := range map[string]CommandDecision{
		"git status":              {Effect: "allow", Rule: "git-ok", Instruction: "ok"},
		"git push origin main":    {Effect: "approval", Rule: "push", Instruction: "ask first"},
		"git push --force origin": {Effect: "deny", Rule: "force", Instruction: "never force"},
		"make build":              {Effect: "allow"},
		"factory tasks list":      {Effect: "allow"},
	} {
		if got := Command(rules, strings.Fields(argv)); got != want {
			t.Errorf("%s: %+v", argv, got)
		}
	}
	if Command(rules, []string{"git", "push", "--force-with-lease"}).Effect != "deny" {
		t.Error("deny must win over approval")
	}
	if g := Guarded(rules); strings.Join(g, ",") != "git" {
		t.Fatal(g)
	}
	if g := Guarded(OwnerCLI().Spec.Rules); strings.Join(g, ",") != "factory" {
		t.Fatal(g)
	}
}

func TestCommandValidation(t *testing.T) {
	ok := builtin("p", Rule{Name: "r", Type: "access", Commands: []string{"xcrun simctl io * tap*"}, Effect: "approval", Instruction: "i"})
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]Rule{
		"no-ops-or-commands": {Name: "r", Type: "access", Effect: "deny", Instruction: "i"},
		"glob-name":          {Name: "r", Type: "access", Commands: []string{"osa*"}, Effect: "deny", Instruction: "i"},
		"path-name":          {Name: "r", Type: "access", Commands: []string{"/usr/bin/open"}, Effect: "deny", Instruction: "i"},
		"class":              {Name: "r", Type: "access", Commands: []string{"open [ab]"}, Effect: "deny", Instruction: "i"},
		"empty":              {Name: "r", Type: "access", Commands: []string{""}, Effect: "deny", Instruction: "i"},
		"padded":             {Name: "r", Type: "access", Commands: []string{" open"}, Effect: "deny", Instruction: "i"},
		"escalation":         {Name: "r", Type: "escalation", Target: "owner", Commands: []string{"open"}, Instruction: "i"},
		"reporting":          {Name: "r", Type: "reporting", Commands: []string{"open"}, Instruction: "i"},
	} {
		if Validate(builtin("p", r)) == nil {
			t.Errorf("%s accepted", name)
		}
	}
	for _, p := range Builtins() {
		if err := Validate(p); err != nil || !p.Spec.Mandatory {
			t.Fatal(p.Metadata.Name, err)
		}
	}
	d := Command(HostUI().Spec.Rules, []string{"osascript", "-l", "JavaScript", "-e", "x"})
	if d.Effect != "deny" || !strings.Contains(d.Instruction, "accessibility identifier") {
		t.Fatal(d)
	}
}

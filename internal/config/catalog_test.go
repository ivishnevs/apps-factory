package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put(t *testing.T, root, path, text string) {
	t.Helper()
	p := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	put(t, root, "prompts/agent.md", "BASE_PROMPT")
	for _, n := range []string{"first", "second"} {
		put(t, root, "skills/"+n+"/SKILL.md", "---\nname: "+n+"\ndescription: A sample skill\n---\nSECRET_SKILL_BODY\n")
	}
	put(t, root, "configs/skills.yaml", "skills:\n  first:\n    path: skills/first/SKILL.md\n    description: First skill\n    categories: [testing]\n  second:\n    path: skills/second/SKILL.md\n    description: Second skill\n    categories: [planning]\n")
	put(t, root, "configs/profiles/base.yaml", `apiVersion: factory/v1
kind: AgentProfile
metadata:
  name: base
spec:
  role: developer
  runtime:
    provider: codex
  prompt:
    file: prompts/agent.md
  execution:
    timeout: 1m
    max_concurrent_runs: 1
    max_retries: 3
  communication:
    can_create_tasks: true
  skills:
    include: [first]
  policies: [restricted]
  tools:
    deny: [factory.danger.*]
`)
	put(t, root, "configs/agents/agent.yaml", `apiVersion: factory/v1
kind: Agent
metadata:
  name: agent
spec:
  extends: base
  execution:
    max_retries: 0
  communication:
    can_create_tasks: false
  skills:
    include: [second]
  policies: []
  tools:
    mode: unrestricted
  approvals:
    mode: never
`)
	put(t, root, "configs/policies/restricted.yaml", `apiVersion: factory/v1
kind: Policy
metadata:
  name: restricted
spec:
  rules:
    - name: keep-denial
      type: access
      operations: [factory.release.publish]
      effect: deny
      instruction: Never publish.
    - name: owner-escalation
      type: escalation
      roles: [manager]
      target: owner
      instruction: Only for managers.
`)
	return root
}
func TestProfilePolicyContext(t *testing.T) {
	root := fixture(t)
	c, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	a := c.Agents["agent"]
	if a.Spec.Execution.MaxRetries != 0 || a.Spec.Communication.CanCreateTasks {
		t.Fatal("explicit scalar zero/false lost")
	}
	if strings.Join(a.AvailableSkills, ",") != "first,second" {
		t.Fatal("skills not extended", a.AvailableSkills)
	}
	if a.Authorize("factory.release.publish").Effect != "deny" || a.Authorize("external.purchase").Effect != "approval" || a.Authorize("factory.danger.delete").Effect != "deny" {
		t.Fatal("restrictions lost")
	}
	if a.Authorize("factory.skills.read").Effect != "allow" {
		t.Fatal("normal development blocked")
	}
	if strings.Contains(a.Instructions, "SECRET_SKILL_BODY") || strings.Contains(a.Instructions, "Only for managers") {
		t.Fatal("eager skill loading or wrong role policy")
	}
	if !strings.Contains(a.Instructions, "First skill") || !strings.Contains(a.Instructions, "Never publish") {
		t.Fatal("missing context")
	}
	a.Spec.Tools.Mode = "allowlist"
	a.Spec.Tools.Allow = []string{"factory.skills.read"}
	if a.Authorize("factory.task.create").Effect != "deny" {
		t.Fatal("allowlist bypassed")
	}
	a.Spec.Approvals.Mode = "always"
	if a.Authorize("factory.skills.read").Effect != "approval" {
		t.Fatal("always approvals ignored")
	}
}
func TestInvalidCatalog(t *testing.T) {
	cases := map[string]func(*testing.T, string){
		"missing-profile": func(t *testing.T, r string) {
			put(t, r, "configs/profiles/base.yaml", "apiVersion: factory/v1\nkind: AgentProfile\nmetadata:\n  name: base\nspec:\n  extends: missing\n")
		},
		"profile-cycle": func(t *testing.T, r string) {
			put(t, r, "configs/profiles/base.yaml", "apiVersion: factory/v1\nkind: AgentProfile\nmetadata:\n  name: base\nspec:\n  extends: base\n")
		},
		"unknown-skill": func(t *testing.T, r string) {
			p := filepath.Join(r, "configs/agents/agent.yaml")
			b, _ := os.ReadFile(p)
			put(t, r, "configs/agents/agent.yaml", strings.Replace(string(b), "[second]", "[missing]", 1))
		},
		"unknown-policy": func(t *testing.T, r string) {
			p := filepath.Join(r, "configs/agents/agent.yaml")
			b, _ := os.ReadFile(p)
			put(t, r, "configs/agents/agent.yaml", strings.Replace(string(b), "policies: []", "policies: [missing]", 1))
		},
		"null-deny": func(t *testing.T, r string) {
			p := filepath.Join(r, "configs/agents/agent.yaml")
			b, _ := os.ReadFile(p)
			put(t, r, "configs/agents/agent.yaml", strings.Replace(string(b), "mode: unrestricted", "mode: unrestricted\n    deny: null", 1))
		},
		"reserved-policy": func(t *testing.T, r string) {
			put(t, r, "configs/policies/system.yaml", "apiVersion: factory/v1\nkind: Policy\nmetadata:\n  name: system\nspec:\n  rules: []\n")
		},
		"bad-timeout": func(t *testing.T, r string) {
			p := filepath.Join(r, "configs/agents/agent.yaml")
			b, _ := os.ReadFile(p)
			put(t, r, "configs/agents/agent.yaml", strings.Replace(string(b), "max_retries: 0", "max_retries: 0\n    timeout: forever", 1))
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			root := fixture(t)
			change(t, root)
			if _, err := LoadCatalog(root); err == nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
}

func TestApprovalsInherit(t *testing.T) {
	root := fixture(t)
	p := filepath.Join(root, "configs/profiles/base.yaml")
	b, _ := os.ReadFile(p)
	put(t, root, "configs/profiles/base.yaml", string(b)+"  approvals:\n    mode: always\n")
	p = filepath.Join(root, "configs/agents/agent.yaml")
	b, _ = os.ReadFile(p)
	put(t, root, "configs/agents/agent.yaml", strings.Replace(string(b), "mode: never", "mode: inherit", 1))
	c, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Agents["agent"].Spec.Approvals.Mode != "always" {
		t.Fatal("inherit dropped parent approvals")
	}
}

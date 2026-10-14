package config

import (
	"encoding/json"
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
    quota_key: codex-team
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
	if a.Spec.Runtime.QuotaKey != "codex-team" {
		t.Fatal("quota_key not inherited", a.Spec.Runtime.QuotaKey)
	}
	// Core reads the key from the run's config snapshot at spec.runtime.quota_key.
	var snapshot struct {
		Spec struct {
			Runtime struct {
				QuotaKey string `json:"quota_key"`
			} `json:"runtime"`
		} `json:"spec"`
	}
	if b, err := json.Marshal(a); err != nil || json.Unmarshal(b, &snapshot) != nil || snapshot.Spec.Runtime.QuotaKey != "codex-team" {
		t.Fatal("quota_key snapshot path", err)
	}
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
		"invalid-quota-key": func(t *testing.T, r string) {
			p := filepath.Join(r, "configs/profiles/base.yaml")
			b, _ := os.ReadFile(p)
			put(t, r, "configs/profiles/base.yaml", strings.Replace(string(b), "quota_key: codex-team", "quota_key: Codex Team", 1))
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
		"bad-workspace-mode": func(t *testing.T, r string) {
			p := filepath.Join(r, "configs/agents/agent.yaml")
			b, _ := os.ReadFile(p)
			put(t, r, "configs/agents/agent.yaml", string(b)+"  workspace:\n    mode: task_branch\n")
		},
		"bad-profile-workspace-mode": func(t *testing.T, r string) {
			p := filepath.Join(r, "configs/profiles/base.yaml")
			b, _ := os.ReadFile(p)
			put(t, r, "configs/profiles/base.yaml", string(b)+"  workspace:\n    mode: fresh\n")
		},
		"zero-resource": func(t *testing.T, r string) {
			put(t, r, "configs/agents/agent.yaml", mustRead(t, r, "configs/agents/agent.yaml")+"  resources: {simulator: 0}\n")
		},
		"bad-resource-name": func(t *testing.T, r string) {
			put(t, r, "configs/profiles/base.yaml", mustRead(t, r, "configs/profiles/base.yaml")+"  resources: {Simulator: 1}\n")
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

func TestCommandRules(t *testing.T) {
	root := fixture(t)
	put(t, root, "configs/policies/restricted.yaml", `apiVersion: factory/v1
kind: Policy
metadata:
  name: restricted
spec:
  rules:
    - name: simulator-input
      type: access
      roles: [developer]
      commands: ["xcrun simctl io * tap*"]
      effect: approval
      instruction: Prefer UI tests.
    - name: click-ok
      type: access
      commands: [cliclick]
      effect: allow
      instruction: Allowed?
    - name: manager-only
      type: access
      roles: [manager]
      commands: [open]
      effect: deny
      instruction: Not for managers.
`)
	c, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	a := c.Agents["agent"]
	if strings.Join(a.Spec.Policies, ",") != "host-ui,owner-cli,restricted,system" {
		t.Fatal(a.Spec.Policies)
	}
	for argv, want := range map[string]string{"cliclick c:1,1": "deny", "factory tasks list": "deny", "xcrun simctl io booted tap 1 1": "approval", "xcrun simctl list": "allow", "open .": "allow"} {
		if d := a.AuthorizeCommand(strings.Fields(argv)); d.Effect != want {
			t.Errorf("%s: %+v", argv, d)
		}
	}
	if !strings.Contains(a.Instructions, "commands: cliclick, osascript") || !strings.Contains(a.Instructions, `{"name":"command","arguments":{"argv"`) {
		t.Fatal(a.Instructions)
	}
	for _, name := range []string{"host-ui", "owner-cli"} {
		root := fixture(t)
		put(t, root, "configs/policies/"+name+".yaml", "apiVersion: factory/v1\nkind: Policy\nmetadata:\n  name: "+name+"\nspec:\n  rules: []\n")
		if _, err := LoadCatalog(root); err == nil {
			t.Fatal(name, "replaced")
		}
	}
	root = fixture(t)
	put(t, root, "configs/policies/bad.yaml", "apiVersion: factory/v1\nkind: Policy\nmetadata:\n  name: bad\nspec:\n  rules:\n    - name: r\n      type: access\n      commands: ['osa*']\n      effect: deny\n      instruction: x\n")
	if _, err := LoadCatalog(root); err == nil {
		t.Fatal("glob command name accepted")
	}
}

func TestWorkspaceMode(t *testing.T) {
	root := fixture(t)
	c, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(c.Agents["agent"].Spec)
	if m := c.Agents["agent"].Spec.Workspace.Mode; m != "" || strings.Contains(string(b), "workspace") {
		t.Fatalf("default mode %q changed the snapshot: %s", m, b)
	}
	put(t, root, "configs/profiles/base.yaml", strings.Replace(mustRead(t, root, "configs/profiles/base.yaml"), "spec:\n", "spec:\n  workspace:\n    mode: worktree\n", 1))
	if c, err = LoadCatalog(root); err != nil || c.Agents["agent"].Spec.Workspace.Mode != "worktree" {
		t.Fatalf("inherited mode: %v %+v", err, c)
	}
	put(t, root, "configs/agents/agent.yaml", mustRead(t, root, "configs/agents/agent.yaml")+"  workspace:\n    mode: shared\n")
	if c, err = LoadCatalog(root); err != nil || c.Agents["agent"].Spec.Workspace.Mode != "shared" {
		t.Fatalf("overridden mode: %v", err)
	}
	put(t, root, "configs/agents/agent.yaml", strings.Replace(mustRead(t, root, "configs/agents/agent.yaml"), "mode: shared", "mode: scratch", 1))
	if c, err = LoadCatalog(root); err != nil || c.Agents["agent"].Spec.Workspace.Mode != "scratch" {
		t.Fatalf("scratch mode: %v", err)
	}
}

func TestResources(t *testing.T) {
	root := fixture(t)
	c, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(c.Agents["agent"].Spec); strings.Contains(string(b), "resources") {
		t.Fatalf("no resources changed the snapshot: %s", b)
	}
	put(t, root, "configs/profiles/base.yaml", mustRead(t, root, "configs/profiles/base.yaml")+"  resources: {simulator: 1, build: 1}\n")
	put(t, root, "configs/agents/agent.yaml", mustRead(t, root, "configs/agents/agent.yaml")+"  resources: {build: 2}\n")
	if c, err = LoadCatalog(root); err != nil {
		t.Fatal(err)
	}
	if r := c.Agents["agent"].Spec.Resources; len(r) != 2 || r["simulator"] != 1 || r["build"] != 2 {
		t.Fatalf("merged resources %v", r)
	}
}

func mustRead(t *testing.T, root, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

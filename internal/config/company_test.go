package config

import (
	"strings"
	"testing"
)

// TestCatalogPerCompany (postgres-tenancy 2.4): configs/ is the default company's catalog and
// configs/<company>/ another company's; an agent of the same name resolves independently in each, and a
// company without a catalog, or named like a catalog directory, is refused.
func TestCatalogPerCompany(t *testing.T) {
	root := fixture(t)
	put(t, root, "configs/acme/profiles/lead.yaml", `apiVersion: factory/v1
kind: AgentProfile
metadata:
  name: lead
spec:
  role: manager
  runtime:
    provider: claude
  prompt:
    file: prompts/agent.md
  execution:
    timeout: 2m
    max_concurrent_runs: 2
`)
	put(t, root, "configs/acme/agents/agent.yaml", `apiVersion: factory/v1
kind: Agent
metadata:
  name: agent
spec:
  extends: lead
`)
	put(t, root, "configs/acme/policies/acme-only.yaml", `apiVersion: factory/v1
kind: Policy
metadata:
  name: acme-only
spec:
  rules:
    - name: no-release
      type: access
      operations: [factory.release.publish]
      effect: deny
      instruction: Acme never publishes.
`)
	def, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	acme, err := LoadCompanyCatalog(root, "acme")
	if err != nil {
		t.Fatal(err)
	}
	d, a := def.Agents["agent"], acme.Agents["agent"]
	if d.Spec.Role != "developer" || d.Spec.Runtime.Provider != "codex" || a.Spec.Role != "manager" || a.Spec.Runtime.Provider != "claude" {
		t.Fatalf("default %s/%s, acme %s/%s", d.Spec.Role, d.Spec.Runtime.Provider, a.Spec.Role, a.Spec.Runtime.Provider)
	}
	if _, ok := acme.Profiles["base"]; ok {
		t.Fatal("acme sees the default company's profile")
	}
	if _, ok := def.Policies["acme-only"]; ok {
		t.Fatal("the default company sees acme's policy")
	}
	if _, ok := acme.Policies["acme-only"]; !ok || len(acme.Skills.Skills) != 0 {
		t.Fatal("acme policy missing, or acme without skills.yaml got the default company's skills", acme.Skills.Skills)
	}
	if same, err := LoadCompanyCatalog(root, DefaultCompany); err != nil || same.Agents["agent"].Spec.Role != "developer" {
		t.Fatal(err)
	}
	for company, want := range map[string]string{"globex": "no agents in", "agents": "invalid company", "a.b": "invalid company"} {
		if _, err = LoadCompanyCatalog(root, company); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", company, err)
		}
	}
}

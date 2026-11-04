package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// acmeAgent is company acme's agent including skills.
func acmeAgent(t *testing.T, root string, include string) {
	t.Helper()
	put(t, root, "configs/acme/teams/main.yaml", "apiVersion: factory/v1\nkind: Team\nmetadata:\n  name: main\nspec:\n  lead: agent\n")
	put(t, root, "configs/acme/agents/agent.yaml", `apiVersion: factory/v1
kind: Agent
metadata:
  name: agent
spec:
  team: main
  role: developer
  runtime:
    provider: codex
  prompt:
    file: prompts/agent.md
  execution:
    timeout: 1m
    max_concurrent_runs: 1
  skills:
    include: [`+include+`]
`)
}

// TestSkillsPerCompany (skill-registry D5, D9): each company's skills.yaml lists its installed skills by source and
// hash (built-in ones resolve from the release); companies do not see each other's skills; a skill of another source
// needs its hash; old path: entries are refused; a scoped skill is refused to agents outside its scope and not
// discovered by them.
func TestSkillsPerCompany(t *testing.T) {
	root := fixture(t)
	hash := "sha256:" + strings.Repeat("ab", 32)
	put(t, root, "configs/acme/skills.yaml", "skills:\n  brand:\n    source: acme-skills\n    hash: "+hash+"\n    description: Brand voice\n    categories: [acme]\n  first:\n    source: factory\n")
	acmeAgent(t, root, "brand, first")

	def, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	acme, err := LoadCompanyCatalog(root, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(acme.Agents["agent"].AvailableSkills, ","); got != "brand,first" {
		t.Fatal("acme agent skills", got)
	}
	if _, ok := def.Skills.Skills["brand"]; ok || len(def.Skills.Skills) != 2 {
		t.Fatal("the default company sees acme's skill", def.Skills.Skills)
	}
	if pins := acme.Skills.Pins([]string{"brand", "first"}); pins["brand"] != hash || pins["first"] != def.Skills.Skills["first"].Hash || pins["first"] == "" {
		t.Fatal(pins)
	}

	for name, yaml := range map[string]string{
		"path entry":      "skills:\n  brand:\n    path: skills/brand/SKILL.md\n    description: x\n",
		"no hash":         "skills:\n  brand:\n    source: acme-skills\n",
		"no source":       "skills:\n  brand:\n    hash: " + hash + "\n",
		"bad scope":       "skills:\n  brand:\n    source: acme-skills\n    hash: " + hash + "\n    scope: everyone\n",
		"unknown builtin": "skills:\n  nope:\n    source: factory\n",
	} {
		put(t, root, "configs/acme/skills.yaml", yaml)
		acmeAgent(t, root, "")
		if _, err = LoadCompanyCatalog(root, "acme"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	// Scope: a team-scoped skill is refused to another team's agent and not discovered by it.
	put(t, root, "configs/acme/skills.yaml", "skills:\n  brand:\n    source: acme-skills\n    hash: "+hash+"\n    categories: [acme]\n    scope: team:other\n")
	acmeAgent(t, root, "brand")
	if _, err = LoadCompanyCatalog(root, "acme"); err == nil || !strings.Contains(err.Error(), "scoped to team:other") {
		t.Fatal(err)
	}
	put(t, root, "configs/acme/skills.yaml", "skills:\n  brand:\n    source: acme-skills\n    hash: "+hash+"\n    categories: [acme]\n    scope: team:main\n")
	if acme, err = LoadCompanyCatalog(root, "acme"); err != nil || strings.Join(acme.Agents["agent"].AvailableSkills, ",") != "brand" {
		t.Fatal(err)
	}

	// No skills.yaml: the company has no skills (no fallback to configs/skills.yaml).
	if err = os.Remove(filepath.Join(root, "configs/acme/skills.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadCompanyCatalog(root, "acme"); err == nil || !strings.Contains(err.Error(), "unknown skill brand") {
		t.Fatal(err)
	}
	acmeAgent(t, root, "")
	if acme, err = LoadCompanyCatalog(root, "acme"); err != nil || len(acme.Skills.Skills) != 0 {
		t.Fatal(err)
	}
}

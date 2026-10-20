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
	put(t, root, "configs/acme/agents/agent.yaml", `apiVersion: factory/v1
kind: Agent
metadata:
  name: agent
spec:
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

// TestSkillsPerCompany: each company's skills registry is its catalog's skills.yaml (configs/<company>/skills.yaml,
// configs/skills.yaml for the default company), with SKILL.md paths relative to that directory or absolute (outside
// the factory root); companies do not see each other's skills and there is no fallback to configs/skills.yaml.
func TestSkillsPerCompany(t *testing.T) {
	root := fixture(t)
	outside := t.TempDir() // e.g. a skills checkout outside the factory root
	put(t, outside, "external/SKILL.md", "---\nname: external\ndescription: External skill\n---\nEXTERNAL\n")
	put(t, outside, "external/references/guide.md", "GUIDE")
	put(t, root, "configs/acme/skills/brand/SKILL.md", "---\nname: brand\ndescription: Brand skill\n---\nBRAND\n")
	put(t, root, "configs/acme/skills.yaml", "skills:\n  brand:\n    path: skills/brand/SKILL.md\n    description: Brand voice\n    categories: [acme]\n  external:\n    path: "+filepath.Join(outside, "external", "SKILL.md")+"\n    description: External\n    categories: [acme]\n")
	acmeAgent(t, root, "brand, external")

	def, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	acme, err := LoadCompanyCatalog(root, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(acme.Agents["agent"].AvailableSkills, ","); got != "brand,external" {
		t.Fatal("acme agent skills", got)
	}
	if _, ok := acme.Skills.Skills["first"]; ok {
		t.Fatal("acme sees the default company's skill")
	}
	if _, ok := def.Skills.Skills["brand"]; ok || len(def.Skills.Skills) != 2 {
		t.Fatal("the default company sees acme's skill", def.Skills.Skills)
	}
	if body, err := acme.Skills.Read("brand", ""); err != nil || !strings.Contains(body, "BRAND") {
		t.Fatal(body, err)
	}
	if body, err := acme.Skills.Read("external", "references/guide.md"); err != nil || body != "GUIDE" {
		t.Fatal(body, err)
	}
	if dir, err := acme.Skills.Dir("external"); err != nil || dir != filepath.Join(outside, "external") {
		t.Fatal(dir, err)
	}
	if pins, err := acme.Skills.Pins([]string{"brand", "external"}); err != nil || len(pins) != 2 {
		t.Fatal(pins, err)
	}

	// A skill of another company is unknown here.
	acmeAgent(t, root, "first")
	if _, err = LoadCompanyCatalog(root, "acme"); err == nil || !strings.Contains(err.Error(), "unknown skill first") {
		t.Fatal(err)
	}
	// A registered skill whose SKILL.md is missing fails loading.
	acmeAgent(t, root, "brand")
	if err = os.Remove(filepath.Join(root, "configs/acme/skills/brand/SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadCompanyCatalog(root, "acme"); err == nil || !strings.Contains(err.Error(), "skill brand") {
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

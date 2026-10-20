package runner

import (
	"os"
	"path/filepath"
	"testing"

	"appfactory/internal/node"
	"appfactory/internal/skills"
)

// TestLoadSkillsPerCompany: a node stages skills from its company's catalog under skills_root
// (configs/skills.yaml for the default company, configs/<company>/skills.yaml for another), so the copies it
// materializes hash like the ones Core pinned from the same catalog; a company without skills.yaml has none.
func TestLoadSkillsPerCompany(t *testing.T) {
	root := t.TempDir()
	for p, data := range map[string]string{
		"configs/skills.yaml":                    "skills:\n  openspec:\n    path: ../skills/openspec/SKILL.md\n    description: Specs.\n    categories: [planning]\n",
		"skills/openspec/SKILL.md":               "---\nname: openspec\ndescription: Specs.\n---\nbody\n",
		"configs/acme/skills.yaml":               "skills:\n  brand:\n    path: skills/brand/SKILL.md\n    description: Brand.\n    categories: [acme]\n",
		"configs/acme/skills/brand/SKILL.md":     "---\nname: brand\ndescription: Brand.\n---\nbody\n",
		"configs/globex/agents/placeholder.txt":  "",
		"configs/acme/skills/brand/references/x": "x\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	def, err := LoadSkills(node.Config{SkillsRoot: root})
	if err != nil || def == nil || len(def.Skills) != 1 || def.Skills["openspec"].Path == "" {
		t.Fatal(def, err)
	}
	acme, err := LoadSkills(node.Config{SkillsRoot: root, Company: "acme"})
	if err != nil || acme == nil || len(acme.Skills) != 1 {
		t.Fatal(acme, err)
	}
	dir, err := acme.Dir("brand")
	if err != nil || dir != filepath.Join(root, "configs/acme/skills/brand") {
		t.Fatal(dir, err)
	}
	core, err := skills.Load(filepath.Join(root, "configs", "acme")) // Core's registry of the same catalog
	if err != nil {
		t.Fatal(err)
	}
	if a, b := mustPins(t, acme), mustPins(t, core); a != b {
		t.Fatal("node and Core hash the company's skill differently", a, b)
	}
	if none, err := LoadSkills(node.Config{SkillsRoot: root, Company: "globex"}); err != nil || none != nil {
		t.Fatal("company without skills.yaml", none, err)
	}
}

func mustPins(t *testing.T, r *skills.Registry) string {
	t.Helper()
	p, err := r.Pins([]string{"brand"})
	if err != nil {
		t.Fatal(err)
	}
	return p["brand"]
}

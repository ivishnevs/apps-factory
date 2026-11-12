package support

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/skills"
)

// TestRunbooksShip (setup-assistant 2.1): the built-in factory-setup skill indexes like every release skill, has a
// runbook for each name the answers use, lists each in SKILL.md, and every runbook ends with its proving check.
func TestRunbooksShip(t *testing.T) {
	dir := filepath.Join("..", "..", "skills", Skill)
	idx, err := skills.Index(dir)
	if err != nil || len(idx) != 1 || idx[0].Error != "" || idx[0].Name != Skill {
		t.Fatalf("index %+v %v", idx, err)
	}
	index, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range Runbooks {
		b, err := os.ReadFile(filepath.Join(dir, "references", name+".md"))
		if err != nil {
			t.Fatalf("runbook %s: %v", name, err)
		}
		text := string(b)
		i := strings.LastIndex(text, "\n## Proof\n")
		if i < 0 || strings.Contains(text[i+1:], "\n## ") || len(strings.TrimSpace(text[i+len("\n## Proof\n"):])) == 0 {
			t.Errorf("runbook %s does not end with its proof", name)
		}
		if !strings.Contains(string(index), "`"+name+"`") {
			t.Errorf("runbook %s not listed in SKILL.md", name)
		}
	}
}

package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScopedResourceAndDiscovery(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "skills/example")
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0700); err != nil {
		t.Fatal(err)
	}
	for p, data := range map[string]string{"SKILL.md": "skill body", "references/guide.md": "reference"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := &Registry{Root: root, Skills: map[string]Skill{"example": {Path: "skills/example/SKILL.md", Description: "Example", Categories: []string{"testing"}}}}
	if len(r.Available(nil, false, []string{"testing"})) != 0 || len(r.Available(nil, true, []string{"testing"})) != 1 {
		t.Fatal("discovery filtering failed")
	}
	content, err := r.Read("example", "references/guide.md")
	if err != nil || content != "reference" {
		t.Fatal(content, err)
	}
	outside := filepath.Join(root, "private")
	_ = os.WriteFile(outside, []byte("secret"), 0600)
	if err = os.Symlink(outside, filepath.Join(dir, "references/escape")); err != nil {
		t.Fatal(err)
	}
	for _, resource := range []string{"../../private", "/etc/passwd", "references/escape"} {
		if _, err = r.Read("example", resource); err == nil {
			t.Fatal("escape accepted", resource)
		}
	}
}

package skills

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, dir, name, desc string, extra map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: " + name + "\ndescription: " + desc + "\nmetadata:\n  categories: Git, review\n---\nBody of " + name + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	for p, c := range extra {
		f := filepath.Join(dir, filepath.FromSlash(p))
		_ = os.MkdirAll(filepath.Dir(f), 0o755)
		if err := os.WriteFile(f, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestPackUnpack (skill-registry 3.1): the tar is deterministic, unpacks to the same Hash, and refuses entries
// leaving the skill.
func TestPackUnpack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "review")
	writeSkill(t, dir, "review", "Review code", map[string]string{"references/checklist.md": "check", "scripts/run.sh": "echo"})
	a, hash, files, err := Pack(dir)
	if err != nil || len(files) != 3 {
		t.Fatal(files, err)
	}
	b, hash2, _, _ := Pack(dir)
	if !bytes.Equal(a, b) || hash != hash2 {
		t.Fatal("pack is not deterministic")
	}
	got, err := Unpack(bytes.NewReader(a), filepath.Join(t.TempDir(), "out"))
	if err != nil || got != hash {
		t.Fatal(got, hash, err)
	}
	if body, err := ReadFile(bytes.NewReader(a), "references/checklist.md"); err != nil || string(body) != "check" {
		t.Fatal(string(body), err)
	}
}

// TestDiscoverAndIndex (skill-registry 4.5): every layout is found; an invalid skill gets its error and does not
// stop the others; the index of the same tree is the same.
func TestDiscoverAndIndex(t *testing.T) {
	layouts := map[string]func(root string){
		"skills dir": func(root string) {
			writeSkill(t, filepath.Join(root, "skills", "alpha"), "alpha", "A", nil)
			writeSkill(t, filepath.Join(root, "skills", "beta"), "wrong-name", "B", nil)
		},
		"agents and claude": func(root string) {
			writeSkill(t, filepath.Join(root, ".agents", "skills", "alpha"), "alpha", "A", nil)
			_ = os.MkdirAll(filepath.Join(root, ".claude", "skills"), 0o755)
			_ = os.Symlink("../../.agents/skills/alpha", filepath.Join(root, ".claude", "skills", "alpha"))
			writeSkill(t, filepath.Join(root, ".claude", "skills", "beta"), "beta", "B", nil)
		},
		"marketplace": func(root string) {
			_ = os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755)
			_ = os.WriteFile(filepath.Join(root, ".claude-plugin", "marketplace.json"), []byte(`{"plugins":[{"source":"./plugins/p1"},{"source":"./plugins/p2","skills":["tools/beta"]}]}`), 0o644)
			writeSkill(t, filepath.Join(root, "plugins", "p1", "skills", "alpha"), "alpha", "A", nil)
			writeSkill(t, filepath.Join(root, "plugins", "p2", "tools", "beta"), "beta", "B", nil)
		},
		"single": func(root string) { writeSkill(t, root, "alpha", "A", nil) },
	}
	for name, make := range layouts {
		root := filepath.Join(t.TempDir(), "alpha")
		make(root)
		idx, err := Index(root)
		if err != nil {
			t.Fatal(name, err)
		}
		var ok, bad []string
		for _, x := range idx {
			if x.Error == "" {
				ok = append(ok, x.Name)
			} else {
				bad = append(bad, x.Name+": "+x.Error)
			}
		}
		switch name {
		case "skills dir":
			if len(ok) != 1 || ok[0] != "alpha" || len(bad) != 1 || !strings.Contains(bad[0], "differs from its directory") {
				t.Fatal(name, ok, bad)
			}
		case "single":
			if len(ok) != 1 || ok[0] != "alpha" {
				t.Fatal(name, ok, bad)
			}
		default:
			if strings.Join(ok, ",") != "alpha,beta" || len(bad) != 0 {
				t.Fatal(name, ok, bad)
			}
		}
		again, _ := Index(root)
		for i := range idx {
			if idx[i].Hash != again[i].Hash || !bytes.Equal(idx[i].Tar, again[i].Tar) {
				t.Fatal(name, "index differs for the same tree")
			}
		}
		if name != "skills dir" && len(idx[0].Meta.Categories()) != 2 {
			t.Fatal(idx[0].Meta.Categories())
		}
	}
}

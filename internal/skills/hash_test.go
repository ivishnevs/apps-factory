package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHashAndCopy(t *testing.T) {
	tmp, _ := filepath.EvalSymlinks(t.TempDir())
	src := filepath.Join(tmp, "example")
	for p, data := range map[string]string{"SKILL.md": "body", "references/guide.md": "guide"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(src, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, p), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h, err := Hash(src)
	if err != nil || !strings.HasPrefix(h, HashPrefix) || len(h) != len(HashPrefix)+64 {
		t.Fatalf("hash %q %v", h, err)
	}
	dst := filepath.Join(t.TempDir(), "copy")
	if c, err := Copy(src, dst); err != nil || c != h {
		t.Fatalf("copy hash %q, want %q (%v)", c, h, err)
	}
	if _, err = Copy(src, dst); err == nil {
		t.Fatal("copy over an existing target")
	}
	// Content and file names both count.
	_ = os.WriteFile(filepath.Join(src, "references/guide.md"), []byte("guidE"), 0o644)
	if h2, _ := Hash(src); h2 == h {
		t.Fatal("content change kept the hash")
	}
	_ = os.WriteFile(filepath.Join(src, "references/guide.md"), []byte("guide"), 0o644)
	_ = os.Rename(filepath.Join(src, "references/guide.md"), filepath.Join(src, "references/other.md"))
	if h3, _ := Hash(src); h3 == h {
		t.Fatal("rename kept the hash")
	}
	if err = os.Symlink("/etc/passwd", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err = Hash(src); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err = Hash(t.TempDir()); err == nil {
		t.Fatal("directory without SKILL.md accepted")
	}
	r := &Registry{Root: filepath.Dir(src), Skills: map[string]Skill{"example": {Path: "example/SKILL.md"}}}
	if d, err := r.Dir("example"); err != nil || d != src {
		t.Fatalf("dir %q %v", d, err)
	}
	if _, err = r.Dir("missing"); err == nil {
		t.Fatal("unknown skill")
	}
}

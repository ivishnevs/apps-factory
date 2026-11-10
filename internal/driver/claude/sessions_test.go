package claude

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHasSession (session-continuity 2.1): a session is there when its transcript is in any project directory of
// the node's configuration; a configuration without projects knows it is not.
func TestHasSession(t *testing.T) {
	dir := t.TempDir()
	d := Driver{ConfigDir: dir}
	if found, known := d.HasSession("abc"); found || known {
		t.Fatal(found, known)
	}
	p := filepath.Join(dir, "projects", "-Users-x-app", "abc.jsonl")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte("{}\n"), 0o600)
	if found, known := d.HasSession("abc"); !found || !known {
		t.Fatal("transcript not found")
	}
	if found, known := d.HasSession("other"); found || !known {
		t.Fatal(found, known)
	}
}

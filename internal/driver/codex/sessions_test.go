package codex

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHasSession (session-continuity 2.1): a Codex thread is there when its rollout is under sessions/ or
// archived_sessions/ of the node's CODEX_HOME.
func TestHasSession(t *testing.T) {
	home := t.TempDir()
	d := Driver{Home: home}
	p := filepath.Join(home, "sessions", "2026", "11", "10", "rollout-2026-11-10T09-00-00-thread-1.jsonl")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte("{}\n"), 0o600)
	if found, known := d.HasSession("thread-1"); !found || !known {
		t.Fatal("rollout not found")
	}
	if found, _ := d.HasSession("thread-2"); found {
		t.Fatal("unknown thread found")
	}
}

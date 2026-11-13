package node

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestRuntimeCaches (node-runtime-caches): Claude Code's temp directory is measured whole and in the part of the
// node's runs (session directories named after a working directory under the state dir or projects root); the
// runtimes' data directories only where the node has its own login.
func TestRuntimeCaches(t *testing.T) {
	tmp := t.TempDir()
	env := func(k string) string { return map[string]string{"CLAUDE_CODE_TMPDIR": tmp}[k] }
	dir := filepath.Join(tmp, "claude-"+strconv.Itoa(os.Getuid()))
	if got := ClaudeTmpDir(env); got != dir {
		t.Fatalf("ClaudeTmpDir = %q, want %q", got, dir)
	}
	write := func(path string, n int) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	state, projects := "/srv/node.a/state", "/srv/projects"
	write(filepath.Join(dir, "-srv-node-a-state-worktrees-co-p-tasks-t1", "s1", "tasks", "out"), 100)
	write(filepath.Join(dir, "-srv-projects-game", "s2", "out"), 20)
	write(filepath.Join(dir, "-home-me-own-repo", "s3", "out"), 7) // the person's own session
	write(filepath.Join(dir, "tmpXYZ", "build.o"), 3)              // a sandboxed command's TMPDIR
	codex := filepath.Join(tmp, "codex-home")
	write(filepath.Join(codex, "sessions", "a.jsonl"), 50)

	c := Config{StateDir: state, ProjectsRoot: projects}
	c.Drivers.Codex.Home = codex
	got := RuntimeCaches(c, env)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	if got[0].Name != "claude_tmp" || got[0].Path != dir || got[0].Bytes != 130 || got[0].AgentBytes == nil || *got[0].AgentBytes != 120 {
		t.Fatalf("claude_tmp %+v", got[0])
	}
	if got[1].Name != "codex_data" || got[1].Bytes != 50 || got[1].AgentBytes != nil {
		t.Fatalf("codex_data %+v", got[1])
	}
	// Nothing there yet: no entries (claude_data without config_dir is the person's own ~/.claude).
	if got := RuntimeCaches(Config{StateDir: state}, func(string) string { return t.TempDir() }); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

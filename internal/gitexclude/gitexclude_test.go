package gitexclude

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func status(t *testing.T, dir string) string {
	return git(t, dir, "status", "--porcelain", "--untracked-files=all")
}

// repo: a main checkout with one commit and an isolated git config whose global excludes file ignores *.log.
func repo(t *testing.T) (main, globalIgnore string) {
	t.Helper()
	base := t.TempDir()
	globalIgnore = filepath.Join(base, "global-ignore")
	write(t, globalIgnore, "*.log")
	gc := filepath.Join(base, "gitconfig")
	write(t, gc, "[user]\n\tname = Test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n[core]\n\texcludesFile = "+globalIgnore+"\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	main = filepath.Join(base, "main")
	git(t, base, "init", "-q", main)
	write(t, filepath.Join(main, "README"), "x")
	git(t, main, "add", "-A")
	git(t, main, "commit", "-qm", "init")
	return main, globalIgnore
}

// TestAddOnlyThisWorktree: rules added in a factory worktree hide its materialized files from status and
// `git add -A`, while the main checkout and a sibling worktree of the same repository still see their own
// openspec/ and .factory/ files; the user's global excludes keep working in the factory worktree.
func TestAddOnlyThisWorktree(t *testing.T) {
	main, _ := repo(t)
	ctx := context.Background()
	wt := filepath.Join(filepath.Dir(main), "wt")
	sib := filepath.Join(filepath.Dir(main), "sibling")
	git(t, main, "worktree", "add", "-q", "-b", "factory", wt)
	git(t, main, "worktree", "add", "-q", "-b", "other", sib)
	for i := 0; i < 2; i++ { // idempotent
		if err := Add(ctx, wt, "/openspec/", "/.factory/"); err != nil {
			t.Fatal(err)
		}
	}
	for _, d := range []string{main, wt, sib} {
		write(t, filepath.Join(d, "openspec/changes/x/proposal.md"), "p")
		write(t, filepath.Join(d, ".factory/state"), "s")
		write(t, filepath.Join(d, "debug.log"), "l")
	}
	if s := status(t, wt); s != "" {
		t.Fatalf("factory worktree not clean: %q", s)
	}
	git(t, wt, "add", "-A")
	if s := git(t, wt, "diff", "--cached", "--name-only"); s != "" {
		t.Fatalf("git add -A staged %q", s)
	}
	for _, d := range []string{main, sib} {
		s := status(t, d)
		if !strings.Contains(s, "openspec/changes/x/proposal.md") || !strings.Contains(s, ".factory/state") || strings.Contains(s, "debug.log") {
			t.Fatalf("%s affected: %q", d, s)
		}
	}
	gitDir := git(t, wt, "rev-parse", "--path-format=absolute", "--git-dir")
	if got := git(t, wt, "config", "--worktree", "core.excludesFile"); got != filepath.Join(gitDir, FileName) {
		t.Fatalf("core.excludesFile = %q", got)
	}
	b, _ := os.ReadFile(filepath.Join(gitDir, FileName))
	if strings.Count(string(b), "/openspec/") != 1 || !strings.Contains(string(b), "*.log") {
		t.Fatalf("excludes file %q", b)
	}
}

// TestAddRemovesLegacyLines: the lines earlier versions wrote to the shared info/exclude are removed,
// the user's own lines stay, and the main checkout sees its new openspec/ files again.
func TestAddRemovesLegacyLines(t *testing.T) {
	main, _ := repo(t)
	exclude := filepath.Join(main, ".git", "info", "exclude")
	write(t, exclude, "# comment\n*.tmp\n/openspec/\n/.factory/\n/.claude/skills/openspec/\n/.claude/skills/own/\n")
	wt := filepath.Join(filepath.Dir(main), "wt")
	git(t, main, "worktree", "add", "-q", "-b", "factory", wt)
	if err := Add(context.Background(), wt, "/openspec/", "/.factory/", "/.claude/skills/openspec/"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(exclude)
	if string(b) != "# comment\n*.tmp\n/.claude/skills/own/\n" {
		t.Fatalf("info/exclude = %q", b)
	}
	write(t, filepath.Join(main, "openspec/specs/a/spec.md"), "s")
	if s := status(t, main); !strings.Contains(s, "openspec/specs/a/spec.md") {
		t.Fatalf("main checkout: %q", s)
	}
}

// TestAddPlainClone: in a plain clone (the main worktree itself) the rules apply to that checkout, and
// keep applying after more patterns are added.
func TestAddPlainClone(t *testing.T) {
	main, _ := repo(t)
	ctx := context.Background()
	if err := Add(ctx, main, "/openspec/", "/.factory/"); err != nil {
		t.Fatal(err)
	}
	if err := Add(ctx, filepath.Join(main), "/.claude/skills/x/"); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(main, "openspec/a.md"), "a")
	write(t, filepath.Join(main, ".factory/state"), "s")
	write(t, filepath.Join(main, ".claude/skills/x/SKILL.md"), "s")
	write(t, filepath.Join(main, "debug.log"), "l")
	if s := status(t, main); s != "" {
		t.Fatalf("status %q", s)
	}
	if b, _ := os.ReadFile(filepath.Join(main, ".git", "info", "exclude")); strings.Contains(string(b), "openspec") {
		t.Fatalf("info/exclude %q", b)
	}
}

// TestAddBareWithWorktrees: enabling worktreeConfig in a bare repository moves core.bare to the main
// config.worktree, so linked worktrees stay non-bare.
func TestAddBareWithWorktrees(t *testing.T) {
	main, _ := repo(t)
	bare := filepath.Join(filepath.Dir(main), "bare.git")
	git(t, filepath.Dir(main), "clone", "-q", "--bare", main, bare)
	wt := filepath.Join(filepath.Dir(main), "wt")
	git(t, bare, "worktree", "add", "-q", "-b", "factory", wt)
	if err := Add(context.Background(), wt, "/.factory/"); err != nil {
		t.Fatal(err)
	}
	if got := git(t, wt, "rev-parse", "--is-bare-repository"); got != "false" {
		t.Fatalf("worktree bare = %s", got)
	}
	if got := git(t, bare, "rev-parse", "--is-bare-repository"); got != "true" {
		t.Fatalf("repository bare = %s", got)
	}
	write(t, filepath.Join(wt, ".factory/state"), "s")
	if s := status(t, wt); s != "" {
		t.Fatalf("status %q", s)
	}
}

// TestAddOutsideRepository does nothing.
func TestAddOutsideRepository(t *testing.T) {
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	if err := Add(context.Background(), t.TempDir(), "/.factory/"); err != nil {
		t.Fatal(err)
	}
}

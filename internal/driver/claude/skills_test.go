package claude

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/driver"
	"appfactory/internal/driver/claude/claudetest"
	"appfactory/internal/skills"
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

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// skillsFixture: a git workspace with its own project skill and a stale copy of a crashed run, and a
// staged skills root with the run's verified skill.
func skillsFixture(t *testing.T) (cwd, root string, list []driver.Skill) {
	t.Helper()
	gc := filepath.Join(t.TempDir(), "gitconfig")
	writeFile(t, gc, "[user]\n\tname = Test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	cwd = t.TempDir()
	git(t, cwd, "init", "-q")
	writeFile(t, filepath.Join(cwd, ".claude/skills/own/SKILL.md"), "---\nname: own\ndescription: Project skill.\n---\n")
	git(t, cwd, "add", "-A")
	git(t, cwd, "commit", "-qm", "init")
	writeFile(t, filepath.Join(cwd, ".claude/skills/stale/SKILL.md"), "old")
	writeFile(t, filepath.Join(cwd, ".claude/skills/stale", SkillMarker), "sha256:old\n")
	root = t.TempDir()
	writeFile(t, filepath.Join(root, "openspec/SKILL.md"), "---\nname: openspec\ndescription: Specs.\n---\nbody\n")
	writeFile(t, filepath.Join(root, "openspec/references/conventions.md"), "conventions\n")
	hash, err := skills.Hash(filepath.Join(root, "openspec"))
	if err != nil {
		t.Fatal(err)
	}
	return cwd, root, []driver.Skill{{Name: "openspec", Hash: hash}}
}

// TestSkillsMaterialized: the run's skill is copied into the project skills directory for the run only,
// hidden from git, a crashed run's copy is replaced and the project's own skill is untouched.
func TestSkillsMaterialized(t *testing.T) {
	cwd, root, list := skillsFixture(t)
	d, _ := fakeDriver(t, "start", claudetest.Dir())
	l, _ := scenarioLaunch(t, "start")
	l.Cwd, l.SkillsRoot, l.Skills = cwd, root, list
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := d.Launch(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	copyDir := filepath.Join(cwd, ".claude/skills/openspec")
	if b, err := os.ReadFile(filepath.Join(copyDir, "references/conventions.md")); err != nil || string(b) != "conventions\n" {
		t.Fatalf("materialized reference %q %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(copyDir, SkillMarker)); strings.TrimSpace(string(b)) != list[0].Hash {
		t.Fatalf("marker %q", b)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".claude/skills/stale")); !os.IsNotExist(err) {
		t.Fatalf("stale copy kept: %v", err)
	}
	if st := git(t, cwd, "status", "--porcelain", "--untracked-files=all"); st != "" {
		t.Fatalf("materialized skills visible to git:\n%s", st)
	}
	s.Close()
	if _, err := os.Stat(copyDir); !os.IsNotExist(err) {
		t.Fatalf("copy left after Close: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".claude/skills/own/SKILL.md")); err != nil {
		t.Fatal("project skill removed:", err)
	}
}

// TestSkillsConflict: a project skill with the name of a pinned skill refuses the launch before the shim.
func TestSkillsConflict(t *testing.T) {
	cwd, root, list := skillsFixture(t)
	writeFile(t, filepath.Join(cwd, ".claude/skills/openspec/SKILL.md"), "project's own")
	d, rec := fakeDriver(t, "start", claudetest.Dir())
	l, _ := scenarioLaunch(t, "start")
	l.Cwd, l.SkillsRoot, l.Skills = cwd, root, list
	if _, err := d.Launch(context.Background(), l); err == nil || !strings.Contains(err.Error(), "own .claude/skills/openspec") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(rec); !os.IsNotExist(err) {
		t.Fatal("shim started")
	}
	if b, _ := os.ReadFile(filepath.Join(cwd, ".claude/skills/openspec/SKILL.md")); string(b) != "project's own" {
		t.Fatalf("project skill changed: %q", b)
	}
	// Without the project setting source Claude Code would not read the skills: refused.
	d.SettingSources = []string{"local"}
	l.Skills[0].Name = "other"
	if _, err := d.Launch(context.Background(), l); err == nil || !strings.Contains(err.Error(), "setting_sources") {
		t.Fatalf("err = %v", err)
	}
}

// TestExcludeRelativeCwd: a relative cwd inside a repository (a relative state_dir) is excluded, not
// refused by filepath.Rel against the absolute top level.
func TestExcludeRelativeCwd(t *testing.T) {
	cwd, _, _ := skillsFixture(t)
	t.Chdir(cwd)
	if err := os.MkdirAll("data/conv", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exclude("data/conv", []string{"openspec"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(cwd, ".git", "info", "exclude"))
	if err != nil || !strings.Contains(string(b), "/data/conv/.claude/skills/openspec/") {
		t.Fatalf("exclude %q %v", b, err)
	}
}

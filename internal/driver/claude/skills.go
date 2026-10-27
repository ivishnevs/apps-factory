package claude

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"appfactory/internal/driver"
	"appfactory/internal/gitexclude"
	"appfactory/internal/skills"
)

// SkillMarker marks a skill directory the driver materialized under <cwd>/.claude/skills; only marked
// directories are replaced or removed, a project's own skills are never touched.
const SkillMarker = ".factory-skill"

// installSkills copies the run's verified skills into <cwd>/.claude/skills/<name>, the project skills
// directory Claude Code reads with the "project" setting source, and keeps them out of git through the
// worktree's own excludes file. Copies left by a crashed run are replaced first. The returned func removes
// this run's copies.
func installSkills(cwd, root string, list []driver.Skill, sources []string) (func(), error) {
	if len(list) == 0 {
		return func() {}, nil
	}
	if sources != nil && !slices.Contains(sources, "project") {
		return nil, fmt.Errorf("skills: setting_sources %v must include project for Claude Code to read .claude/skills", sources)
	}
	dir := filepath.Join(cwd, ".claude", "skills")
	if err := removeMarked(dir); err != nil {
		return nil, err
	}
	var installed []string
	cleanup := func() {
		for _, p := range installed {
			_ = os.RemoveAll(p)
		}
	}
	names := make([]string, 0, len(list))
	for _, s := range list {
		target := filepath.Join(dir, s.Name)
		if _, err := os.Lstat(target); err == nil {
			cleanup()
			return nil, fmt.Errorf("skills: the project has its own .claude/skills/%s; the run's pinned skill %s cannot be materialized", s.Name, s.Name)
		}
		hash, err := skills.Copy(filepath.Join(root, s.Name), target)
		if err != nil {
			_ = os.RemoveAll(target)
			cleanup()
			return nil, fmt.Errorf("skills: %s: %w", s.Name, err)
		}
		installed = append(installed, target)
		if s.Hash != "" && hash != s.Hash {
			cleanup()
			return nil, fmt.Errorf("skills: %s copy hash %s differs from the verified %s", s.Name, hash, s.Hash)
		}
		if err = os.WriteFile(filepath.Join(target, SkillMarker), []byte(hash+"\n"), 0o644); err != nil {
			cleanup()
			return nil, err
		}
		names = append(names, s.Name)
	}
	if err := exclude(cwd, names); err != nil {
		cleanup()
		return nil, err
	}
	return cleanup, nil
}

// removeMarked removes the driver's copies under dir.
func removeMarked(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if _, err := os.Lstat(filepath.Join(p, SkillMarker)); e.IsDir() && err == nil {
			if err = os.RemoveAll(p); err != nil {
				return err
			}
		}
	}
	return nil
}

// exclude makes git ignore /.claude/skills/<name>/ in the worktree containing cwd only (per-worktree
// excludes, see gitexclude; never the shared info/exclude, which the owner's checkout and sibling
// worktrees read too), so neither the agent's commits nor the worktree seal pick the copies up; outside a
// repository it does nothing.
func exclude(cwd string, names []string) error {
	// git prints absolute, symlink-resolved paths: Rel needs cwd the same way. Abs first, or a relative
	// cwd resolves against an unresolved working directory (/var vs /private/var on macOS).
	cwd, err := filepath.Abs(cwd)
	if err != nil {
		return err
	}
	if cwd, err = filepath.EvalSymlinks(cwd); err != nil {
		return err
	}
	cmd := exec.Command("git", "rev-parse", "--path-format=absolute", "--show-toplevel")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return nil // not a git work tree
	}
	top := strings.TrimSpace(string(out))
	if top == "" {
		return nil
	}
	rel, err := filepath.Rel(top, cwd)
	if err != nil {
		return err
	}
	prefix := "/"
	if rel != "." {
		prefix += filepath.ToSlash(rel) + "/"
	}
	patterns := make([]string, 0, len(names))
	for _, n := range names {
		patterns = append(patterns, prefix+".claude/skills/"+n+"/")
	}
	return gitexclude.Add(context.Background(), cwd, patterns...)
}

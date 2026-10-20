package claude

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"appfactory/internal/driver"
	"appfactory/internal/skills"
)

// SkillMarker marks a skill directory the driver materialized under <cwd>/.claude/skills; only marked
// directories are replaced or removed, a project's own skills are never touched.
const SkillMarker = ".factory-skill"

// installSkills copies the run's verified skills into <cwd>/.claude/skills/<name>, the project skills
// directory Claude Code reads with the "project" setting source, and keeps them out of git through the
// repository's info/exclude. Copies left by a crashed run are replaced first. The returned func removes
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

// exclude adds /.claude/skills/<name>/ to the info/exclude of the repository containing cwd, so neither
// the agent's commits nor the worktree seal pick the copies up; outside a repository it does nothing.
func exclude(cwd string, names []string) error {
	cwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return err
	}
	if cwd, err = filepath.Abs(cwd); err != nil { // git prints absolute paths: Rel needs both absolute
		return err
	}
	cmd := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir", "--show-toplevel")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return nil // not a git repository
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return nil
	}
	common, top := lines[0], lines[1]
	rel, err := filepath.Rel(top, cwd)
	if err != nil {
		return err
	}
	prefix := "/"
	if rel != "." {
		prefix += filepath.ToSlash(rel) + "/"
	}
	p := filepath.Join(common, "info", "exclude")
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var add bytes.Buffer
	for _, n := range names {
		if l := prefix + ".claude/skills/" + n + "/"; !have[l] {
			add.WriteString(l + "\n")
		}
	}
	if add.Len() == 0 {
		return nil
	}
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		b = append(b, '\n')
	}
	if err = os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, append(b, add.Bytes()...), 0o644)
}

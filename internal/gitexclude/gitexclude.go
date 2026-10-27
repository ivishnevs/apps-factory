// Package gitexclude makes git ignore the files the node materializes into a worktree (the OpenSpec
// tree, .factory, pinned skills) in that worktree only.
//
// git reads info/exclude from the repository's common directory, so a rule written there applies to the
// main checkout and every linked worktree of the repository: a factory worktree created from the owner's
// checkout would make the owner's own new files under openspec/ silently ignored. Instead the rules go to
// a file inside the worktree's own git directory (<git-dir>/factory-exclude) that the worktree's
// per-worktree configuration (extensions.worktreeConfig + `git config --worktree`) names as
// core.excludesFile. That setting replaces the user's global excludes file for this worktree, so the
// file also carries a copy of it, refreshed on every Add.
package gitexclude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FileName is the per-worktree excludes file, inside the worktree's git directory.
const FileName = "factory-exclude"

const (
	header     = "# App Factory: files the node materializes into this worktree; git ignores them in this worktree only."
	copyHeader = "# App Factory: copy of the excludes file this worktree's core.excludesFile replaces: "
)

// Add makes git ignore patterns (anchored to the worktree root, e.g. "/.factory/") in the worktree that
// contains dir, and nowhere else. The same lines earlier node versions wrote to the repository's shared
// info/exclude are removed from it; other lines there stay. Outside a work tree it does nothing.
func Add(ctx context.Context, dir string, patterns ...string) error {
	out, err := run(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err != nil || out != "true" {
		return nil // not a repository, or a bare one
	}
	out, err = run(ctx, dir, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return err
	}
	dirs := strings.Split(out, "\n")
	if len(dirs) != 2 {
		return fmt.Errorf("gitexclude: unexpected rev-parse output %q", out)
	}
	gitDir, common := dirs[0], dirs[1]
	if err = enableWorktreeConfig(ctx, dir, common); err != nil {
		return err
	}
	file := filepath.Join(gitDir, FileName)
	if err = writeFile(file, baseExcludes(ctx, dir, file), patterns); err != nil {
		return err
	}
	if cur, _ := run(ctx, dir, "config", "--worktree", "--get", "core.excludesFile"); cur != file {
		if _, err = run(ctx, dir, "config", "--worktree", "core.excludesFile", file); err != nil {
			return err
		}
	}
	return removeLegacy(filepath.Join(common, "info", "exclude"), patterns)
}

func run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// enableWorktreeConfig turns on extensions.worktreeConfig. As git requires, a core.bare=true or
// core.worktree of the common config moves to the main worktree's config.worktree first, or linked
// worktrees would inherit it.
func enableWorktreeConfig(ctx context.Context, dir, common string) error {
	cfg := filepath.Join(common, "config")
	if v, _ := run(ctx, dir, "config", "--file", cfg, "--type=bool", "--get", "extensions.worktreeConfig"); v == "true" {
		return nil
	}
	for _, key := range []string{"core.bare", "core.worktree"} {
		v, err := run(ctx, dir, "config", "--file", cfg, "--get", key)
		if err != nil || (key == "core.bare" && v != "true") {
			continue
		}
		if _, err = run(ctx, dir, "config", "--file", filepath.Join(common, "config.worktree"), key, v); err != nil {
			return err
		}
		if _, err = run(ctx, dir, "config", "--file", cfg, "--unset-all", key); err != nil {
			return err
		}
	}
	_, err := run(ctx, dir, "config", "--file", cfg, "extensions.worktreeConfig", "true")
	return err
}

// baseExcludes is the excludes file git would use here without the per-worktree setting: the last
// core.excludesFile outside the worktree scope, else $XDG_CONFIG_HOME/git/ignore or ~/.config/git/ignore.
func baseExcludes(ctx context.Context, dir, own string) string {
	base := ""
	out, _ := run(ctx, dir, "config", "--type=path", "--show-scope", "--get-all", "core.excludesFile")
	for _, l := range strings.Split(out, "\n") {
		scope, v, ok := strings.Cut(l, "\t")
		if ok && scope != "worktree" && v != own && v != "" {
			base = v
		}
	}
	if base == "" {
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			base = filepath.Join(x, "git", "ignore")
		} else if h, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(h, ".config", "git", "ignore")
		}
	}
	return base
}

// writeFile rewrites the per-worktree file: the factory's patterns (kept ones plus new) and a fresh
// copy of base.
func writeFile(file, base string, patterns []string) error {
	old, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var keep []string
	seen := map[string]bool{}
	for _, l := range strings.Split(string(old), "\n") {
		if strings.HasPrefix(l, copyHeader) {
			break
		}
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") && !seen[l] {
			seen[l] = true
			keep = append(keep, l)
		}
	}
	for _, p := range patterns {
		if p = strings.TrimSpace(p); p != "" && !seen[p] {
			seen[p] = true
			keep = append(keep, p)
		}
	}
	var b strings.Builder
	b.WriteString(header + "\n")
	for _, p := range keep {
		b.WriteString(p + "\n")
	}
	if base != "" {
		if c, err := os.ReadFile(base); err == nil && len(c) > 0 {
			b.WriteString(copyHeader + base + "\n")
			b.Write(c)
			if !bytes.HasSuffix(c, []byte("\n")) {
				b.WriteByte('\n')
			}
		}
	}
	if string(old) == b.String() {
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(file), FileName+".tmp-")
	if err != nil {
		return err
	}
	if _, err = tmp.WriteString(b.String()); err == nil {
		err = tmp.Chmod(0o644)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), file)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// removeLegacy drops the exact lines in patterns from the shared info/exclude (earlier node versions
// wrote them there); every other line stays as it is.
func removeLegacy(file string, patterns []string) error {
	b, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	drop := map[string]bool{}
	for _, p := range patterns {
		drop[strings.TrimSpace(p)] = true
	}
	lines := strings.SplitAfter(string(b), "\n")
	kept := lines[:0]
	for _, l := range lines {
		if t := strings.TrimSpace(l); t == "" || !drop[t] {
			kept = append(kept, l)
		}
	}
	if s := strings.Join(kept, ""); s != string(b) {
		return os.WriteFile(file, []byte(s), 0o644)
	}
	return nil
}

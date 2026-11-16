package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// A project's clone on the node (<root>/<company>/<project>/repo) is a partial clone (node-repo-store): the
// history of every file below the blob limit is local, so diff, log -p and blame work offline as before; larger
// files (images, video, builds) are fetched from origin when a checkout or a command needs a version the clone
// does not hold. A remote that does not support filters gives a full clone, as before.

var blobLimitRe = regexp.MustCompile(`^[1-9][0-9]*[kKmMgG]?$`)

// ValidBlobLimit: "" or "0" (full clones) or a git size (512k, 1m, 2g).
func ValidBlobLimit(s string) bool { return s == "" || s == "0" || blobLimitRe.MatchString(s) }

func blobFilter(limit string) string {
	if limit == "" || limit == "0" {
		return ""
	}
	return "blob:limit=" + strings.ToLower(limit)
}

// partial: the repository is a partial clone of origin.
func partial(ctx context.Context, repo string) bool {
	v, _ := git(ctx, repo, nil, "config", "--get", "remote.origin.promisor")
	return v == "true"
}

// cloneBare clones url bare into dst, with the node's blob filter when it has one; a clone with a filter that
// fails is tried again without it (a git or a remote without filter support).
func (w *Worktrees) cloneBare(ctx context.Context, dir, url, dst string) error {
	if w.filter != "" {
		if _, err := git(ctx, dir, nil, "clone", "--bare", "--quiet", "--filter="+w.filter, "--", url, dst); err == nil {
			return nil
		} else {
			slog.Warn("partial clone failed; cloning in full", "url", url, "error", err)
		}
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
	}
	_, err := git(ctx, dir, nil, "clone", "--bare", "--quiet", "--", url, dst)
	return err
}

// transplant copies into dst every object of src that origin does not hold: commits and their files on no
// origin branch (unpushed work, WIP refs, reflogs) and what the indexes of src's worktrees stage. Objects
// origin holds stay out: dst fetches them when it needs them.
func transplant(ctx context.Context, src, dst string) error {
	list, err := git(ctx, src, nil, "rev-list", "--objects", "--all", "--reflog", "--indexed-objects", "--not", "--remotes=origin")
	if err != nil {
		return err
	}
	if strings.TrimSpace(list) == "" {
		return nil
	}
	pack := exec.CommandContext(ctx, "git", "pack-objects", "--stdout", "--quiet")
	pack.Dir, pack.Stdin = src, strings.NewReader(list+"\n")
	index := exec.CommandContext(ctx, "git", "index-pack", "--stdin")
	index.Dir = dst
	r, wr := io.Pipe()
	pack.Stdout, index.Stdin = wr, r
	var perr, ierr strings.Builder
	pack.Stderr, index.Stderr = &perr, &ierr
	if err = index.Start(); err != nil {
		return err
	}
	err = pack.Run()
	_ = wr.CloseWithError(err)
	if e := index.Wait(); err == nil && e != nil {
		err = fmt.Errorf("git index-pack: %w: %s", e, strings.TrimSpace(ierr.String()))
	}
	if err != nil {
		return fmt.Errorf("copying local objects: %w: %s", err, strings.TrimSpace(perr.String()))
	}
	return nil
}

// copyRefs points dst's refs at src's for every ref but the remote-tracking ones (dst fetched its own).
func copyRefs(ctx context.Context, src, dst string) error {
	out, err := git(ctx, src, nil, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, l := range strings.Split(out, "\n") {
		ref, sha, ok := strings.Cut(l, " ")
		if !ok || strings.HasPrefix(ref, "refs/remotes/") {
			continue
		}
		fmt.Fprintf(&b, "update %s %s\n", ref, sha)
	}
	if b.Len() == 0 {
		return nil
	}
	return gitStdin(ctx, dst, b.String(), "update-ref", "--stdin")
}

func gitStdin(ctx context.Context, dir, in string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir, cmd.Stdin = dir, strings.NewReader(in)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

const compactReady = "factory-compact-ready"

// recoverCompact finishes or drops a compaction a crash interrupted (repo.lock held): a new clone not yet
// verified is dropped; a verified one replaces the old clone.
func recoverCompact(dir string) error {
	repo, tmp, old := filepath.Join(dir, "repo"), filepath.Join(dir, "repo.compact"), filepath.Join(dir, "repo.old")
	if pathExists(tmp) && !pathExists(filepath.Join(tmp, compactReady)) {
		if err := os.RemoveAll(tmp); err != nil {
			return err
		}
	}
	if pathExists(tmp) {
		if pathExists(repo) {
			if pathExists(filepath.Join(repo, "worktrees")) {
				if err := os.Rename(filepath.Join(repo, "worktrees"), filepath.Join(tmp, "worktrees")); err != nil {
					return err
				}
			}
			if err := os.RemoveAll(old); err != nil {
				return err
			}
			if err := os.Rename(repo, old); err != nil {
				return err
			}
		}
		if err := os.Rename(tmp, repo); err != nil {
			return err
		}
	}
	if pathExists(old) {
		if !pathExists(repo) {
			return os.Rename(old, repo)
		}
		if err := os.RemoveAll(old); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(repo, compactReady)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// CompactAll makes every full project clone of the node a partial one (Compact); a project in use is left for
// a later call. It answers whether a project is still to compact.
func (w *Worktrees) CompactAll(ctx context.Context) (pending bool) {
	if w.filter == "" {
		return false
	}
	dirs, _ := filepath.Glob(filepath.Join(w.root, "*", "*", "repo"))
	for _, repo := range dirs {
		done, err := w.Compact(ctx, filepath.Dir(repo))
		if err != nil {
			slog.Warn("compacting the project clone", "path", repo, "error", err)
		}
		if !done && err == nil {
			pending = true
		}
	}
	return pending
}

// Compact replaces the full clone of the project directory dir by a partial one (node-repo-store) while nothing
// uses it: no run holds a worktree of it, no review copy exists, no run writes the project workspace when it is
// a worktree of it. The new clone gets every object origin does not hold (transplant), every local ref, the
// configuration and the worktrees; what origin holds is fetched again when needed. done: the clone is partial
// now (or the node keeps full clones); false without an error: it is in use, try again later.
func (w *Worktrees) Compact(ctx context.Context, dir string) (done bool, err error) {
	if w.filter == "" {
		return true, nil
	}
	unlock, err := lockRepo(ctx, filepath.Join(dir, "repo.lock"))
	if err != nil {
		return false, err
	}
	defer unlock()
	if err = recoverCompact(dir); err != nil {
		return false, err
	}
	repo := filepath.Join(dir, "repo")
	if out, err := git(ctx, repo, nil, "rev-parse", "--is-bare-repository"); err != nil || out != "true" {
		return err == nil, err // not a clone of this allocator: Ensure replaces it
	}
	if partial(ctx, repo) {
		return true, nil
	}
	release, idle, err := lockWorktrees(ctx, dir, repo)
	if err != nil || !idle {
		return false, err
	}
	defer release()
	url, err := git(ctx, repo, nil, "config", "--get", "remote.origin.url")
	if err != nil {
		return false, err
	}
	tmp := filepath.Join(dir, "repo.compact")
	if _, err = git(ctx, dir, nil, "clone", "--bare", "--quiet", "--filter="+w.filter, "--", url, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return false, err
	}
	fail := func(err error) (bool, error) { _ = os.RemoveAll(tmp); return false, err }
	if !partial(ctx, tmp) {
		// The remote ignores filters: the full clone stays.
		slog.Info("the remote does not serve partial clones; the full clone stays", "path", repo)
		_ = os.RemoveAll(tmp)
		return true, nil
	}
	filter, _ := git(ctx, tmp, nil, "config", "--get", "remote.origin.partialclonefilter")
	if err = transplant(ctx, repo, tmp); err != nil {
		return fail(err)
	}
	// The old configuration (worktree settings, branch upstreams), with the partial clone's own keys.
	b, err := os.ReadFile(filepath.Join(repo, "config"))
	if err != nil {
		return fail(err)
	}
	if err = os.WriteFile(filepath.Join(tmp, "config"), b, 0o644); err != nil {
		return fail(err)
	}
	for _, kv := range [][2]string{{"core.repositoryformatversion", "1"}, {"extensions.partialClone", "origin"},
		{"remote.origin.promisor", "true"}, {"remote.origin.partialclonefilter", filter}} {
		if _, err = git(ctx, tmp, nil, "config", kv[0], kv[1]); err != nil {
			return fail(err)
		}
	}
	if err = configureRepo(ctx, tmp); err != nil {
		return fail(err)
	}
	if _, err = git(ctx, tmp, nil, "fetch", "--quiet", "--prune", "origin"); err != nil {
		return fail(err)
	}
	if err = copyRefs(ctx, repo, tmp); err != nil {
		return fail(err)
	}
	if pathExists(filepath.Join(repo, "info")) {
		_ = os.RemoveAll(filepath.Join(tmp, "info"))
		if err = copyDir(filepath.Join(repo, "info"), filepath.Join(tmp, "info")); err != nil {
			return fail(err)
		}
	}
	if err = os.WriteFile(filepath.Join(tmp, compactReady), nil, 0o644); err != nil {
		return fail(err)
	}
	if err = recoverCompact(dir); err != nil {
		return false, err
	}
	slog.Info("project clone is partial now", "path", repo, "filter", filter)
	return true, nil
}

// lockWorktrees takes, without waiting, the lock of every worktree of repo the node knows (task worktrees, the
// project workspace); idle is false when one is held or a review copy exists. Worktrees an agent added
// elsewhere are used only by a run, which holds its task worktree's lock.
func lockWorktrees(ctx context.Context, dir, repo string) (release func(), idle bool, err error) {
	var unlocks []func()
	release = func() {
		for _, u := range unlocks {
			u()
		}
	}
	if reviews, _ := filepath.Glob(filepath.Join(dir, "reviews", "*")); len(reviews) > 0 {
		return release, false, nil
	}
	out, err := git(ctx, repo, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return release, false, err
	}
	for _, l := range strings.Split(out, "\n") {
		path, ok := strings.CutPrefix(l, "worktree ")
		if !ok || samePathRepo(path, repo) {
			continue
		}
		var lock string
		switch {
		case pathExists(filepath.Join(path, ".factory", "state")):
			lock = filepath.Join(path, ".factory", "lock")
		case pathExists(filepath.Join(path, writerLock)):
			lock = filepath.Join(path, writerLock)
		default:
			continue
		}
		u, err := flockTry(lock)
		if errors.Is(err, ErrBusy) {
			release()
			return func() {}, false, nil
		}
		if err != nil {
			release()
			return func() {}, false, err
		}
		unlocks = append(unlocks, u)
	}
	return release, true, nil
}

func samePathRepo(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFile(p, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// KeepCompacting compacts the node's project clones now and again every interval while one is in use; once
// all are partial it ends. Calls after the first do nothing.
func (w *Worktrees) KeepCompacting(ctx context.Context, interval time.Duration) {
	if w.filter == "" || !w.compacting.CompareAndSwap(false, true) {
		return
	}
	go func() {
		for w.CompactAll(ctx) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
		}
	}()
}

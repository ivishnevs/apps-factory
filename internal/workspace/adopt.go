package workspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// NotAdoptableError: the project workspace keeps its own repository; Reason says why (the node retries later).
type NotAdoptableError struct{ Reason string }

func (e *NotAdoptableError) Error() string {
	return "workspace: project workspace kept as it is: " + e.Reason
}

// adoptRetry: how long a workspace AdoptWorkspace refused or failed is left as it is.
const adoptRetry = 6 * time.Hour

// adoptedSuffix: the workspace's own repository while it is replaced (removed once the worktree is verified).
const adoptedSuffix = ".git-adopted"

// adoptingSuffix: next to the workspace while AdoptWorkspace holds its writer lock; Local waits instead of
// refusing the run.
const adoptingSuffix = ".adopting"

// AdoptWorkspace makes ws, the project workspace the node cloned from url, a worktree of the project's clone
// (node-repo-store), so the node keeps one copy of the project's history instead of two. Nothing of the
// workspace is lost: its files stay where they are, its index and HEAD carry over, every object origin does not
// hold and every local branch or tag is copied into the project clone (one that would move a branch with work
// of its own is kept under refs/factory/workspace/). A workspace that is busy (its writer lock is held) is
// left for a later call (false, nil); one git cannot carry over safely (a stash, an operation in progress,
// submodules, its own worktrees, Git LFS, a shallow clone) answers a *NotAdoptableError.
func (w *Worktrees) AdoptWorkspace(ctx context.Context, company, project, url, ws string) (bool, error) {
	if t, ok := w.adoptAfter.Load(ws); ok && time.Now().Before(t.(time.Time)) {
		return false, nil
	}
	ok, err := w.adopt(ctx, company, project, url, ws)
	if err != nil {
		w.adoptAfter.Store(ws, time.Now().Add(adoptRetry))
	}
	return ok, err
}

func (w *Worktrees) adopt(ctx context.Context, company, project, url, ws string) (bool, error) {
	for _, id := range []string{company, project} {
		if !safeID.MatchString(id) || strings.Contains(id, "..") {
			return false, fmt.Errorf("workspace: invalid id %q", id)
		}
	}
	dir := filepath.Join(w.root, company, project)
	gitDir := filepath.Join(ws, ".git")
	if err := recoverAdopt(ctx, ws); err != nil {
		return false, err
	}
	if fi, err := os.Lstat(gitDir); err != nil || !fi.IsDir() {
		return false, err // a worktree already, or no repository
	}
	if cur, err := git(ctx, ws, nil, "config", "--get", "remote.origin.url"); err != nil || cur != url {
		return false, &NotAdoptableError{"its origin is not " + url}
	}
	for _, p := range []struct{ path, why string }{
		{"modules", "it has submodules"}, {"worktrees", "it has worktrees of its own"}, {"lfs", "it uses Git LFS"},
		{"shallow", "it is a shallow clone"}, {"MERGE_HEAD", "a merge is in progress"}, {"rebase-merge", "a rebase is in progress"},
		{"rebase-apply", "a rebase is in progress"}, {"CHERRY_PICK_HEAD", "a cherry-pick is in progress"},
		{"REVERT_HEAD", "a revert is in progress"}, {"BISECT_LOG", "a bisect is in progress"},
	} {
		if pathExists(filepath.Join(gitDir, p.path)) {
			return false, &NotAdoptableError{p.why}
		}
	}
	if _, err := git(ctx, ws, nil, "rev-parse", "--verify", "--quiet", "refs/stash"); err == nil {
		return false, &NotAdoptableError{"it has stashed changes"}
	}
	release, err := flockTry(filepath.Join(ws, writerLock))
	if errors.Is(err, ErrBusy) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer release()
	if err = os.WriteFile(ws+adoptingSuffix, nil, 0o644); err != nil {
		return false, err
	}
	defer os.Remove(ws + adoptingSuffix)
	unlock, err := lockRepo(ctx, filepath.Join(dir, "repo.lock"))
	if err != nil {
		return false, err
	}
	defer unlock()
	repo := filepath.Join(dir, "repo")
	if err = w.ensureClone(ctx, dir, repo, url); err != nil {
		return false, err
	}
	if _, err = git(ctx, repo, nil, "fetch", "--quiet", "--prune", "origin"); err != nil {
		return false, err
	}
	head, err := git(ctx, ws, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return false, &NotAdoptableError{"it has no commit"}
	}
	sym, _ := git(ctx, ws, nil, "symbolic-ref", "--quiet", "HEAD")
	status, err := git(ctx, ws, nil, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}
	// The workspace's view of origin decides what transplant copies: refresh it so nothing origin dropped is
	// taken for pushed.
	if _, err = git(ctx, ws, nil, "fetch", "--quiet", "--prune", "origin"); err != nil {
		return false, err
	}
	if err = transplant(ctx, ws, repo); err != nil {
		return false, err
	}
	checkedOut, err := branchesCheckedOut(ctx, repo)
	if err != nil {
		return false, err
	}
	if err = mergeRefs(ctx, ws, repo, checkedOut); err != nil {
		return false, err
	}
	if sym != "" {
		if cur, _ := git(ctx, repo, nil, "rev-parse", "--verify", "--quiet", sym); cur != head || checkedOut[sym] {
			slog.Warn("project workspace detached: its branch is held elsewhere", "workspace", ws, "branch", sym)
			sym = ""
		}
	}
	// A worktree entry of repo for ws: added at a temporary path, then pointed at ws.
	tmp := filepath.Join(dir, "workspace")
	_ = os.RemoveAll(tmp)
	if _, err = git(ctx, repo, nil, "worktree", "prune"); err != nil {
		return false, err
	}
	if _, err = git(ctx, repo, nil, "worktree", "add", "--quiet", "--no-checkout", "--detach", tmp, head); err != nil {
		return false, err
	}
	admin, err := git(ctx, tmp, nil, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return false, err
	}
	undo := func(err error) (bool, error) {
		_ = os.RemoveAll(tmp)
		_ = os.RemoveAll(admin)
		_, _ = git(ctx, repo, nil, "worktree", "prune")
		return false, err
	}
	if err = carryWorktreeState(ctx, gitDir, admin, sym); err != nil {
		return undo(err)
	}
	// The swap: ws/.git becomes the worktree's link; the old repository stays aside until verified.
	adopted := ws + adoptedSuffix
	if err = os.Rename(gitDir, adopted); err != nil {
		return undo(err)
	}
	rollback := func(err error) (bool, error) {
		_ = os.Remove(gitDir)
		if e := os.Rename(adopted, gitDir); e != nil {
			return false, errors.Join(err, e)
		}
		return undo(err)
	}
	if err = os.WriteFile(gitDir, []byte("gitdir: "+admin+"\n"), 0o644); err != nil {
		return rollback(err)
	}
	if err = os.WriteFile(filepath.Join(admin, "gitdir"), []byte(gitDir+"\n"), 0o644); err != nil {
		return rollback(err)
	}
	_ = os.RemoveAll(tmp)
	got, err := git(ctx, ws, nil, "rev-parse", "--verify", "HEAD")
	if err != nil || got != head {
		return rollback(fmt.Errorf("workspace: adopted HEAD %q is not %s: %v", got, head, err))
	}
	after, err := git(ctx, ws, nil, "status", "--porcelain", "--untracked-files=no")
	if err != nil || after != status {
		return rollback(fmt.Errorf("workspace: adopted status differs (%v):\n%s\nwas:\n%s", err, after, status))
	}
	if err = os.RemoveAll(adopted); err != nil {
		slog.Warn("removing the project workspace's old repository", "path", adopted, "error", err)
	}
	slog.Info("project workspace shares the project clone now", "workspace", ws, "repo", repo)
	return true, nil
}

// recoverAdopt finishes or undoes an adoption a crash interrupted: with ws/.git a working link the old
// repository is removed, without one it is put back.
func recoverAdopt(ctx context.Context, ws string) error {
	adopted, gitDir := ws+adoptedSuffix, filepath.Join(ws, ".git")
	if !pathExists(adopted) {
		return nil
	}
	if fi, err := os.Lstat(gitDir); err == nil && !fi.IsDir() {
		if _, err = git(ctx, ws, nil, "rev-parse", "--verify", "HEAD"); err == nil {
			return os.RemoveAll(adopted)
		}
		if err = os.Remove(gitDir); err != nil {
			return err
		}
	} else if err == nil {
		return fmt.Errorf("workspace: both %s and %s exist", gitDir, adopted)
	}
	return os.Rename(adopted, gitDir)
}

// branchesCheckedOut: the branches the worktrees of repo have checked out.
func branchesCheckedOut(ctx context.Context, repo string) (map[string]bool, error) {
	out, err := git(ctx, repo, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	m := map[string]bool{}
	for _, l := range strings.Split(out, "\n") {
		if b, ok := strings.CutPrefix(l, "branch "); ok {
			m[b] = true
		}
	}
	return m, nil
}

// mergeRefs copies the local refs of the workspace ws into repo (objects transplanted already). A ref repo
// lacks is created; one repo has elsewhere is moved only when that does not drop work: repo's commit is on
// origin or in the workspace's history, and no worktree has the branch checked out. Otherwise the
// workspace's ref is kept under refs/factory/workspace/.
func mergeRefs(ctx context.Context, ws, repo string, checkedOut map[string]bool) error {
	out, err := git(ctx, ws, nil, "for-each-ref", "--format=%(refname) %(objectname)")
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, l := range strings.Split(out, "\n") {
		ref, sha, ok := strings.Cut(l, " ")
		if !ok || strings.HasPrefix(ref, "refs/remotes/") || ref == "refs/stash" {
			continue
		}
		cur, _ := git(ctx, repo, nil, "rev-parse", "--verify", "--quiet", ref)
		switch {
		case cur == sha:
			continue
		case cur == "":
		case !checkedOut[ref] && (onOrigin(ctx, repo, cur) || isAnc(ctx, repo, cur, sha)):
		default:
			slog.Warn("project workspace ref kept aside", "ref", ref, "workspace", ws)
			ref = "refs/factory/workspace/" + strings.TrimPrefix(ref, "refs/")
		}
		fmt.Fprintf(&b, "update %s %s\n", ref, sha)
	}
	if b.Len() == 0 {
		return nil
	}
	return gitStdin(ctx, repo, b.String(), "update-ref", "--stdin")
}

func onOrigin(ctx context.Context, repo, sha string) bool {
	out, err := git(ctx, repo, nil, "for-each-ref", "--count=1", "--contains", sha, "--format=%(refname)", "refs/remotes/origin")
	return err == nil && out != ""
}

// carryWorktreeState gives the new worktree entry admin the workspace's HEAD (sym, else the detached commit
// already there), index, per-worktree settings and excludes, and the node's project marker.
func carryWorktreeState(ctx context.Context, gitDir, admin, sym string) error {
	if sym != "" {
		if err := os.WriteFile(filepath.Join(admin, "HEAD"), []byte("ref: "+sym+"\n"), 0o644); err != nil {
			return err
		}
	}
	for _, f := range []string{"index", "factory-project"} {
		if fi, err := os.Stat(filepath.Join(gitDir, f)); err == nil {
			if err = copyFile(filepath.Join(gitDir, f), filepath.Join(admin, f), fi.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	cfg := filepath.Join(admin, "config.worktree")
	set := func(k, v string) error {
		_, err := git(ctx, admin, nil, "config", "--file", cfg, k, v)
		return err
	}
	// The workspace's excludes: the node's per-worktree file and its info/exclude, which in a linked worktree
	// would otherwise not apply (info/exclude is the common directory's).
	var excludes []string
	for _, f := range []string{filepath.Join(gitDir, "factory-exclude"), filepath.Join(gitDir, "info", "exclude")} {
		if b, err := os.ReadFile(f); err == nil {
			excludes = append(excludes, strings.TrimRight(string(b), "\n"))
		}
	}
	if own, _ := git(ctx, admin, nil, "config", "--file", filepath.Join(gitDir, "config.worktree"), "--get", "core.excludesFile"); own == "" {
		// Without a per-worktree file the workspace used the global excludes: keep them.
		if base, _ := git(ctx, admin, nil, "config", "--global", "--type=path", "--get", "core.excludesFile"); base != "" {
			if b, err := os.ReadFile(base); err == nil {
				excludes = append(excludes, strings.TrimRight(string(b), "\n"))
			}
		}
	}
	if len(excludes) > 0 {
		file := filepath.Join(admin, "factory-exclude")
		if err := os.WriteFile(file, []byte(strings.Join(excludes, "\n")+"\n"), 0o644); err != nil {
			return err
		}
		if err := set("core.excludesFile", file); err != nil {
			return err
		}
	}
	// Settings of the workspace's own configuration that are not the repository's (identity, hooks, line
	// endings): per worktree now.
	out, _ := git(ctx, admin, nil, "config", "--file", filepath.Join(gitDir, "config"), "--list")
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		var err error
		switch {
		case carriedKey(k):
			err = set(k, v)
		case strings.HasPrefix(k, "branch."):
			// Upstreams are the repository's: added where the project clone has none.
			if cur, _ := git(ctx, admin, nil, "config", "--get", k); cur == "" {
				_, err = git(ctx, admin, nil, "config", k, v)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func carriedKey(k string) bool {
	switch {
	case strings.HasPrefix(k, "remote."), strings.HasPrefix(k, "branch."), strings.HasPrefix(k, "extensions."):
		return false
	case strings.HasPrefix(k, "core."):
		return k == "core.hookspath" || k == "core.autocrlf" || k == "core.eol" || k == "core.safecrlf"
	}
	return true
}

func isAnc(ctx context.Context, repo, a, b string) bool {
	ok, _ := ancestor(ctx, repo, a, b)
	return ok
}

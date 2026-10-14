package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ResetRef keeps a reset task's abandoned local state: the seal's WIP commit, else the old branch head
// when the remote does not contain it. Nothing of the task's work is lost by a reset.
func ResetRef(task string) string { return "refs/factory/reset/" + task }

// ResetResult describes a reset-workspace.
type ResetResult struct {
	// Seal is the seal of the old worktree (zero when there was none).
	Seal SealResult `json:"seal"`
	// PreviousHead is the local branch head before the reset ("" when the branch did not exist).
	PreviousHead string `json:"previous_head,omitempty"`
	// Kept is the commit saved on ResetRef ("" when everything was already on the remote).
	Kept string `json:"kept,omitempty"`
	// From is the start point of the new worktree: origin/<branch>, else origin/<default branch>.
	From string `json:"from"`
	Head string `json:"head"`
	Path string `json:"path"`
	// Branch is the task branch the worktree has checked out again.
	Branch string `json:"branch"`
}

// Reset is reset-workspace (task-worktrees 4.2), node side: under the task's run lock it stops a leftover
// run group, seals the worktree, removes it and its scratch directory, points the task branch at the
// remote branch (or the default branch when the task never pushed) and creates the worktree again. The
// abandoned local state stays on ResetRef. Forgetting the task's session records is the caller's part
// (the next run must start a fresh session).
//
// An empty RepoURL is taken from the task's existing clone; an empty Branch and Slug from the worktree's
// state, else the default task branch. ctx bounds the wait for a running run's lock.
func (w *Worktrees) Reset(ctx context.Context, r Request) (ResetResult, error) {
	dir, err := w.project(r)
	if err != nil {
		return ResetResult{}, err
	}
	path := filepath.Join(dir, "tasks", r.Task)
	repo := filepath.Join(dir, "repo")
	if r.RepoURL == "" {
		if r.RepoURL, err = git(ctx, repo, nil, "config", "--get", "remote.origin.url"); err != nil {
			return ResetResult{}, fmt.Errorf("workspace: no clone of project %s on this node to reset: %w", r.Project, err)
		}
	}
	if r.Branch == "" && r.Slug == "" {
		if st, err := readState(path); err == nil && st.Branch != "" {
			r.Branch = st.Branch
		}
	}
	branch := branchName(r)
	if _, err = git(ctx, "", nil, "check-ref-format", "--branch", branch); err != nil {
		return ResetResult{}, fmt.Errorf("workspace: invalid branch %q", branch)
	}
	res := ResetResult{Branch: branch, Path: path}
	if err = w.resetLocked(ctx, r, dir, path, repo, branch, &res); err != nil {
		return res, err
	}
	// The worktree lock is released: a run waiting for it sees the worktree gone and ensures it again,
	// which is the same idempotent Ensure as here.
	wt, err := w.Ensure(ctx, r)
	if err != nil {
		return res, err
	}
	res.Path = wt.Path
	res.Head, err = git(ctx, wt.Path, nil, "rev-parse", "--verify", "HEAD")
	return res, err
}

// resetLocked runs under the worktree's run lock: stop, seal, remove, move the branch.
func (w *Worktrees) resetLocked(ctx context.Context, r Request, dir, path, repo, branch string, res *ResetResult) error {
	unlock, err := flockWait(ctx, filepath.Join(path, ".factory", "lock"))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return ErrBusy
		}
		return err
	}
	defer unlock()
	if err = stopRun(path); err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(path, ".factory", "state")); err == nil {
		if res.Seal, err = w.Seal(ctx, r); err != nil {
			return fmt.Errorf("workspace: seal before reset: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	unlockRepo, err := lockRepo(ctx, filepath.Join(dir, "repo.lock"))
	if err != nil {
		return err
	}
	defer unlockRepo()
	if err = w.ensureClone(ctx, dir, repo, r.RepoURL); err != nil {
		return err
	}
	if _, err = git(ctx, repo, nil, "fetch", "--prune", "origin"); err != nil {
		return err
	}
	_, _ = git(ctx, repo, nil, "worktree", "remove", "--force", "--force", path)
	if err = os.RemoveAll(path); err != nil {
		return err
	}
	if err = os.RemoveAll(filepath.Join(dir, "scratch", r.Task)); err != nil {
		return err
	}
	if _, err = git(ctx, repo, nil, "worktree", "prune"); err != nil {
		return err
	}
	def := r.DefaultBranch
	if def == "" {
		if def, err = git(ctx, repo, nil, "symbolic-ref", "--short", "HEAD"); err != nil {
			return err
		}
	}
	res.From = "origin/" + branch
	start, err := git(ctx, repo, nil, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch)
	if err != nil {
		res.From = "origin/" + def
		if start, err = git(ctx, repo, nil, "rev-parse", "--verify", "refs/remotes/origin/"+def); err != nil {
			return err
		}
	}
	res.PreviousHead, _ = git(ctx, repo, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	keep := res.Seal.WIPSHA
	if keep == "" && res.PreviousHead != "" {
		if ok, err := ancestor(ctx, repo, res.PreviousHead, start); err != nil {
			return err
		} else if !ok {
			keep = res.PreviousHead
		}
	}
	if keep != "" {
		if _, err = git(ctx, repo, nil, "update-ref", "-m", "factory reset-workspace", ResetRef(r.Task), keep); err != nil {
			return err
		}
		res.Kept = keep
	}
	// The local WIP ref describes the removed worktree; the new one starts without it.
	if _, err = git(ctx, repo, nil, "update-ref", "-d", WIPRef(r.Task)); err != nil {
		return err
	}
	if _, err = git(ctx, repo, nil, "branch", "--no-track", "--force", branch, start); err != nil {
		return err
	}
	if _, err = git(ctx, repo, nil, "config", "branch."+branch+".remote", "origin"); err != nil {
		return err
	}
	_, err = git(ctx, repo, nil, "config", "branch."+branch+".merge", "refs/heads/"+branch)
	return err
}

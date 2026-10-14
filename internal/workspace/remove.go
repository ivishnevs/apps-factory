package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrBusy: a run holds the worktree lock.
var ErrBusy = errors.New("workspace: worktree is in use by a run")

// Find lists the worktrees of task on this node, one per project of company.
func (w *Worktrees) Find(company, task string) ([]Request, error) {
	if _, err := w.project(Request{Company: company, Project: "x", Task: task}); err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(w.root, company, "*", "tasks", task))
	if err != nil {
		return nil, err
	}
	var out []Request
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			out = append(out, Request{Company: company, Project: filepath.Base(filepath.Dir(filepath.Dir(p))), Task: task})
		}
	}
	return out, nil
}

// Remove closes a task's worktree (node-agent D4): under its run lock it stops a leftover run group, seals,
// calls hook with the worktree path, then removes the worktree and its scratch directory. The branch and
// the WIP ref stay in the repository. Nothing is removed when the seal fails; ErrBusy when a run holds the
// lock for wait.
func (w *Worktrees) Remove(ctx context.Context, r Request, wait time.Duration, hook func(path string)) (SealResult, error) {
	dir, err := w.project(r)
	if err != nil {
		return SealResult{}, err
	}
	path := filepath.Join(dir, "tasks", r.Task)
	lockCtx, stop := context.WithTimeout(ctx, wait)
	unlock, err := flockWait(lockCtx, filepath.Join(path, ".factory", "lock"))
	stop()
	if err != nil {
		if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
			return SealResult{}, ErrBusy
		}
		return SealResult{}, err
	}
	defer unlock()
	if err = stopRun(path); err != nil {
		return SealResult{}, err
	}
	var res SealResult
	if _, err = os.Stat(filepath.Join(path, ".factory", "state")); err == nil {
		if res, err = w.Seal(ctx, r); err != nil {
			return SealResult{}, fmt.Errorf("workspace: seal before removal: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return SealResult{}, err
	}
	// A directory without state is a half-created worktree: nothing of a run to seal.
	if hook != nil {
		hook(path)
	}
	unlockRepo, err := lockRepo(ctx, filepath.Join(dir, "repo.lock"))
	if err != nil {
		return res, err
	}
	defer unlockRepo()
	repo := filepath.Join(dir, "repo")
	_, _ = git(ctx, repo, nil, "worktree", "remove", "--force", "--force", path)
	if err = os.RemoveAll(path); err != nil {
		return res, err
	}
	if err = os.RemoveAll(filepath.Join(dir, "scratch", r.Task)); err != nil {
		return res, err
	}
	if _, err = os.Stat(repo); err == nil {
		_, err = git(ctx, repo, nil, "worktree", "prune")
	} else if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return res, err
}

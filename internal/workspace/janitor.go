package workspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// worktrees lists the task worktrees <root>/<company>/<project>/tasks/<task> for task (a glob pattern).
func (w *Worktrees) worktrees(task string) ([]Request, error) {
	paths, err := filepath.Glob(filepath.Join(w.root, "*", "*", "tasks", task))
	if err != nil {
		return nil, err
	}
	var r []Request
	for _, p := range paths {
		rel, _ := filepath.Rel(w.root, p)
		parts := strings.Split(rel, string(filepath.Separator))
		req := Request{Company: parts[0], Project: parts[1], Task: parts[3]}
		if _, err := w.project(req); err == nil {
			r = append(r, req)
		}
	}
	return r, nil
}

func (w *Worktrees) path(r Request) string {
	return filepath.Join(w.root, r.Company, r.Project, "tasks", r.Task)
}

// SealLeftovers stops and seals every worktree whose run pidfile outlived its holder, i.e. runs of a
// previous node process (task-worktrees D3; runtime protocol v2 §3, before register). The seal stays
// local: the run is not reported, Core ends runs of older epochs itself.
func (w *Worktrees) SealLeftovers(ctx context.Context) error {
	reqs, err := w.worktrees("*")
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range reqs {
		path := w.path(r)
		if _, err := os.Stat(pidPath(path)); err != nil {
			continue
		}
		if err := w.sealIdle(ctx, r, func() error { return nil }); err != nil {
			errs = append(errs, fmt.Errorf("task %s: %w", r.Task, err))
		}
	}
	return errors.Join(errs...)
}

// RemoveTask deletes a closed task's worktrees (node-agent D4 cleanup): stop a leftover run group, seal,
// `git worktree remove`, remove its scratch directory. The branch, the WIP ref and the clone stay.
// A worktree held by a run is kept and reported as ErrBusy.
func (w *Worktrees) RemoveTask(ctx context.Context, task string) error {
	if !safeID.MatchString(task) || strings.Contains(task, "..") {
		return fmt.Errorf("workspace: invalid id %q", task)
	}
	reqs, err := w.worktrees(task)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range reqs {
		err := w.sealIdle(ctx, r, func() error {
			dir, _ := w.project(r)
			unlock, err := lockRepo(ctx, filepath.Join(dir, "repo.lock"))
			if err != nil {
				return err
			}
			defer unlock()
			path := w.path(r)
			if _, err = git(ctx, filepath.Join(dir, "repo"), nil, "worktree", "remove", "--force", "--force", path); err != nil {
				slog.Warn("git worktree remove failed; deleting the directory", "path", path, "error", err)
				if err = os.RemoveAll(path); err != nil {
					return err
				}
				if _, err = git(ctx, filepath.Join(dir, "repo"), nil, "worktree", "prune"); err != nil {
					return err
				}
			}
			return os.RemoveAll(filepath.Join(dir, "scratch", r.Task))
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("%s/%s/%s: %w", r.Company, r.Project, r.Task, err))
		}
	}
	return errors.Join(errs...)
}

// sealIdle takes the worktree's run lock without waiting, stops a recorded run group, seals the
// worktree when it is valid, then runs then under the lock.
func (w *Worktrees) sealIdle(ctx context.Context, r Request, then func() error) error {
	path := w.path(r)
	unlock, err := flockTry(filepath.Join(path, ".factory", "lock"))
	if err != nil {
		return err
	}
	defer unlock()
	if err = stopRun(path); err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(path, ".factory", "state")); err == nil {
		res, err := w.Seal(ctx, r)
		if err != nil {
			return err
		}
		slog.Info("worktree sealed", "task", r.Task, "path", path, "sealed_head", res.SealedHead, "wip_sha", res.WIPSHA)
	}
	return then()
}

// flockTry takes an exclusive flock or fails with ErrBusy.
func flockTry(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

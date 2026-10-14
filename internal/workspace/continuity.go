package workspace

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
)

// Exit is a run's accepted exit as the worker saw it (docs/explicit-exits-contract.md §5): its kind and
// the commit it handed over (the head the adapter pushed; "" when unknown).
type Exit struct {
	Kind, SHA string
}

// ResetsToExit reports whether the next run of a task starts exactly from the exit's commit: true for
// handoff, submit and done; ask, block, wait and runs without an exit continue from the worktree as it is
// (task-worktrees D4).
func ResetsToExit(kind string) bool {
	switch kind {
	case "handoff", "submit", "done":
		return true
	}
	return false
}

// SealExit seals the worktree (D3) and records the run's accepted exit. After a handoff, submit or done
// with a known commit it then resets the worktree to it (`reset --hard` + `clean -fd`, D4) and records it
// as sealed_head, so the next run's start check expects exactly that commit. Nothing is lost: uncommitted
// work is on the WIP ref first, and commits made after the exit (HEAD not contained in the exit commit)
// become the WIP ref when there was no uncommitted work. Ignored files (and .factory/) stay.
func (w *Worktrees) SealExit(ctx context.Context, r Request, exit Exit) (SealResult, error) {
	dir, err := w.project(r)
	if err != nil {
		return SealResult{}, err
	}
	path := filepath.Join(dir, "tasks", r.Task)
	unlock, err := lockRepo(ctx, filepath.Join(dir, "repo.lock"))
	if err != nil {
		return SealResult{}, err
	}
	defer unlock()
	st, _ := readState(path)
	// A reset interrupted by a crash is finished before anything is snapshot again: the snapshot of a
	// half-reset tree would replace the WIP ref that holds the work.
	if err = finishReset(ctx, path, &st); err != nil {
		return SealResult{}, err
	}
	res, err := snapshot(ctx, r, path)
	if err != nil {
		return SealResult{}, err
	}
	res.ExitSHA = st.ExitSHA
	if exit.SHA != "" {
		res.ExitSHA = exit.SHA
	}
	st.Task = r.Task
	if ResetsToExit(exit.Kind) && exit.SHA != "" {
		if _, err = git(ctx, path, nil, "rev-parse", "--verify", "--quiet", exit.SHA+"^{commit}"); err != nil {
			// The seal stands; the worktree continues as it is (the start check then expects HEAD).
			res.Unpushed = unpushed(ctx, path)
			st.SealResult = res
			return res, errors.Join(fmt.Errorf("workspace: exit commit %s not in the worktree; not reset", exit.SHA), writeState(path, st))
		}
		if res.SealedHead != exit.SHA && res.WIPSHA == "" {
			if ok, err := ancestor(ctx, path, res.SealedHead, exit.SHA); err != nil {
				return SealResult{}, err
			} else if !ok {
				if _, err = git(ctx, path, nil, "update-ref", "-m", "factory seal: commits after the exit", WIPRef(r.Task), res.SealedHead); err != nil {
					return SealResult{}, err
				}
				res.WIPSHA = res.SealedHead
			}
		}
		st.ResetFrom = res.SealedHead
		res.SealedHead, res.Reset = exit.SHA, true
		st.SealResult = res
		if err = writeState(path, st); err != nil {
			return SealResult{}, err
		}
		if err = finishReset(ctx, path, &st); err != nil {
			return SealResult{}, err
		}
	}
	res.Unpushed = unpushed(ctx, path)
	st.SealResult = res
	return res, writeState(path, st)
}

// finishReset completes a recorded reset to st.SealedHead; nothing to do without one. Under the repo lock.
func finishReset(ctx context.Context, path string, st *worktreeState) error {
	if st.ResetFrom == "" {
		return nil
	}
	if _, err := git(ctx, path, nil, "reset", "--hard", "--quiet", st.SealedHead); err != nil {
		return err
	}
	if _, err := git(ctx, path, nil, "clean", "-fdq"); err != nil {
		return err
	}
	st.ResetFrom = ""
	return writeState(path, *st)
}

// unpushed reports whether HEAD has commits that no origin branch contains (as last fetched or pushed).
func unpushed(ctx context.Context, path string) bool {
	out, err := git(ctx, path, nil, "rev-list", "--count", "HEAD", "--not", "--remotes=origin")
	if err != nil {
		return false
	}
	n, _ := strconv.Atoi(out)
	return n > 0
}

// freshStart is the fresh_from_default start (D4): seal what the last run left, then check out the just
// fetched origin/<default> detached with a clean tree; sealed_head follows. No start check.
func (w *Worktrees) freshStart(ctx context.Context, r Request, wt Worktree) error {
	if !wt.created {
		if _, err := w.Seal(ctx, r); err != nil {
			return err
		}
	}
	dir, err := w.project(r)
	if err != nil {
		return err
	}
	unlock, err := lockRepo(ctx, filepath.Join(dir, "repo.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	def := r.DefaultBranch
	if def == "" {
		if def, err = git(ctx, wt.Repo, nil, "symbolic-ref", "--short", "HEAD"); err != nil {
			return err
		}
	}
	target, err := git(ctx, wt.Path, nil, "rev-parse", "--verify", "refs/remotes/origin/"+def+"^{commit}")
	if err != nil {
		return err
	}
	if _, err = git(ctx, wt.Path, nil, "checkout", "--quiet", "--force", "--detach", target); err != nil {
		return err
	}
	if _, err = git(ctx, wt.Path, nil, "clean", "-fdq"); err != nil {
		return err
	}
	st, _ := readState(wt.Path)
	st.Task, st.Branch, st.SealedHead, st.ResetFrom = r.Task, "", target, ""
	return writeState(wt.Path, st)
}

// Head is the worktree's current HEAD commit.
func (t Worktree) Head(ctx context.Context) (string, error) {
	return git(ctx, t.Path, nil, "rev-parse", "--verify", "HEAD")
}

// IsLockError reports a git lock failure (index.lock, config.lock, packed-refs.lock, a ref lock) that
// persisted through the allocator's retries: contention, not a broken setup (task-worktrees D1).
func IsLockError(err error) bool {
	var ge *gitError
	return errors.As(err, &ge) && lockErr.MatchString(ge.stderr)
}

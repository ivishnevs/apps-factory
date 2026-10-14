package workspace

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
)

// DivergedError is a failed start check (task-worktrees D4): the worktree's HEAD moved outside any run,
// or its uncommitted changes block the fast-forward to the remote branch. The worktree is left untouched;
// Core escalates workspace_diverged (answers: reset from the remote branch, cancel).
type DivergedError struct {
	Task, Branch, Path string
	// Head is the worktree's HEAD, SealedHead the HEAD recorded by the last seal, Remote origin/<branch>
	// ("" when the branch was never pushed).
	Head, SealedHead, Remote string
	Reason                   string
}

func (e *DivergedError) Error() string {
	return fmt.Sprintf("workspace_diverged: task %s worktree %s (branch %s): %s; HEAD %s, sealed_head %s, origin %s",
		e.Task, e.Path, e.Branch, e.Reason, short(e.Head), short(e.SealedHead), short(e.Remote))
}
func short(sha string) string {
	if sha == "" {
		return "-"
	}
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// startCheck: HEAD == sealed_head (or nothing sealed yet, or a just created worktree) → start, after
// fast-forwarding to origin/<branch> when the remote is strictly ahead; git refuses the fast-forward when
// uncommitted changes conflict, which is reported as diverged. HEAD != sealed_head → diverged.
func (w *Worktrees) startCheck(ctx context.Context, r Request, wt Worktree) error {
	dir, err := w.project(r)
	if err != nil {
		return err
	}
	unlock, err := lockRepo(ctx, filepath.Join(dir, "repo.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	head, err := git(ctx, wt.Path, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	remote, _ := git(ctx, wt.Path, nil, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+wt.Branch)
	st, err := readState(wt.Path)
	if err != nil {
		return err
	}
	diverged := func(reason string) error {
		return &DivergedError{Task: r.Task, Branch: wt.Branch, Path: wt.Path, Head: head, SealedHead: st.SealedHead, Remote: remote, Reason: reason}
	}
	if !wt.created && st.SealedHead != "" && head != st.SealedHead {
		return diverged("HEAD changed outside any run")
	}
	if remote == "" || remote == head {
		return nil
	}
	// Local commits ahead of the remote, or a rebase by the agent not yet pushed, are the task's own work.
	if ok, err := ancestor(ctx, wt.Path, head, remote); err != nil || !ok {
		return err
	}
	if _, err = git(ctx, wt.Path, nil, "merge", "--ff-only", "--quiet", remote); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return diverged("the remote branch moved and uncommitted changes conflict with it")
	}
	if st.SealedHead != "" {
		st.SealedHead = remote
		return writeState(wt.Path, st)
	}
	return nil
}

// ancestor reports whether a is an ancestor of b.
func ancestor(ctx context.Context, dir, a, b string) (bool, error) {
	_, err := git(ctx, dir, nil, "merge-base", "--is-ancestor", a, b)
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return err == nil, err
}

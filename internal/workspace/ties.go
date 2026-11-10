package workspace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
)

// node-ties: a task's unsaved work is saved to the service ref refs/factory/wip/<task> of its repository, so a
// task is tied to the node that holds its work only while that save fails.

// MaxWIPFileBytes is the per-file cap of a saved WIP (node-ties, owner 2026-11-10: 50 MB); a larger file keeps
// the work on the node.
var MaxWIPFileBytes int64 = 50 << 20

// SaveWIP pushes the worktree's local work (uncommitted changes and commits on no origin branch, as the seal
// res found them) to refs/factory/wip/<task> on origin, after the size cap and the secret scan. It reports the
// saved commit in res.SavedWIP, or in res.Unsaved why the work stays only on this node. A worktree without
// local work deletes a WIP it saved earlier: that work is on the remote branch now. Called under the run lock,
// after SealExit; a fresh_from_default worktree is never continued elsewhere and saves nothing.
func (w *Worktrees) SaveWIP(ctx context.Context, r Request, res SealResult) SealResult {
	if r.Fresh {
		return res
	}
	dir, err := w.project(r)
	if err != nil {
		res.Unsaved = err.Error()
		return res
	}
	path := filepath.Join(dir, "tasks", r.Task)
	st, err := readState(path)
	if err != nil {
		res.Unsaved = "worktree state: " + err.Error()
		return res
	}
	// After a reset to the exit the worktree is the exit's commit: what the seal put on the WIP ref then is
	// work the exit dropped, not work the next run continues.
	dirty := !res.Reset && res.WIPSHA != ""
	res.LocalWork = res.Unpushed || dirty
	if !res.LocalWork {
		if st.SavedWIP != "" {
			deleteRemoteWIP(ctx, path, r.Task)
			st.SavedWIP = ""
			if err = writeState(path, st); err != nil {
				slog.Warn("worktree state", "task", r.Task, "error", err)
			}
		}
		return res
	}
	save := res.SealedHead
	if dirty {
		save = res.WIPSHA
	}
	// The same files as the WIP already saved (a run that changed nothing): keep that commit, no push.
	if st.SavedWIP != "" {
		a, _ := git(ctx, path, nil, "rev-parse", "--verify", "--quiet", st.SavedWIP+"^{tree}")
		b, _ := git(ctx, path, nil, "rev-parse", "--verify", "--quiet", save+"^{tree}")
		if a != "" && a == b {
			res.SavedWIP = st.SavedWIP
			return res
		}
	}
	if big, err := oversized(ctx, path, save); err != nil {
		res.Unsaved = "size check failed: " + err.Error()
		return res
	} else if big != "" {
		res.Unsaved = big
		return res
	}
	if err = w.Scan(ctx, path, save+" --not --remotes=origin"); err != nil {
		var se *SecretError
		if errors.As(err, &se) {
			res.Unsaved = "secret finding: " + firstLines(se.Report, 3)
		} else {
			res.Unsaved = "secret scan failed: " + err.Error()
		}
		return res
	}
	// Scanned above with the hook's range; --no-verify keeps the scan from running twice.
	if _, err = git(ctx, path, nil, "push", "--no-verify", "--quiet", "--force", "origin", save+":"+WIPRef(r.Task)); err != nil {
		res.Unsaved = "push failed: " + firstLines(err.Error(), 2)
		return res
	}
	res.SavedWIP = save
	st.SavedWIP = save
	if err = writeState(path, st); err != nil {
		slog.Warn("worktree state", "task", r.Task, "error", err)
	}
	return res
}

// oversized names the first file of the work above the remote larger than MaxWIPFileBytes ("" when none). The
// work already follows .gitignore: the seal's snapshot is `git add -A`.
func oversized(ctx context.Context, path, save string) (string, error) {
	all, err := git(ctx, path, nil, "rev-list", "--objects", save, "--not", "--remotes=origin")
	if err != nil {
		return "", err
	}
	big, err := git(ctx, path, nil, "rev-list", "--objects", "--filter=blob:limit="+strconv.FormatInt(MaxWIPFileBytes+1, 10), "--filter-print-omitted", save, "--not", "--remotes=origin")
	if err != nil {
		return "", err
	}
	omitted := map[string]bool{}
	for _, l := range strings.Split(big, "\n") {
		if oid, ok := strings.CutPrefix(l, "~"); ok {
			omitted[strings.TrimSpace(oid)] = true
		}
	}
	if len(omitted) == 0 {
		return "", nil
	}
	for _, l := range strings.Split(all, "\n") {
		oid, name, _ := strings.Cut(l, " ")
		if omitted[oid] {
			return fmt.Sprintf("file %s is larger than %d MB", name, MaxWIPFileBytes>>20), nil
		}
	}
	return fmt.Sprintf("a file is larger than %d MB", MaxWIPFileBytes>>20), nil
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " / ")
}

// deleteRemoteWIP removes the task's service ref on origin; best effort (it may be gone already).
func deleteRemoteWIP(ctx context.Context, path, task string) {
	if _, err := git(ctx, path, nil, "push", "--no-verify", "--quiet", "origin", ":"+WIPRef(task)); err != nil {
		slog.Info("remote WIP not deleted", "task", task, "error", firstLines(err.Error(), 1))
	}
}

// supersede runs before the start check: this worktree's leftovers were saved remotely (SavedWIP), the task ran
// on another node since (r.Moved) and Core continues it from other work (r.WIP differs). They stay on the local
// WIP ref; the worktree goes back to the remote branch (or the default branch when the task branch was never
// pushed), so the start check neither stumbles over them nor continues them. Without Moved (the same node
// continues, or a Core that does not say) nothing changes. Under the run lock.
func (w *Worktrees) supersede(ctx context.Context, r Request, wt Worktree) error {
	if !r.Moved {
		return nil
	}
	st, err := readState(wt.Path)
	if err != nil || st.SavedWIP == "" || st.SavedWIP == r.WIP {
		return err
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
	target, err := remoteBase(ctx, wt, r)
	if err != nil {
		return err
	}
	if _, err = git(ctx, wt.Path, nil, "reset", "--hard", "--quiet", target); err != nil {
		return err
	}
	if _, err = git(ctx, wt.Path, nil, "clean", "-fdq"); err != nil {
		return err
	}
	st.SavedWIP, st.SealedHead, st.ResetFrom = "", target, ""
	return writeState(wt.Path, st)
}

// remoteBase is origin/<branch>, or origin/<default> when the task branch was never pushed.
func remoteBase(ctx context.Context, wt Worktree, r Request) (string, error) {
	if sha, err := git(ctx, wt.Path, nil, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+wt.Branch+"^{commit}"); err == nil && sha != "" {
		return sha, nil
	}
	def := r.DefaultBranch
	if def == "" {
		var err error
		if def, err = git(ctx, wt.Repo, nil, "symbolic-ref", "--short", "HEAD"); err != nil {
			return "", err
		}
	}
	return git(ctx, wt.Path, nil, "rev-parse", "--verify", "refs/remotes/origin/"+def+"^{commit}")
}

// RestoreError: the saved WIP Core named cannot be fetched; the run does not start without its work.
type RestoreError struct {
	WIP string
	Err error
}

func (e *RestoreError) Error() string {
	return fmt.Sprintf("workspace: saved work %s of the task could not be restored from %s: %v", short(e.WIP), "refs/factory/wip", e.Err)
}
func (e *RestoreError) Unwrap() error { return e.Err }

// restoreWIP runs after the start check: when Core names a saved WIP (r.WIP) this worktree does not hold, the
// WIP commit is fetched from the service ref and its files are written over the worktree (files, not commits,
// like the local seal): HEAD and the index stay at the remote branch, the work shows as uncommitted changes.
func (w *Worktrees) restoreWIP(ctx context.Context, r Request, wt Worktree) error {
	if r.WIP == "" {
		return nil
	}
	st, err := readState(wt.Path)
	if err != nil || st.SavedWIP == r.WIP {
		return err
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
	local := "refs/factory/restore/" + r.Task
	if _, err = git(ctx, wt.Repo, nil, "fetch", "--quiet", "--no-tags", "origin", "+"+WIPRef(r.Task)+":"+local); err != nil {
		if _, e := git(ctx, wt.Repo, nil, "cat-file", "-e", r.WIP+"^{commit}"); e != nil {
			return &RestoreError{WIP: r.WIP, Err: err}
		}
	}
	if _, err = git(ctx, wt.Repo, nil, "cat-file", "-e", r.WIP+"^{commit}"); err != nil {
		return &RestoreError{WIP: r.WIP, Err: fmt.Errorf("the service ref holds other work")}
	}
	if _, err = git(ctx, wt.Path, nil, "restore", "--source="+r.WIP, "--worktree", "--", "."); err != nil {
		return &RestoreError{WIP: r.WIP, Err: err}
	}
	_, _ = git(ctx, wt.Repo, nil, "update-ref", "-d", local)
	st.SavedWIP = r.WIP
	return writeState(wt.Path, st)
}

package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var ErrWorktreesDisabled = errors.New("workspace: worktrees allocator is disabled")

var DefaultScanCommand = []string{"gitleaks", "git", "--redact", "--no-banner", "--exit-code", "1", "--log-opts"}

// WorktreesConfig is off unless Enabled is set; the worker does not use it yet.
type WorktreesConfig struct {
	Enabled bool
	Root    string
	// ScanCommand receives a git revision range (may contain spaces) as its last argument and exits 1 on a finding.
	ScanCommand []string
}

type Worktrees struct {
	root string
	scan []string
}

func NewWorktrees(cfg WorktreesConfig) (*Worktrees, error) {
	if !cfg.Enabled {
		return nil, ErrWorktreesDisabled
	}
	if cfg.Root == "" {
		return nil, errors.New("workspace: worktrees root is required")
	}
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		return nil, err
	}
	scan := cfg.ScanCommand
	if len(scan) == 0 {
		scan = DefaultScanCommand
	}
	return &Worktrees{root: root, scan: scan}, nil
}

type Request struct {
	Company, Project, Task string
	RepoURL, DefaultBranch string
	Slug, Branch           string
}

type Worktree struct {
	Path, Branch, Scratch, Repo string
	Release                     func()
}

type SealResult struct {
	SealedHead string    `json:"sealed_head"`
	WIPSHA     string    `json:"wip_sha,omitempty"`
	SealedAt   time.Time `json:"sealed_at"`
}

type worktreeState struct {
	Task   string `json:"task"`
	Branch string `json:"branch"`
	SealResult
}

type SecretError struct{ Report string }

func (e *SecretError) Error() string { return "workspace: secret scan finding:\n" + e.Report }

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func (w *Worktrees) project(r Request) (string, error) {
	for _, id := range []string{r.Company, r.Project, r.Task} {
		if !safeID.MatchString(id) || strings.Contains(id, "..") {
			return "", fmt.Errorf("workspace: invalid id %q", id)
		}
	}
	return filepath.Join(w.root, r.Company, r.Project), nil
}

func WIPRef(task string) string { return "refs/factory/wip/" + task }

var slugBad = regexp.MustCompile(`[^a-z0-9]+`)

func branchName(r Request) string {
	if r.Branch != "" {
		return r.Branch
	}
	s := strings.Trim(slugBad.ReplaceAllString(strings.ToLower(r.Slug), "-"), "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	if s == "" {
		return "af/" + r.Task
	}
	return "af/" + r.Task + "-" + s
}

// Acquire ensures the task worktree and takes its run lock, waiting for a previous holder.
func (w *Worktrees) Acquire(ctx context.Context, r Request) (Worktree, error) {
	wt, err := w.Ensure(ctx, r)
	if err != nil {
		return Worktree{}, err
	}
	unlock, err := flockWait(ctx, filepath.Join(wt.Path, ".factory", "lock"))
	if err != nil {
		return Worktree{}, err
	}
	wt.Release = unlock
	return wt, nil
}

// Ensure creates or repairs the clone, branch and worktree of a task; it is safe to re-run after a crash at any point.
func (w *Worktrees) Ensure(ctx context.Context, r Request) (Worktree, error) {
	dir, err := w.project(r)
	if err != nil {
		return Worktree{}, err
	}
	if r.RepoURL == "" {
		return Worktree{}, errors.New("workspace: repo url is required")
	}
	branch := branchName(r)
	if _, err = git(ctx, "", nil, "check-ref-format", "--branch", branch); err != nil {
		return Worktree{}, fmt.Errorf("workspace: invalid branch %q", branch)
	}
	unlock, err := lockRepo(ctx, filepath.Join(dir, "repo.lock"))
	if err != nil {
		return Worktree{}, err
	}
	defer unlock()
	repo := filepath.Join(dir, "repo")
	if err = w.ensureClone(ctx, dir, repo, r.RepoURL); err != nil {
		return Worktree{}, err
	}
	if _, err = git(ctx, repo, nil, "fetch", "--prune", "origin"); err != nil {
		return Worktree{}, err
	}
	def := r.DefaultBranch
	if def == "" {
		if def, err = git(ctx, repo, nil, "symbolic-ref", "--short", "HEAD"); err != nil {
			return Worktree{}, err
		}
	}
	if err = ensureBranch(ctx, repo, branch, def); err != nil {
		return Worktree{}, err
	}
	path := filepath.Join(dir, "tasks", r.Task)
	if err = ensureWorktree(ctx, repo, path, branch); err != nil {
		return Worktree{}, err
	}
	hooks, err := w.writeHooks(dir)
	if err != nil {
		return Worktree{}, err
	}
	if _, err = git(ctx, path, nil, "config", "--worktree", "core.hooksPath", hooks); err != nil {
		return Worktree{}, err
	}
	scratch := filepath.Join(dir, "scratch", r.Task)
	if err = os.MkdirAll(scratch, 0o755); err != nil {
		return Worktree{}, err
	}
	st := filepath.Join(path, ".factory", "state")
	if _, err = os.Stat(st); errors.Is(err, os.ErrNotExist) {
		if err = writeState(path, worktreeState{Task: r.Task, Branch: branch}); err != nil {
			return Worktree{}, err
		}
	} else if err != nil {
		return Worktree{}, err
	}
	return Worktree{Path: path, Branch: branch, Scratch: scratch, Repo: repo, Release: func() {}}, nil
}

func (w *Worktrees) ensureClone(ctx context.Context, dir, repo, url string) error {
	if _, err := os.Stat(repo); err == nil {
		if out, err := git(ctx, repo, nil, "rev-parse", "--is-bare-repository"); err == nil && out == "true" {
			return configureRepo(ctx, repo)
		}
		if err = os.RemoveAll(repo); err != nil {
			return err
		}
	}
	tmp := repo + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if _, err := git(ctx, dir, nil, "clone", "--bare", "--quiet", url, tmp); err != nil {
		return err
	}
	if err := configureRepo(ctx, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, repo)
}

func configureRepo(ctx context.Context, repo string) error {
	for _, kv := range [][2]string{
		{"remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"},
		{"extensions.worktreeConfig", "true"},
	} {
		if _, err := git(ctx, repo, nil, "config", kv[0], kv[1]); err != nil {
			return err
		}
	}
	// With worktreeConfig, core.bare must live in the main config.worktree or linked worktrees see bare=true.
	if _, err := git(ctx, repo, nil, "config", "--file", filepath.Join(repo, "config.worktree"), "core.bare", "true"); err != nil {
		return err
	}
	if _, err := git(ctx, repo, nil, "config", "--unset-all", "core.bare"); err != nil {
		var ge *gitError
		var ee *exec.ExitError
		if !errors.As(err, &ge) || !errors.As(ge.err, &ee) || ee.ExitCode() != 5 {
			return err
		}
	}
	exclude := filepath.Join(repo, "info", "exclude")
	b, err := os.ReadFile(exclude)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == "/.factory/" {
			return nil
		}
	}
	if err = os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		b = append(b, '\n')
	}
	return os.WriteFile(exclude, append(b, "/.factory/\n"...), 0o644)
}

func ensureBranch(ctx context.Context, repo, branch, def string) error {
	if _, err := git(ctx, repo, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		return nil
	}
	start := "refs/remotes/origin/" + def
	if _, err := git(ctx, repo, nil, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch); err == nil {
		start = "refs/remotes/origin/" + branch
	}
	if _, err := git(ctx, repo, nil, "branch", "--no-track", branch, start); err != nil {
		return err
	}
	if _, err := git(ctx, repo, nil, "config", "branch."+branch+".remote", "origin"); err != nil {
		return err
	}
	_, err := git(ctx, repo, nil, "config", "branch."+branch+".merge", "refs/heads/"+branch)
	return err
}

// ensureWorktree keeps a worktree whose .factory/state exists; anything else at the path is a half-created worktree and is rebuilt.
func ensureWorktree(ctx context.Context, repo, path, branch string) error {
	if _, err := os.Stat(filepath.Join(path, ".factory", "state")); err == nil {
		top, err := git(ctx, path, nil, "rev-parse", "--show-toplevel")
		if err == nil && top == path {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("workspace: worktree %s has state but is not a valid worktree; refusing to discard it", path)
	}
	if _, err := os.Stat(path); err == nil {
		_, _ = git(ctx, repo, nil, "worktree", "remove", "--force", "--force", path)
		if err = os.RemoveAll(path); err != nil {
			return err
		}
	}
	if _, err := git(ctx, repo, nil, "worktree", "prune"); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := git(ctx, repo, nil, "worktree", "add", "--quiet", path, branch)
	return err
}

func (w *Worktrees) writeHooks(dir string) (string, error) {
	hooks := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		return "", err
	}
	q := make([]string, len(w.scan))
	for i, a := range w.scan {
		q[i] = shellQuote(a)
	}
	script := `#!/bin/sh
# Generated by App Factory: secret scan of commits not yet on the remote.
status=0
while read lref lsha rref rsha; do
	case "$lsha" in *[!0]*) ;; *) continue ;; esac
	` + strings.Join(q, " ") + ` "$lsha --not --remotes=origin" >&2 || status=1
done
[ $status -eq 0 ] || echo "factory: push refused by secret scan" >&2
exit $status
`
	p := filepath.Join(hooks, "pre-push")
	if b, err := os.ReadFile(p); err == nil && string(b) == script {
		return hooks, nil
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(script), 0o755); err != nil {
		return "", err
	}
	return hooks, os.Rename(tmp, p)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Seal snapshots the worktree's working tree onto the local WIP ref via a temporary index; HEAD, the index and files are untouched.
func (w *Worktrees) Seal(ctx context.Context, r Request) (SealResult, error) {
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
	head, err := git(ctx, path, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return SealResult{}, err
	}
	gitDir, err := git(ctx, path, nil, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return SealResult{}, err
	}
	tmp, err := os.CreateTemp(filepath.Join(path, ".factory"), "seal-index-")
	if err != nil {
		return SealResult{}, err
	}
	idx := tmp.Name()
	defer os.Remove(idx)
	if b, err := os.ReadFile(filepath.Join(gitDir, "index")); err == nil {
		_, err = tmp.Write(b)
		if err != nil {
			tmp.Close()
			return SealResult{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		tmp.Close()
		return SealResult{}, err
	}
	if err = tmp.Close(); err != nil {
		return SealResult{}, err
	}
	env := []string{"GIT_INDEX_FILE=" + idx,
		"GIT_AUTHOR_NAME=App Factory", "GIT_AUTHOR_EMAIL=factory@localhost",
		"GIT_COMMITTER_NAME=App Factory", "GIT_COMMITTER_EMAIL=factory@localhost"}
	if _, err = git(ctx, path, env, "add", "-A", "--", "."); err != nil {
		return SealResult{}, err
	}
	tree, err := git(ctx, path, env, "write-tree")
	if err != nil {
		return SealResult{}, err
	}
	headTree, err := git(ctx, path, nil, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return SealResult{}, err
	}
	res := SealResult{SealedHead: head, SealedAt: time.Now().UTC()}
	if tree != headTree {
		if res.WIPSHA, err = git(ctx, path, env, "commit-tree", tree, "-p", head, "-m", "factory wip "+r.Task); err != nil {
			return SealResult{}, err
		}
		if _, err = git(ctx, path, nil, "update-ref", "-m", "factory seal", WIPRef(r.Task), res.WIPSHA); err != nil {
			return SealResult{}, err
		}
	}
	st, _ := readState(path)
	st.Task, st.SealResult = r.Task, res
	return res, writeState(path, st)
}

// ScanExit scans merge-base(origin/<default>, HEAD)..HEAD with the configured scanner.
func (w *Worktrees) ScanExit(ctx context.Context, r Request) error {
	dir, err := w.project(r)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "tasks", r.Task)
	def := r.DefaultBranch
	if def == "" {
		if def, err = git(ctx, filepath.Join(dir, "repo"), nil, "symbolic-ref", "--short", "HEAD"); err != nil {
			return err
		}
	}
	base, err := git(ctx, path, nil, "merge-base", "refs/remotes/origin/"+def, "HEAD")
	if err != nil {
		return err
	}
	return w.Scan(ctx, path, base+"..HEAD")
}

func (w *Worktrees) Scan(ctx context.Context, path, revRange string) error {
	cmd := exec.CommandContext(ctx, w.scan[0], append(w.scan[1:len(w.scan):len(w.scan)], revRange)...)
	cmd.Dir = path
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 && ctx.Err() == nil {
		return &SecretError{Report: strings.TrimSpace(string(out))}
	}
	return fmt.Errorf("workspace: secret scanner: %w", err)
}

func readState(path string) (worktreeState, error) {
	var st worktreeState
	b, err := os.ReadFile(filepath.Join(path, ".factory", "state"))
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(b, &st)
}

func writeState(path string, st worktreeState) error {
	dir := filepath.Join(path, ".factory")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "state.tmp")
	if err = os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "state"))
}

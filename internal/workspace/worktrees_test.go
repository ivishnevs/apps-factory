package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	w      *Worktrees
	root   string
	remote string
	req    Request
}

func setup(t *testing.T, scan ...string) fixture {
	t.Helper()
	base := t.TempDir()
	base, _ = filepath.EvalSymlinks(base)
	gc := filepath.Join(base, "gitconfig")
	write(t, gc, "[user]\n\tname = Test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote := filepath.Join(base, "remote.git")
	run(t, base, "init", "--bare", "-q", remote)
	seed := filepath.Join(base, "seed")
	run(t, base, "init", "-q", seed)
	write(t, filepath.Join(seed, "a.txt"), "a\n")
	write(t, filepath.Join(seed, "b.txt"), "b\n")
	run(t, seed, "add", "-A")
	run(t, seed, "commit", "-qm", "init")
	run(t, seed, "push", "-q", remote, "main")
	if len(scan) == 0 {
		scan = []string{"true"}
	}
	w, err := NewWorktrees(WorktreesConfig{Enabled: true, Root: filepath.Join(base, "root"), ScanCommand: scan})
	if err != nil {
		t.Fatal(err)
	}
	return fixture{w: w, root: filepath.Join(base, "root"), remote: remote,
		req: Request{Company: "acme", Project: "app", RepoURL: remote, DefaultBranch: "main", Task: "t1", Slug: "Add login!"}}
}

func (f fixture) task(id string) Request { r := f.req; r.Task, r.Slug = id, ""; return r }

func TestWorktreesDisabledByDefault(t *testing.T) {
	if _, err := NewWorktrees(WorktreesConfig{Root: t.TempDir()}); !errors.Is(err, ErrWorktreesDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureWorktree(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	wt, err := f.w.Acquire(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	if wt.Branch != "af/t1-add-login" || wt.Path != filepath.Join(f.root, "acme", "app", "tasks", "t1") {
		t.Fatalf("worktree = %+v", wt)
	}
	if got, want := run(t, wt.Path, "rev-parse", "HEAD"), run(t, f.remote, "rev-parse", "main"); got != want {
		t.Fatalf("HEAD %s, want %s", got, want)
	}
	if got := run(t, wt.Path, "symbolic-ref", "--short", "HEAD"); got != wt.Branch {
		t.Fatalf("branch %s", got)
	}
	if s := run(t, wt.Path, "status", "--porcelain", "--untracked-files=all"); s != "" {
		t.Fatalf(".factory not excluded: %q", s)
	}
	if strings.HasPrefix(wt.Scratch, wt.Path) {
		t.Fatalf("scratch %s inside worktree", wt.Scratch)
	}
	if _, err = os.Stat(wt.Scratch); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err = f.w.Acquire(ctx2, f.req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second run acquired a locked worktree: %v", err)
	}
	write(t, filepath.Join(wt.Path, "work.txt"), "keep\n")
	wt.Release()
	again, err := f.w.Acquire(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Release()
	if again.Path != wt.Path || again.Branch != wt.Branch {
		t.Fatalf("re-ensure changed worktree: %+v", again)
	}
	if b, err := os.ReadFile(filepath.Join(wt.Path, "work.txt")); err != nil || string(b) != "keep\n" {
		t.Fatalf("re-ensure lost work: %q %v", b, err)
	}
}

func TestEnsureRecoversAfterCrash(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	first, err := f.w.Ensure(ctx, f.task("t0"))
	if err != nil {
		t.Fatal(err)
	}
	repo, tasks := first.Repo, filepath.Dir(first.Path)
	cases := map[string]func(){
		"branch without worktree": func() { run(t, repo, "branch", "af/t1", "refs/remotes/origin/main") },
		"worktree without state": func() {
			run(t, repo, "worktree", "add", "-q", "-b", "af/t2", filepath.Join(tasks, "t2"), "refs/remotes/origin/main")
			os.Remove(filepath.Join(tasks, "t2", "a.txt"))
		},
		"registered worktree deleted": func() {
			if _, err := f.w.Ensure(ctx, f.task("t3")); err != nil {
				t.Fatal(err)
			}
			os.RemoveAll(filepath.Join(tasks, "t3"))
		},
		"junk directory": func() { write(t, filepath.Join(tasks, "t4", "junk"), "x") },
		"stale lock file": func() {
			go func() {
				lock := filepath.Join(repo, "config.lock")
				write(t, lock, "")
				time.Sleep(300 * time.Millisecond)
				os.Remove(lock)
			}()
			time.Sleep(50 * time.Millisecond)
		},
	}
	ids := map[string]string{"branch without worktree": "t1", "worktree without state": "t2", "registered worktree deleted": "t3", "junk directory": "t4", "stale lock file": "t5"}
	for name, crash := range cases {
		t.Run(name, func(t *testing.T) {
			crash()
			r := f.task(ids[name])
			wt, err := f.w.Ensure(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(filepath.Join(wt.Path, "a.txt")); err != nil {
				t.Fatal(err)
			}
			if s := run(t, wt.Path, "status", "--porcelain", "--untracked-files=all"); s != "" {
				t.Fatalf("dirty after recovery: %q", s)
			}
			if _, err = f.w.Ensure(ctx, r); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEnsureRecoversHalfClone(t *testing.T) {
	f := setup(t)
	dir := filepath.Join(f.root, "acme", "app")
	write(t, filepath.Join(dir, "repo", "HEAD"), "garbage")
	write(t, filepath.Join(dir, "repo.tmp", "objects", "x"), "garbage")
	wt, err := f.w.Ensure(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	if run(t, wt.Repo, "rev-parse", "--is-bare-repository") != "true" {
		t.Fatal("repo is not bare")
	}
	if _, err = os.Stat(filepath.Join(dir, "repo.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repo.tmp left behind: %v", err)
	}
}

func TestConcurrentEnsure(t *testing.T) {
	f := setup(t)
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.w.Ensure(context.Background(), f.task(fmt.Sprintf("c%d", i)))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("ensure c%d: %v", i, err)
		}
	}
	list := run(t, filepath.Join(f.root, "acme", "app", "repo"), "worktree", "list", "--porcelain")
	for i := range errs {
		if !strings.Contains(list, fmt.Sprintf("branch refs/heads/af/c%d\n", i)) && !strings.HasSuffix(list, fmt.Sprintf("refs/heads/af/c%d", i)) {
			t.Fatalf("missing worktree c%d in\n%s", i, list)
		}
	}
}

func TestGitOpsSerializedPerRepo(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if _, err := f.w.Ensure(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(f.root, "acme", "app", "repo.lock")
	unlock, err := flockWait(ctx, lock)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	go func() { _, err := f.w.Ensure(ctx, f.task("t2")); done <- err }()
	go func() { _, err := f.w.Seal(ctx, f.req); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("git op ran while another process held the repo lock: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	active, peak := 0, 0
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := lockRepo(ctx, lock)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			active++
			peak = max(peak, active)
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			release()
		}()
	}
	wg.Wait()
	if peak != 1 {
		t.Fatalf("peak concurrent holders %d", peak)
	}
	other, err := lockRepo(ctx, filepath.Join(f.root, "acme", "other", "repo.lock"))
	if err != nil {
		t.Fatal(err)
	}
	other()
}

func TestSealKeepsHeadAndIndex(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	wt, err := f.w.Ensure(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	p := wt.Path
	write(t, filepath.Join(p, "a.txt"), "staged\n")
	run(t, p, "add", "a.txt")
	write(t, filepath.Join(p, "b.txt"), "unstaged\n")
	write(t, filepath.Join(p, "new", "c.txt"), "untracked\n")
	gitDir := run(t, p, "rev-parse", "--absolute-git-dir")
	snapshot := func() string {
		idx, err := os.ReadFile(filepath.Join(gitDir, "index"))
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("%s|%x|%s|%s", run(t, p, "rev-parse", "HEAD"), sha256.Sum256(idx),
			run(t, p, "symbolic-ref", "HEAD"), run(t, p, "status", "--porcelain", "--untracked-files=all"))
	}
	before := snapshot()
	head := run(t, p, "rev-parse", "HEAD")
	res, err := f.w.Seal(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("seal changed worktree:\n%s\n%s", before, after)
	}
	for file, want := range map[string]string{"a.txt": "staged\n", "b.txt": "unstaged\n", "new/c.txt": "untracked\n"} {
		if b, _ := os.ReadFile(filepath.Join(p, file)); string(b) != want {
			t.Fatalf("%s = %q", file, b)
		}
		if got := run(t, p, "show", WIPRef("t1")+":"+file); got+"\n" != want {
			t.Fatalf("wip %s = %q", file, got)
		}
	}
	if res.SealedHead != head || res.WIPSHA != run(t, p, "rev-parse", WIPRef("t1")) || run(t, p, "rev-parse", WIPRef("t1")+"^") != head {
		t.Fatalf("seal result %+v", res)
	}
	if strings.Contains(run(t, p, "ls-tree", "-r", "--name-only", WIPRef("t1")), ".factory") {
		t.Fatal(".factory sealed into wip")
	}
	st, err := readState(p)
	if err != nil || st.SealedHead != head || st.WIPSHA != res.WIPSHA || st.Branch != wt.Branch {
		t.Fatalf("state %+v %v", st, err)
	}
	if strings.Contains(run(t, f.remote, "for-each-ref"), "factory") {
		t.Fatal("wip ref reached the remote")
	}
	run(t, p, "add", "-A")
	run(t, p, "commit", "-qm", "work")
	res, err = f.w.Seal(ctx, f.req)
	if err != nil || res.WIPSHA != "" || res.SealedHead != run(t, p, "rev-parse", "HEAD") {
		t.Fatalf("clean seal %+v %v", res, err)
	}
}

const secret = "AKIA" + "ABCDEFGHIJKLMNOP"

func TestSecretScanBlocksPush(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "scans")
	scanner := filepath.Join(dir, "scan")
	write(t, scanner, "#!/bin/sh\necho \"$1\" >> "+log+"\nif git log -p $1 | grep -qE 'AKIA[0-9A-Z]{16}'; then echo 'finding: aws key AKIA****'; exit 1; fi\n")
	os.Chmod(scanner, 0o755)
	f := setup(t, scanner)
	ctx := context.Background()
	wt, err := f.w.Ensure(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	p := wt.Path
	write(t, filepath.Join(p, "ok.txt"), "fine\n")
	run(t, p, "add", "-A")
	run(t, p, "commit", "-qm", "ok")
	if err = f.w.ScanExit(ctx, f.req); err != nil {
		t.Fatal(err)
	}
	run(t, p, "push", "-q", "-u", "origin", "HEAD")
	if b, _ := os.ReadFile(log); !strings.Contains(string(b), "--not --remotes=origin") {
		t.Fatalf("pre-push hook did not run the scanner: %q", b)
	}
	pushed := run(t, f.remote, "rev-parse", wt.Branch)

	write(t, filepath.Join(p, "config.env"), "AWS_KEY="+secret+"\n")
	run(t, p, "add", "-A")
	run(t, p, "commit", "-qm", "leak")
	cmd := exec.Command("git", "push", "origin", "HEAD")
	cmd.Dir = p
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "secret scan") {
		t.Fatalf("push with secret was not refused: %v\n%s", err, out)
	}
	if got := run(t, f.remote, "rev-parse", wt.Branch); got != pushed {
		t.Fatal("secret reached the remote")
	}
	err = f.w.ScanExit(ctx, f.req)
	var se *SecretError
	if !errors.As(err, &se) || strings.Contains(se.Report, secret) {
		t.Fatalf("exit scan = %v", err)
	}
}

func TestMissingScannerBlocksPush(t *testing.T) {
	f := setup(t, filepath.Join(t.TempDir(), "no-such-scanner"))
	ctx := context.Background()
	wt, err := f.w.Ensure(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(wt.Path, "x.txt"), "x\n")
	run(t, wt.Path, "add", "-A")
	run(t, wt.Path, "commit", "-qm", "x")
	cmd := exec.Command("git", "push", "origin", "HEAD")
	cmd.Dir = wt.Path
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("push succeeded without a scanner:\n%s", out)
	}
	var se *SecretError
	if err = f.w.ScanExit(ctx, f.req); err == nil || errors.As(err, &se) {
		t.Fatalf("exit scan without scanner = %v", err)
	}
}

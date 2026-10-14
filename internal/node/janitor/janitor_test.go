package janitor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"appfactory/internal/configfile"
	"appfactory/internal/workspace"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
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

// trees is a worktrees allocator over a local bare remote with one commit on main.
func trees(t *testing.T) (*workspace.Worktrees, workspace.Request) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	gc := filepath.Join(base, "gitconfig")
	write(t, gc, "[user]\n\tname = Test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote, seed := filepath.Join(base, "remote.git"), filepath.Join(base, "seed")
	git(t, base, "init", "--bare", "-q", remote)
	git(t, base, "init", "-q", seed)
	write(t, filepath.Join(seed, "a.txt"), "a\n")
	git(t, seed, "add", "-A")
	git(t, seed, "commit", "-qm", "init")
	git(t, seed, "push", "-q", remote, "main")
	w, err := workspace.NewWorktrees(workspace.WorktreesConfig{Enabled: true, Root: filepath.Join(base, "root"), ScanCommand: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	return w, workspace.Request{Company: "default", Project: "app", RepoURL: remote, DefaultBranch: "main"}
}

// A closed task's hook removes its fake simulator and its worktree after a seal; an open task keeps both.
func TestCleanupClosedTaskOnly(t *testing.T) {
	w, base := trees(t)
	ctx := context.Background()
	sims := t.TempDir()
	log := filepath.Join(t.TempDir(), "hooks.log")
	paths := map[string]string{}
	for _, task := range []string{"t1", "t2", "t3"} {
		r := base
		r.Task = task
		wt, err := w.Acquire(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(wt.Path, "wip.txt"), task+"\n")
		if _, err = w.Seal(ctx, r); err != nil {
			t.Fatal(err)
		}
		if task != "t3" {
			_ = wt.Release()
		} else {
			defer wt.Release() // t3 is still running
		}
		paths[task] = wt.Path
		write(t, filepath.Join(sims, task, "device.plist"), "sim")
	}
	j := &Janitor{Worktrees: w, Company: "default", LockWait: 50 * time.Millisecond, Config: Config{
		OnTaskClosed: []string{`rm -rf "` + sims + `/$FACTORY_TASK_ID"`, `test -f "$FACTORY_WORKTREE/wip.txt" && echo "$FACTORY_TASK_ID" >> ` + log},
	}}
	done := j.Cleanup(ctx, []string{"t1", "t3", "t9"})
	if !slices.Equal(done, []string{"t1", "t9"}) {
		t.Fatalf("done %v", done)
	}
	for task, gone := range map[string]bool{"t1": true, "t2": false, "t3": false} {
		_, err := os.Stat(filepath.Join(sims, task))
		if gone != errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s simulator gone=%v: %v", task, gone, err)
		}
		_, err = os.Stat(paths[task])
		if gone != errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s worktree gone=%v: %v", task, gone, err)
		}
	}
	repo := filepath.Join(filepath.Dir(filepath.Dir(paths["t1"])), "repo")
	if git(t, repo, "show", workspace.WIPRef("t1")+":wip.txt") != "t1" {
		t.Fatal("WIP ref of the removed worktree lost")
	}
	// Repeated lists are idempotent; hooks ran once, with the worktree still present.
	if done = j.Cleanup(ctx, []string{"t1"}); !slices.Equal(done, []string{"t1"}) {
		t.Fatal(done)
	}
	if b, _ := os.ReadFile(log); string(b) != "t1\n" {
		t.Fatalf("hook log %q", b)
	}
}

func TestRunStoppedAndPrune(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	j := &Janitor{Config: Config{
		OnRunStop:   []string{`echo "stop $FACTORY_RUN_ID $FACTORY_TASK_ID $FACTORY_WORKTREE" >> ` + out, "exit 3", "sleep 5"},
		Prune:       []Prune{{Command: `echo "prune $FACTORY_OLDER_THAN_MINUTES" >> ` + out, OlderThan: 72 * time.Hour}},
		HookTimeout: 200 * time.Millisecond,
	}}
	start := time.Now()
	j.RunStopped(context.Background(), "r1", "t1", "/wt")
	if time.Since(start) > 3*time.Second {
		t.Fatal("hook timeout not applied")
	}
	j.Prune(context.Background(), false)
	j.Prune(context.Background(), true)
	if b, _ := os.ReadFile(out); string(b) != "stop r1 t1 /wt\nprune 4320\nprune 0\n" {
		t.Fatalf("%q", b)
	}
}

// Run prunes aggressively as soon as the disk falls below the low watermark, otherwise every interval.
func TestRunPrunesOnLowDisk(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	var low atomic.Bool
	j := &Janitor{Tick: 10 * time.Millisecond, LowDisk: low.Load, Config: Config{Interval: time.Hour,
		Prune: []Prune{{Command: `echo "$FACTORY_OLDER_THAN_MINUTES" >> ` + out, OlderThan: time.Hour}}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go j.Run(ctx)
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pruned before the interval")
	}
	low.Store(true)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, _ := os.ReadFile(out); string(b) == "0\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no aggressive prune")
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	if b, _ := os.ReadFile(out); string(b) != "0\n" {
		t.Fatalf("pruned again while still low: %q", b)
	}
}

func TestConfig(t *testing.T) {
	var c Config
	err := configfile.Decode([]byte("interval: 15m\nhook_timeout: 30s\non_run_stop: [\"xcrun simctl delete $FACTORY_TASK_ID\"]\nprune:\n  - {command: \"find ~/Library/Developer/Xcode/DerivedData -mindepth 1 -maxdepth 1 -mmin +$FACTORY_OLDER_THAN_MINUTES -exec rm -rf {} +\", older_than: 72h}\n"), &c)
	if err != nil || c.Interval != 15*time.Minute || c.HookTimeout != 30*time.Second || c.Prune[0].OlderThan != 72*time.Hour || c.Validate() != nil {
		t.Fatalf("%+v %v", c, err)
	}
	for _, bad := range []Config{{OnTaskClosed: []string{""}}, {Prune: []Prune{{OlderThan: time.Hour}}}, {Interval: -time.Second}} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

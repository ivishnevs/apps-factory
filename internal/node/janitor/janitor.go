// Package janitor cleans up after runs and closed tasks on a node (node-agent D4): on_run_stop hooks after
// every run, on_task_closed hooks and worktree removal (after a successful seal) for the tasks Core lists
// under cleanup, and periodic prune commands, run with older_than = 0 below the low disk watermark.
package janitor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"appfactory/internal/workspace"
)

// Config is node.yaml janitor. Hooks are trusted node configuration, run with /bin/sh -c.
type Config struct {
	// Interval between prune passes; default 10m.
	Interval time.Duration `yaml:"interval"`
	// HookTimeout bounds one hook or prune command; default 5m.
	HookTimeout time.Duration `yaml:"hook_timeout"`
	// OnRunStop runs after every run with FACTORY_RUN_ID, FACTORY_TASK_ID and FACTORY_WORKTREE.
	OnRunStop []string `yaml:"on_run_stop"`
	// OnTaskClosed runs once per closed task before its worktree is removed, with FACTORY_TASK_ID and
	// FACTORY_WORKTREE (empty when the task has no worktree on this node).
	OnTaskClosed []string `yaml:"on_task_closed"`
	Prune        []Prune  `yaml:"prune"`
}

// Prune is one cache cleanup command; it receives FACTORY_OLDER_THAN_MINUTES (0 below the low watermark).
type Prune struct {
	Command   string        `yaml:"command"`
	OlderThan time.Duration `yaml:"older_than"`
}

func (c Config) Validate() error {
	var errs []error
	if c.Interval < 0 || c.HookTimeout < 0 {
		errs = append(errs, errors.New("janitor: interval and hook_timeout must not be negative"))
	}
	for _, h := range append(append([]string{}, c.OnRunStop...), c.OnTaskClosed...) {
		if h == "" {
			errs = append(errs, errors.New("janitor: empty hook command"))
		}
	}
	for _, p := range c.Prune {
		if p.Command == "" || p.OlderThan < 0 {
			errs = append(errs, fmt.Errorf("janitor: prune %q: command required, older_than not negative", p.Command))
		}
	}
	return errors.Join(errs...)
}

type Janitor struct {
	Config Config
	// Worktrees removes closed tasks' worktrees; nil skips removal.
	Worktrees *workspace.Worktrees
	Company   string
	// LowDisk reports free space below the low watermark; nil is never.
	LowDisk func() bool
	Log     *slog.Logger
	// LockWait bounds waiting for a worktree held by a run; default 1 s (retried on the next cleanup).
	LockWait time.Duration
	// Tick is the disk check period between passes; default 1m.
	Tick time.Duration

	mu     sync.Mutex // one cleanup or prune pass at a time
	closed map[string]time.Time
}

func (j *Janitor) log() *slog.Logger {
	if j.Log == nil {
		return slog.Default()
	}
	return j.Log
}

// RunStopped runs the on_run_stop hooks for a run that has ended.
func (j *Janitor) RunStopped(ctx context.Context, run, task, worktree string) {
	for _, h := range j.Config.OnRunStop {
		j.hook(ctx, "on_run_stop", h, "FACTORY_RUN_ID="+run, "FACTORY_TASK_ID="+task, "FACTORY_WORKTREE="+worktree)
	}
}

// Cleanup closes tasks Core listed as closed: on_task_closed hooks, then worktree removal after a seal.
// It returns the tasks that are done on this node (no worktree left); a busy or unsealable worktree stays
// and is retried with the next list. Hooks of a task run once per node process.
func (j *Janitor) Cleanup(ctx context.Context, tasks []string) []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed == nil {
		j.closed = map[string]time.Time{}
	}
	for t, at := range j.closed {
		if time.Since(at) > 24*time.Hour {
			delete(j.closed, t)
		}
	}
	var done []string
	for _, task := range tasks {
		if ctx.Err() != nil {
			break
		}
		if j.closeTask(ctx, task) {
			done = append(done, task)
		}
	}
	return done
}

func (j *Janitor) closeTask(ctx context.Context, task string) bool {
	hooks := func(path string) {
		if _, ok := j.closed[task]; ok {
			return
		}
		for _, h := range j.Config.OnTaskClosed {
			j.hook(ctx, "on_task_closed", h, "FACTORY_TASK_ID="+task, "FACTORY_WORKTREE="+path)
		}
		j.closed[task] = time.Now()
	}
	var found []workspace.Request
	if j.Worktrees != nil {
		var err error
		if found, err = j.Worktrees.Find(j.Company, task); err != nil {
			j.log().Error("janitor: find worktree", "task", task, "error", err)
			return false
		}
	}
	if len(found) == 0 {
		hooks("")
		return true
	}
	wait := j.LockWait
	if wait <= 0 {
		wait = time.Second
	}
	ok := true
	for _, r := range found {
		res, err := j.Worktrees.Remove(ctx, r, wait, hooks)
		if err != nil {
			ok = false
			j.log().Warn("janitor: worktree kept", "task", task, "project", r.Project, "error", err)
			continue
		}
		j.log().Info("janitor: worktree removed", "task", task, "project", r.Project, "sealed_head", res.SealedHead, "wip_sha", res.WIPSHA)
	}
	return ok
}

// Prune runs the prune commands; aggressive (below the low watermark) passes older_than = 0.
func (j *Janitor) Prune(ctx context.Context, aggressive bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, p := range j.Config.Prune {
		older := p.OlderThan
		if aggressive {
			older = 0
		}
		j.hook(ctx, "prune", p.Command, "FACTORY_OLDER_THAN_MINUTES="+strconv.Itoa(int(older/time.Minute)))
	}
}

// Run prunes every Interval, and at once when free space falls below the low watermark (aggressively
// while it stays there).
func (j *Janitor) Run(ctx context.Context) {
	interval, tick := j.Config.Interval, j.Tick
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	if tick <= 0 || tick > interval {
		tick = min(time.Minute, interval)
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	last, wasLow := time.Now(), false
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		low := j.LowDisk != nil && j.LowDisk()
		if low && !wasLow {
			j.log().Warn("janitor: disk below low watermark; aggressive prune")
		}
		if (low && !wasLow) || time.Since(last) >= interval {
			j.Prune(ctx, low)
			last = time.Now()
		}
		wasLow = low
	}
}

// hook runs one command in its own process group with the timeout; failures are logged only.
func (j *Janitor) hook(ctx context.Context, kind, command string, env ...string) {
	timeout := j.Config.HookTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	start := time.Now()
	err := cmd.Run()
	b := out.Bytes()
	if len(b) > 4096 {
		b = b[len(b)-4096:]
	}
	if err != nil {
		j.log().Error("janitor: hook failed", "kind", kind, "command", command, "env", env, "error", err, "output", string(b))
		return
	}
	j.log().Info("janitor: hook", "kind", kind, "command", command, "env", env, "duration", time.Since(start).Round(time.Millisecond))
}

package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// runPid in .factory/run.pid records the process group of the run holding a worktree.
// The leader's start time tells a reused PID from the original leader.
type runPid struct {
	Group   int    `json:"group"`
	Started string `json:"started"`
}

var (
	stopGrace = 10 * time.Second // SIGTERM to SIGKILL
	stopLimit = 30 * time.Second // group gone or Stop fails
)

func pidPath(path string) string { return filepath.Join(path, ".factory", "run.pid") }

// Track records the run's process group; group is the PID of a leader started with Setpgid.
// The caller must keep reaping its leader (cmd.Wait) or the zombie keeps the group alive.
func (wt Worktree) Track(group int) error {
	if group <= 1 {
		return fmt.Errorf("workspace: invalid process group %d", group)
	}
	started, err := processStart(group)
	if err != nil || started == "" {
		return fmt.Errorf("workspace: process %d: start time unknown: %v", group, err)
	}
	b, _ := json.Marshal(runPid{group, started})
	tmp := pidPath(wt.Path) + ".tmp"
	if err = os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, pidPath(wt.Path))
}

// Stop kills the tracked process group (SIGTERM, SIGKILL after stopGrace), waits up to
// stopLimit for it to be gone, then removes the pidfile. The pidfile stays when the group survives.
func (wt Worktree) Stop() error { return stopRun(wt.Path) }

func stopRun(path string) error {
	b, err := os.ReadFile(pidPath(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	var p runPid
	if json.Unmarshal(b, &p) == nil && p.Group > 1 && running(p) {
		if err = killGroup(p); err != nil {
			return err
		}
	}
	if err = os.Remove(pidPath(path)); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func processStart(pid int) (string, error) {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", fmt.Sprint(pid)).Output()
	return strings.TrimSpace(string(out)), err
}

// running reports whether the recorded group still exists. A live leader must have the recorded
// start time; without a leader, a live group with that ID can only be the original one, because
// a PID is not reused while a process group with that ID exists.
func running(p runPid) bool {
	if started, err := processStart(p.Group); err == nil && started != "" {
		return started == p.Started
	}
	return !errors.Is(syscall.Kill(-p.Group, 0), syscall.ESRCH)
}

func killGroup(p runPid) error {
	_ = syscall.Kill(-p.Group, syscall.SIGTERM)
	start, killed := time.Now(), false
	for running(p) {
		el := time.Since(start)
		if el >= stopLimit {
			return fmt.Errorf("workspace: process group %d still alive after %s", p.Group, stopLimit)
		}
		if !killed && el >= stopGrace {
			_ = syscall.Kill(-p.Group, syscall.SIGKILL)
			killed = true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

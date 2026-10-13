package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// A pidfile records the process group of a running runtime so a restarted worker can kill it.
// The leader's start time tells a reused PID from the original leader.
type pidfile struct {
	Run     string `json:"run"`
	Group   int    `json:"group"`
	Started string `json:"started"`
}

func processStart(pid int) (string, error) {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", fmt.Sprint(pid)).Output()
	return strings.TrimSpace(string(out)), err
}

func writePidfile(dir, run string, group int) (string, error) {
	started, err := processStart(group)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(pidfile{run, group, started})
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, run+".pid.json")
	return path, os.WriteFile(path, b, 0o600)
}

// killOrphans kills and reaps the runtime process groups left by previous runs of this
// worker, then removes their pidfiles.
func killOrphans(dir string) error {
	paths, err := filepath.Glob(filepath.Join(dir, "*.pid.json"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		var p pidfile
		b, err := os.ReadFile(path)
		if err == nil {
			err = json.Unmarshal(b, &p)
		}
		if err != nil || p.Group <= 1 {
			slog.Error("invalid pidfile removed", "path", path, "error", err)
			_ = os.Remove(path)
			continue
		}
		if orphaned(p) {
			slog.Info("killing orphaned runtime", "run", p.Run, "group", p.Group)
			if err = killGroup(p.Group); err != nil {
				return fmt.Errorf("run %s: %w", p.Run, err)
			}
		}
		if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// orphaned reports whether the recorded group still exists. A live leader must have the
// recorded start time; without a leader, a live group with that ID can only be the original
// one, because a PID is not reused while a process group with that ID exists.
func orphaned(p pidfile) bool {
	if started, err := processStart(p.Group); err == nil && started != "" {
		return started == p.Started
	}
	return groupAlive(p.Group)
}

func groupAlive(group int) bool {
	return !errors.Is(syscall.Kill(-group, 0), syscall.ESRCH)
}

func killGroup(group int) error {
	if err := syscall.Kill(-group, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if !groupAlive(group) {
			return nil
		}
	}
	return fmt.Errorf("process group %d still alive", group)
}

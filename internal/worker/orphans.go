package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"appfactory/internal/procgroup"
)

// A pidfile records the process group of a running runtime so a restarted worker can kill it.
// The leader's start time tells a reused PID from the original leader.
type pidfile struct {
	Run     string `json:"run"`
	Group   int    `json:"group"`
	Started string `json:"started"`
}

func writePidfile(dir, run string, group int) (string, error) {
	g, err := procgroup.Record(group)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(pidfile{run, g.ID, g.Started})
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, run+".pid.json")
	return path, os.WriteFile(path, b, 0o600)
}

// killOrphans kills (SIGKILL) and reaps the runtime process groups left by previous runs of
// this worker, then removes their pidfiles.
func killOrphans(dir string) error {
	groups, err := TrackedGroups(dir)
	if err != nil {
		return err
	}
	for _, g := range groups {
		if err = g.Reap(); err != nil {
			return err
		}
	}
	return nil
}

// KillOrphans is killOrphans for executors hosted outside this package (node slots).
func KillOrphans(dir string) error {
	if err := killOrphans(dir); err != nil {
		return fmt.Errorf("orphaned runtimes: %w", err)
	}
	return nil
}

// TrackedGroup is a runtime process group recorded in a StateDir pidfile.
type TrackedGroup struct {
	Run   string
	Group int
	path  string
	p     pidfile
}

// TrackedGroups lists the pidfiles in dir; invalid ones are removed.
func TrackedGroups(dir string) ([]TrackedGroup, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.pid.json"))
	if err != nil {
		return nil, err
	}
	var r []TrackedGroup
	for _, path := range paths {
		var p pidfile
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue // the run ended meanwhile
		}
		if err == nil {
			err = json.Unmarshal(b, &p)
		}
		if err != nil || p.Group <= 1 {
			slog.Error("invalid pidfile removed", "path", path, "error", err)
			_ = os.Remove(path)
			continue
		}
		r = append(r, TrackedGroup{p.Run, p.Group, path, p})
	}
	return r, nil
}

// Reap kills the group if it is still the recorded one, then removes its pidfile.
func (g TrackedGroup) Reap() error {
	if pg := (procgroup.Group{ID: g.p.Group, Started: g.p.Started}); pg.Running() {
		slog.Info("killing orphaned runtime", "run", g.Run, "group", g.Group)
		if err := pg.Stop(0, 5*time.Second); err != nil {
			return fmt.Errorf("run %s: %w", g.Run, err)
		}
	}
	if err := os.Remove(g.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// GroupAlive reports whether any process of the group still exists.
func GroupAlive(group int) bool { return procgroup.Alive(group) }

package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"appfactory/internal/procgroup"
)

// .factory/run.pid records the process group of the run holding a worktree (procgroup.Group).
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
	g, err := procgroup.Record(group)
	if err != nil {
		return fmt.Errorf("workspace: %w", err)
	}
	b, _ := json.Marshal(g)
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
	var g procgroup.Group
	if json.Unmarshal(b, &g) == nil {
		if err = g.Stop(stopGrace, stopLimit); err != nil {
			return fmt.Errorf("workspace: %w", err)
		}
	}
	if err = os.Remove(pidPath(path)); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

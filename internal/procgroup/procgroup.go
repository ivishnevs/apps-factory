// Package procgroup records and stops runtime process groups. A group is identified by its
// leader's PID and start time, so a reused PID is never mistaken for the original leader.
package procgroup

import (
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Group is a process group whose leader (PID ID) started at Started (`ps -o lstart=`).
type Group struct {
	ID      int    `json:"group"`
	Started string `json:"started"`
}

// psBinary is the ps the start time is read with (a test replaces it).
var psBinary = "ps"

// StartTime is the start time of a live process as `ps -o lstart=` prints it.
func StartTime(pid int) (string, error) {
	out, err := exec.Command(psBinary, "-o", "lstart=", "-p", fmt.Sprint(pid)).Output()
	return strings.TrimSpace(string(out)), err
}

// Record returns the group led by the live process pid.
func Record(pid int) (Group, error) {
	started, err := StartTime(pid)
	if err == nil && started == "" {
		err = errors.New("empty ps output")
	}
	if err != nil {
		return Group{}, fmt.Errorf("process %d: start time unknown: %w", pid, err)
	}
	return Group{pid, started}, nil
}

var recordFailed sync.Once

// RecordOrLeader is Record for callers that fall back to killing only the leader (a zero Group): the first
// failure in this process is logged with a hint, later ones are not (they share the cause, usually a missing
// ps).
func RecordOrLeader(pid int, who string) Group {
	g, err := Record(pid)
	if err != nil {
		recordFailed.Do(func() {
			slog.Warn("runtime process group not recorded; stopping a runtime kills only its leader and can orphan its children",
				"component", who, "error", err, "hint", "install procps (ps); see factory-node doctor")
		})
	}
	return g
}

// Alive reports whether any process group with this ID exists.
func Alive(id int) bool {
	return !errors.Is(syscall.Kill(-id, 0), syscall.ESRCH)
}

// Running reports whether the recorded group still exists. A live leader must have the recorded
// start time; without a leader, a live group with that ID can only be the original one, because
// a PID is not reused while a process group with that ID exists.
func (g Group) Running() bool {
	if started, err := StartTime(g.ID); err == nil && started != "" {
		return started == g.Started
	}
	return Alive(g.ID)
}

// Stop sends SIGTERM to the group if it is running, SIGKILL after grace (at once when grace is 0),
// and fails when the group still runs after limit.
func (g Group) Stop(grace, limit time.Duration) error {
	if g.ID <= 1 || !g.Running() {
		return nil
	}
	sig, killed := syscall.SIGTERM, false
	if grace <= 0 {
		sig, killed = syscall.SIGKILL, true
	}
	if err := syscall.Kill(-g.ID, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	for start := time.Now(); g.Running(); time.Sleep(50 * time.Millisecond) {
		el := time.Since(start)
		if el >= limit {
			return fmt.Errorf("process group %d still alive after %s", g.ID, limit)
		}
		if !killed && el >= grace {
			_ = syscall.Kill(-g.ID, syscall.SIGKILL)
			killed = true
		}
	}
	return nil
}

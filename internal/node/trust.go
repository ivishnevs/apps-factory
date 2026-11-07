package node

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Node trust (connector-trust): what the person on the machine controls without Core — a local pause that stops
// new work at once, and a local activity log of what ran here.

// LocalPauseFile is the flag `factory-node pause` writes; while it exists the node claims no runs.
func LocalPauseFile(stateDir string) string { return filepath.Join(stateDir, "paused") }

// LocallyPaused says whether the machine's person paused this node.
func LocallyPaused(stateDir string) bool {
	_, err := os.Stat(LocalPauseFile(stateDir))
	return err == nil
}

// SetLocalPause pauses (reason) or resumes ("" with on false) this node.
func SetLocalPause(stateDir string, on bool, reason string) error {
	if !on {
		err := os.Remove(LocalPauseFile(stateDir))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(LocalPauseFile(stateDir), []byte(time.Now().UTC().Format(time.RFC3339)+" "+reason+"\n"), 0o600)
}

// Activity is one line of the local activity log: a run that started or ended on this node.
type Activity struct {
	At      time.Time `json:"at"`
	Event   string    `json:"event"` // run.started, run.ended
	Run     string    `json:"run"`
	Runtime string    `json:"runtime,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Project string    `json:"project,omitempty"`
	Task    string    `json:"task,omitempty"`
	Cwd     string    `json:"cwd,omitempty"`
	Outcome string    `json:"outcome,omitempty"`
}

// ActivityFile is the node's local activity log.
func ActivityFile(stateDir string) string { return filepath.Join(stateDir, "activity.jsonl") }

// LogActivity appends a to the log (best effort).
func LogActivity(stateDir string, a Activity) {
	if stateDir == "" {
		return
	}
	if a.At.IsZero() {
		a.At = time.Now().UTC()
	}
	f, err := os.OpenFile(ActivityFile(stateDir), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(a)
	_, _ = f.Write(append(b, '\n'))
}

// ReadActivity returns the last n log lines, oldest first.
func ReadActivity(stateDir string, n int) ([]Activity, error) {
	f, err := os.Open(ActivityFile(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var all []Activity
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var a Activity
		if json.Unmarshal(sc.Bytes(), &a) == nil {
			all = append(all, a)
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, sc.Err()
}

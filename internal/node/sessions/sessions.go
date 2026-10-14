// Package sessions records the runtime sessions this node ran (runtime-drivers D1, ResumePolicy): one
// file per session ID under <state_dir>/sessions. A run resumes its spec.SessionID only when this node
// has a record of it for the same (task, agent) key; reset-workspace forgets a task's records so its
// next run starts a fresh session.
package sessions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Record is the last run of one session on this node.
type Record struct {
	SessionID string `json:"session_id"`
	Task      string `json:"task,omitempty"`
	Agent     string `json:"agent"`
	Project   string `json:"project,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Run       string `json:"run"`
	// Started: the prompt reached the runtime.
	Started bool `json:"started"`
	// Status is the run's terminal status; "" while running or when the node stopped first.
	Status string    `json:"status,omitempty"`
	At     time.Time `json:"at"`
}

// Store keeps one record file per session under Dir.
type Store struct{ Dir string }

// Open is the store under stateDir/sessions.
func Open(stateDir string) *Store { return &Store{Dir: filepath.Join(stateDir, "sessions")} }

func (s *Store) path(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(s.Dir, hex.EncodeToString(sum[:16])+".json")
}

// Get returns the record of session id; ok is false when this node has none.
func (s *Store) Get(id string) (r Record, ok bool, err error) {
	if id == "" {
		return r, false, nil
	}
	b, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return r, false, nil
	}
	if err != nil {
		return r, false, err
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return r, false, fmt.Errorf("session record %s: %w", s.path(id), err)
	}
	return r, r.SessionID == id, nil
}

// Put writes r atomically.
func (s *Store) Put(r Record) error {
	if r.SessionID == "" {
		return errors.New("session record without session id")
	}
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	p := s.path(r.SessionID)
	tmp, err := os.CreateTemp(s.Dir, ".tmp-")
	if err != nil {
		return err
	}
	if _, err = tmp.Write(append(b, '\n')); err == nil {
		err = tmp.Close()
	} else {
		tmp.Close()
	}
	if err == nil {
		err = os.Rename(tmp.Name(), p)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// ForgetTask removes every record of task (any agent) and returns the forgotten session IDs.
func (s *Store) ForgetTask(task string) ([]string, error) {
	if task == "" {
		return nil, errors.New("task required")
	}
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var gone []string
	var errs []error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(s.Dir, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var r Record
		if json.Unmarshal(b, &r) != nil || r.Task != task {
			continue
		}
		if err = os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		gone = append(gone, r.SessionID)
	}
	return gone, errors.Join(errs...)
}

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"appfactory/internal/driver"
	"appfactory/internal/node/sessions"
	"appfactory/internal/protocol"
	"appfactory/internal/skills"
)

// SkillError refuses a run before the runtime starts: an offered skill is not pinned, cannot be downloaded, or its
// content differs from the hash pinned on the run.
type SkillError struct{ Msg string }

func (e *SkillError) Error() string { return "skills: " + e.Msg }

// pinnedSkills are the skill hashes Core recorded on the run (agent snapshot "skill_hashes").
func pinnedSkills(spec protocol.RunSpec) (map[string]string, error) {
	var snap struct {
		SkillHashes map[string]string `json:"skill_hashes"`
	}
	if len(spec.ConfigSnapshot) > 0 {
		if err := json.Unmarshal(spec.ConfigSnapshot, &snap); err != nil {
			return nil, fmt.Errorf("invalid agent snapshot: %w", err)
		}
	}
	return snap.SkillHashes, nil
}

// stageSkills builds the run's view of its skills (skill-registry D8): each offered skill's pinned hash is taken
// from the node's store (downloaded from Core on first use and verified) and copied to root/<name>, a new directory
// outside the workspace the drivers install from; the caller removes it. root is "" when nothing is staged.
func (w *Worker) stageSkills(ctx context.Context, spec protocol.RunSpec) (root string, list []driver.Skill, err error) {
	if len(spec.Skills) == 0 {
		return "", nil, nil
	}
	pins, err := pinnedSkills(spec)
	if err != nil {
		return "", nil, err
	}
	names := append([]string(nil), spec.Skills...)
	sort.Strings(names)
	if root, err = os.MkdirTemp("", "factory-skills-"); err != nil {
		return "", nil, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(root)
			root, list = "", nil
		}
	}()
	for _, name := range names {
		if !skills.ValidName(name) {
			return root, nil, &SkillError{fmt.Sprintf("invalid skill name %q", name)}
		}
		pin := pins[name]
		if pin == "" { // Core pinned nothing (no skill index): the node has no content to take
			slog.Warn("skill not materialized: no content hash pinned on the run", "run", spec.ID, "skill", name)
			continue
		}
		src, e := w.storedSkill(ctx, spec.ID, pin)
		if e != nil {
			return root, nil, &SkillError{fmt.Sprintf("%s: %v", name, e)}
		}
		hash, e := skills.Copy(src, filepath.Join(root, name))
		if e != nil {
			return root, nil, &SkillError{fmt.Sprintf("%s: %v", name, e)}
		}
		if hash != pin {
			return root, nil, &SkillError{fmt.Sprintf("%s content hash %s differs from the run's pinned %s", name, hash, pin)}
		}
		list = append(list, driver.Skill{Name: name, Hash: hash})
	}
	return root, list, nil
}

func (w *Worker) skillStore() string {
	if w.SkillStore != "" {
		return w.SkillStore
	}
	return filepath.Join(os.TempDir(), "factory-skill-store")
}

// storedSkill is the store directory of hash, downloaded from Core for run when missing: unpacked into tmp/,
// verified against hash, renamed into store/sha256-<hex> and made read-only; its use is recorded for the GC.
func (w *Worker) storedSkill(ctx context.Context, run, hash string) (string, error) {
	hex, ok := strings.CutPrefix(hash, skills.HashPrefix)
	if !ok || len(hex) != 64 || strings.Trim(hex, "0123456789abcdef") != "" {
		return "", fmt.Errorf("invalid skill hash %q", hash)
	}
	store := w.skillStore()
	dir := filepath.Join(store, "store", "sha256-"+hex)
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err == nil {
		now := time.Now()
		_ = os.Chtimes(dir, now, now)
		return dir, nil
	}
	if err := os.MkdirAll(filepath.Join(store, "tmp"), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Join(store, "tmp"), "skill-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	body, err := w.API.Get(ctx, "/runs/"+run+"/skills/"+hash)
	if err != nil {
		return "", fmt.Errorf("download from Core: %w", err)
	}
	defer body.Close()
	got, err := skills.Unpack(io.LimitReader(body, 32<<20), filepath.Join(tmp, "skill"))
	if err != nil {
		return "", err
	}
	if got != hash {
		return "", fmt.Errorf("downloaded content hash %s differs from %s", got, hash)
	}
	if err = os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	if err = os.Rename(filepath.Join(tmp, "skill"), dir); err != nil {
		if _, e := os.Stat(filepath.Join(dir, "SKILL.md")); e == nil { // another slot stored it meanwhile
			return dir, nil
		}
		return "", err
	}
	// Immutable once in place (a read-only directory cannot be renamed on every system).
	return dir, readOnly(dir)
}

// readOnly makes a stored skill immutable: files 0444, directories 0555.
func readOnly(dir string) error {
	var dirs []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		return os.Chmod(p, 0o444)
	})
	for i := len(dirs) - 1; i >= 0 && err == nil; i-- {
		err = os.Chmod(dirs[i], 0o555)
	}
	return err
}

// GCSkillStore removes stored skills not used for maxAge, except the hashes in keep (pinned by live runs).
func GCSkillStore(store string, maxAge time.Duration, keep map[string]bool) (int, error) {
	entries, err := os.ReadDir(filepath.Join(store, "store"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		hash := skills.HashPrefix + strings.TrimPrefix(e.Name(), "sha256-")
		info, err := e.Info()
		if err != nil || keep[hash] || time.Since(info.ModTime()) < maxAge {
			continue
		}
		p := filepath.Join(store, "store", e.Name())
		_ = filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(q, 0o755)
			}
			return nil
		})
		if err = os.RemoveAll(p); err == nil {
			n++
		}
	}
	return n, nil
}

// FreshSessionNote starts the prompt of a run that does not resume its session.
const FreshSessionNote = protocol.FreshSessionNote

// errSessionLost: the node ran the task's session under the same account, but the runtime no longer has it
// (session-continuity). The run does not start; Core asks the owner whether to continue in a new session.
type errSessionLost struct{ id string }

func (e errSessionLost) Error() string {
	return "session_lost: this node ran session " + e.id + " for the task under the same account, but the runtime no longer has it (its configuration directory was cleaned or replaced)"
}

// resumeSpec applies the driver's ResumePolicy to spec.SessionID: the session is resumed only when
// this node recorded it for the same (task, agent) key in the same directory and the same runtime account;
// otherwise the run starts a fresh session with FreshSessionNote before its prompt. A session this node should
// resume but the runtime no longer has is errSessionLost for a task run; a conversation starts fresh (its history
// is in Core). Without a session store the spec is unchanged.
func (w *Worker) resumeSpec(drv driver.Driver, spec protocol.RunSpec, cwd string) (protocol.RunSpec, sessions.Record, error) {
	var prev sessions.Record
	if spec.SessionID == "" || w.Sessions == nil {
		return spec, prev, nil
	}
	rec, ok, err := w.Sessions.Get(spec.SessionID)
	if err != nil {
		slog.Error("session record", "run", spec.ID, "session", spec.SessionID, "error", err)
	}
	// A session resumes in the directory it ran in (Claude keeps transcripts per directory). Records without
	// one predate it; a conversation's ran in the project workspace before conversation directories.
	sameDir := rec.Cwd == cwd || rec.Cwd == "" && !spec.Conversation
	sameNode := ok && rec.Task == spec.TaskID && rec.Agent == spec.Agent && sameDir
	loc, _ := drv.(driver.SessionLocator)
	sameAccount := true
	if sameNode && loc != nil && rec.Account != "" {
		if now := loc.AccountHash(); now != "" && now != rec.Account {
			sameAccount = false // the node's login changed (and was accepted): as on a new node
		}
	}
	if sameNode && sameAccount {
		prev = rec
	}
	if drv.ResumePolicy(driver.Outcome{End: prev.Outcome, Started: prev.Started}, sameNode && sameAccount) {
		if loc == nil {
			return spec, prev, nil
		}
		if found, known := loc.HasSession(spec.SessionID); found || !known {
			return spec, prev, nil
		}
		if !spec.Conversation {
			return spec, prev, errSessionLost{spec.SessionID}
		}
		slog.Warn("conversation session lost on this node; starting a new one", "run", spec.ID, "agent", spec.Agent, "session", spec.SessionID)
	}
	slog.Info("fresh session instead of resume", "run", spec.ID, "task", spec.TaskID, "agent", spec.Agent, "session", spec.SessionID, "same_node", sameNode, "same_account", sameAccount, "started", prev.Started)
	spec.SessionID = ""
	spec.Prompt = FreshSessionNote + spec.Prompt
	return spec, sessions.Record{}, nil
}

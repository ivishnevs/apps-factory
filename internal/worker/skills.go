package worker

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"appfactory/internal/driver"
	"appfactory/internal/node/sessions"
	"appfactory/internal/protocol"
	"appfactory/internal/skills"
)

// SkillError refuses a run before the runtime starts: an offered skill is missing from the node's
// registry, cannot be copied, or its content differs from the hash pinned on the run.
type SkillError struct{ Msg string }

func (e *SkillError) Error() string { return "skills: " + e.Msg }

// pinnedSkills are the skill hashes recorded on the run. Core does not record them yet; the agent
// snapshot's "skill_hashes" ({name: "sha256:..."}) is read when present.
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

// stageSkills copies the run's offered skills from the node's registry into a new directory outside
// the workspace (root/<name>) and verifies each copy against the hash pinned on the run; a skill
// without a pin is materialized with its computed hash logged. root is "" when nothing is staged;
// the caller removes it.
func (w *Worker) stageSkills(spec protocol.RunSpec) (root string, list []driver.Skill, err error) {
	if len(spec.Skills) == 0 {
		return "", nil, nil
	}
	if w.Skills == nil {
		slog.Warn("skills not materialized: no local skills registry", "run", spec.ID, "skills", spec.Skills)
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
		src, e := w.Skills.Dir(name)
		if e != nil {
			return root, nil, &SkillError{fmt.Sprintf("%s is not in this node's skills registry (%s)", name, w.Skills.Root)}
		}
		hash, e := skills.Copy(src, filepath.Join(root, name))
		if e != nil {
			return root, nil, &SkillError{fmt.Sprintf("%s: %v", name, e)}
		}
		switch pin := pins[name]; {
		case pin == "":
			slog.Info("skill hash not pinned on the run; materialized as computed", "run", spec.ID, "skill", name, "hash", hash)
		case pin != hash:
			return root, nil, &SkillError{fmt.Sprintf("%s content hash %s differs from the run's pinned %s; update the node's skills (%s) or rerun with the current skills", name, hash, pin, w.Skills.Root)}
		}
		list = append(list, driver.Skill{Name: name, Hash: hash})
	}
	return root, list, nil
}

// FreshSessionNote starts the prompt of a run that does not resume its session.
const FreshSessionNote = "[App Factory] This run starts a new runtime session: the previous session of this task is not available here (it ran on another node, or the workspace was reset). Take the task's context, history and decisions from Core: this prompt and the Factory tools.\n\n"

// resumeSpec applies the driver's ResumePolicy to spec.SessionID: the session is resumed only when
// this node recorded it for the same (task, agent) key; otherwise the run starts a fresh session with
// FreshSessionNote before its prompt. Without a session store the spec is unchanged.
func (w *Worker) resumeSpec(drv driver.Driver, spec protocol.RunSpec) (protocol.RunSpec, sessions.Record) {
	var prev sessions.Record
	if spec.SessionID == "" || w.Sessions == nil {
		return spec, prev
	}
	rec, ok, err := w.Sessions.Get(spec.SessionID)
	if err != nil {
		slog.Error("session record", "run", spec.ID, "session", spec.SessionID, "error", err)
	}
	sameNode := ok && rec.Task == spec.TaskID && rec.Agent == spec.Agent
	if sameNode {
		prev = rec
	}
	if drv.ResumePolicy(driver.Outcome{Status: prev.Status, Started: prev.Started}, sameNode) {
		return spec, prev
	}
	slog.Info("fresh session instead of resume", "run", spec.ID, "task", spec.TaskID, "agent", spec.Agent, "session", spec.SessionID, "same_node", sameNode, "started", prev.Started)
	spec.SessionID = ""
	spec.Prompt = FreshSessionNote + spec.Prompt
	return spec, sessions.Record{}
}

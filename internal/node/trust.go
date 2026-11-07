package node

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// Policy is what the machine's person allows (connector-trust 1.2, node.yaml policy); Core cannot change it.
type Policy struct {
	// Projects are the project ids this node takes ("" or empty: all).
	Projects []string `yaml:"projects"`
	// Runtimes are the runtimes it runs (empty: all enabled drivers).
	Runtimes []string `yaml:"runtimes"`
	// FullAccess are projects whose agents run without the worktree sandbox when sandbox is worktree.
	FullAccess []string `yaml:"full_access"`
	// Network "off" refuses runs of agents that need network beyond git (reserved; runs report it).
	Network string `yaml:"network"`
}

// Refuse says why the policy refuses a run of project with runtime ("" when it is allowed).
func (p Policy) Refuse(project, runtime string) string {
	if len(p.Projects) > 0 && !contains(p.Projects, project) {
		return "project " + project + " is not in policy.projects"
	}
	if len(p.Runtimes) > 0 && !contains(p.Runtimes, runtime) {
		return "runtime " + runtime + " is not in policy.runtimes"
	}
	return ""
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Security is a node's trust settings as people see them (connector-trust 2.1): the sandbox, the local policy and
// pause, whether the runtimes have the node's own logins, and whether the node's key files are private.
type Security struct {
	Sandbox        string   `json:"sandbox"`
	LocallyPaused  bool     `json:"locally_paused,omitempty"`
	Projects       []string `json:"projects,omitempty"`
	Runtimes       []string `json:"runtimes,omitempty"`
	FullAccess     []string `json:"full_access,omitempty"`
	OwnClaudeLogin bool     `json:"own_claude_login"`
	OwnCodexLogin  bool     `json:"own_codex_login"`
	KeysPrivate    bool     `json:"keys_private"`
}

// SecurityOf reads c's trust settings.
func SecurityOf(c Config) Security {
	s := Security{Sandbox: c.Sandbox, LocallyPaused: LocallyPaused(c.StateDir), Projects: c.Policy.Projects, Runtimes: c.Policy.Runtimes,
		FullAccess: c.Policy.FullAccess, OwnClaudeLogin: c.Drivers.Claude.ConfigDir != "", OwnCodexLogin: c.Drivers.Codex.Home != "", KeysPrivate: true}
	if s.Sandbox == "" {
		s.Sandbox = "full"
	}
	for _, f := range []string{SignKeyFile, BoxKeyFile} {
		if fi, err := os.Stat(filepath.Join(c.StateDir, f)); err == nil && fi.Mode().Perm()&0o077 != 0 {
			s.KeysPrivate = false
		}
	}
	return s
}

// SecurityChecks are doctor --security's checks: what agents may touch here and what protects the node.
func SecurityChecks(c Config) []CheckResult {
	s := SecurityOf(c)
	var r []CheckResult
	if s.Sandbox == "worktree" {
		r = append(r, CheckResult{Check: "security.sandbox", Status: Pass, Detail: "agents write only in their task's worktree; SSH, cloud and node keys unreadable"})
	} else {
		r = append(r, CheckResult{Check: "security.sandbox", Status: Warn, Detail: "agents act as this user everywhere (sandbox: full)", Hint: "set sandbox: worktree in node.yaml and restart the node"})
	}
	if len(s.FullAccess) > 0 {
		r = append(r, CheckResult{Check: "security.full_access", Status: Warn, Detail: "projects with full access: " + strings.Join(s.FullAccess, ", ")})
	}
	if len(s.Projects) == 0 {
		r = append(r, CheckResult{Check: "security.policy", Status: Warn, Detail: "every project of the company may run here", Hint: "limit with policy.projects in node.yaml"})
	} else {
		r = append(r, CheckResult{Check: "security.policy", Status: Pass, Detail: "projects: " + strings.Join(s.Projects, ", ")})
	}
	for _, x := range []struct {
		rt  string
		own bool
	}{{"claude", s.OwnClaudeLogin}, {"codex", s.OwnCodexLogin}} {
		rt, own := x.rt, x.own
		if own {
			r = append(r, CheckResult{Check: "security." + rt + "_login", Status: Pass, Detail: "the node's own " + rt + " login"})
		} else {
			r = append(r, CheckResult{Check: "security." + rt + "_login", Status: Warn, Detail: rt + " uses this user's own login", Hint: "factory-node login " + rt})
		}
	}
	if s.KeysPrivate {
		r = append(r, CheckResult{Check: "security.keys", Status: Pass, Detail: "node key files readable by this user only"})
	} else {
		r = append(r, CheckResult{Check: "security.keys", Status: Fail, Detail: "node key files are readable by others", Hint: "chmod 600 " + filepath.Join(c.StateDir, "factory-node-*.key")})
	}
	if s.LocallyPaused {
		r = append(r, CheckResult{Check: "security.pause", Status: Warn, Detail: "paused locally (factory-node resume)"})
	}
	return r
}

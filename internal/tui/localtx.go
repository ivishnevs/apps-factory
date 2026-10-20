package tui

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"appfactory/internal/redact"
)

// LocalTranscripts finds a run's runtime session file when the UI runs on the node's machine: Claude Code
// keeps <ClaudeDir>/projects/<project dir>/<session id>.jsonl, Codex <CodexHome>/sessions/YYYY/MM/DD/
// rollout-<time>-<thread id>.jsonl. Empty directories mean the defaults ($CLAUDE_CONFIG_DIR or ~/.claude,
// $CODEX_HOME or ~/.codex).
type LocalTranscripts struct {
	ClaudeDir string
	CodexHome string
	// Off disables local transcripts (tests, a UI that is not on a node).
	Off bool
}

func (l LocalTranscripts) claudeDir() string {
	if l.ClaudeDir != "" {
		return l.ClaudeDir
	}
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func (l LocalTranscripts) codexHome() string {
	if l.CodexHome != "" {
		return l.CodexHome
	}
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

// errNoLocal: the session file is not on this machine.
var errNoLocal = errors.New("not available here")

// safeID: a session or thread id is a plain name (it becomes part of a file name pattern).
func safeID(id string) bool {
	if id == "" || len(id) > 200 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return !strings.Contains(id, "..")
}

// Find is the session file of provider ("claude", "codex"; empty tries both) and session id.
func (l LocalTranscripts) Find(provider, session string) (string, string, error) {
	if l.Off || !safeID(session) {
		return "", "", errNoLocal
	}
	if provider != "codex" {
		matches, _ := filepath.Glob(filepath.Join(l.claudeDir(), "projects", "*", session+".jsonl"))
		if len(matches) > 0 {
			return matches[0], "claude", nil
		}
	}
	if provider != "claude" {
		root := filepath.Join(l.codexHome(), "sessions")
		found := ""
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || found != "" {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") && strings.Contains(d.Name(), session) {
				found = path
				return fs.SkipAll
			}
			return nil
		})
		if found != "" {
			return found, "codex", nil
		}
	}
	return "", "", errNoLocal
}

// Read parses a session file found by Find, with credentials redacted by the node's rules (the values of
// credential-named environment variables and common token shapes).
func (l LocalTranscripts) Read(path, provider string) ([]TxEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var entries []TxEntry
	if provider == "codex" {
		entries, err = parseCodexSession(f)
	} else {
		entries, err = parseClaudeSession(f)
	}
	return redactEntries(entries, redact.New(redact.FromEnv(os.Environ())...)), err
}

// redactEntries replaces credentials in every text of the entries.
func redactEntries(entries []TxEntry, r *redact.Redactor) []TxEntry {
	clean := func(s string) string {
		if s == "" {
			return s
		}
		b, _ := r.Bytes([]byte(s))
		return string(b)
	}
	for i := range entries {
		e := &entries[i]
		e.Input, e.Brief, e.Result, e.Text = clean(e.Input), clean(e.Brief), clean(e.Result), clean(e.Text)
	}
	return entries
}

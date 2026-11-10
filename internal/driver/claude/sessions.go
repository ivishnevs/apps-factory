package claude

import (
	"os"
	"path/filepath"

	"appfactory/internal/driver"
	"appfactory/internal/node"
)

// configDir is the Claude Code configuration the node's runs use: the node's own, else CLAUDE_CONFIG_DIR, else
// ~/.claude.
func (d Driver) configDir() string {
	if d.ConfigDir != "" {
		return node.ExpandHome(d.ConfigDir)
	}
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// HasSession reports whether Claude Code still has the session's transcript (projects/<cwd>/<id>.jsonl) in the
// node's configuration (session-continuity).
func (d Driver) HasSession(id string) (found, known bool) {
	return driver.HasSessionFile([]string{filepath.Join(d.configDir(), "projects")}, 1, func(name string) bool { return name == id+".jsonl" })
}

// AccountHash is the recorded account of the node's Claude login.
func (d Driver) AccountHash() string {
	if d.AccountsDir == "" {
		return ""
	}
	a, _ := node.LoadAccount(d.AccountsDir, "claude")
	return a.Hash
}

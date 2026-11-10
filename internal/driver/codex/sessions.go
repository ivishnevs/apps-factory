package codex

import (
	"os"
	"path/filepath"
	"strings"

	"appfactory/internal/driver"
	"appfactory/internal/node"
)

func (d Driver) home() string {
	if d.Home != "" {
		return node.ExpandHome(d.Home)
	}
	if v := os.Getenv("CODEX_HOME"); v != "" {
		return v
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".codex")
}

// HasSession reports whether Codex still has the thread's rollout (sessions/YYYY/MM/DD/rollout-…-<id>.jsonl, or
// archived_sessions) under the node's CODEX_HOME (session-continuity).
func (d Driver) HasSession(id string) (found, known bool) {
	h := d.home()
	return driver.HasSessionFile([]string{filepath.Join(h, "sessions"), filepath.Join(h, "archived_sessions")}, 4, func(name string) bool {
		return strings.HasPrefix(name, "rollout-") && (strings.HasSuffix(name, "-"+id+".jsonl") || strings.HasSuffix(name, "-"+id+".json"))
	})
}

// AccountHash is the recorded account of the node's Codex login.
func (d Driver) AccountHash() string {
	if d.AccountsDir == "" {
		return ""
	}
	a, _ := node.LoadAccount(d.AccountsDir, "codex")
	return a.Hash
}

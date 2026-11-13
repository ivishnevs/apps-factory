package node

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RuntimeCache is a directory the agent runtimes fill on this node outside the task worktrees, reported in the
// heartbeat's health (node-runtime-caches) so the Nodes page shows where the disk goes.
type RuntimeCache struct {
	// Name: claude_tmp (Claude Code's temp directory: task output, the TMPDIR of sandboxed commands), claude_data
	// (the node's Claude Code login: sessions, transcripts) or codex_data (the node's CODEX_HOME: sessions, logs).
	Name  string `json:"name"`
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	// AgentBytes: of a directory the machine's own sessions share (claude_tmp), the part of this node's runs.
	AgentBytes *int64 `json:"agent_bytes,omitempty"`
}

// ClaudeTmpDir is Claude Code's per-user temp directory: <CLAUDE_CODE_TMPDIR or /tmp>/claude-<uid>. Agents run as
// the node's user, so it is the node's uid. Empty where there is no uid (Windows).
func ClaudeTmpDir(getenv func(string) string) string {
	uid := os.Getuid()
	if uid < 0 {
		return ""
	}
	base := getenv("CLAUDE_CODE_TMPDIR")
	if base == "" {
		base = "/tmp"
	}
	return filepath.Join(base, "claude-"+strconv.Itoa(uid))
}

// RuntimeCaches measures the runtime caches of c that exist. It walks whole trees: call it off the heartbeat path.
func RuntimeCaches(c Config, getenv func(string) string) []RuntimeCache {
	out := []RuntimeCache{}
	if dir := ClaudeTmpDir(getenv); dir != "" {
		if total, ok := treeBytes(dir); ok {
			// Claude Code names a session's directory after its working directory with every other character
			// than a letter or digit turned into '-': the node's runs work under the state dir or projects root.
			prefixes := []string{}
			for _, root := range []string{c.StateDir, c.ProjectsRoot} {
				if root != "" {
					prefixes = append(prefixes, claudeProjectName(ExpandHome(root)))
				}
			}
			var agents int64
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				for _, p := range prefixes {
					if strings.HasPrefix(e.Name(), p) {
						n, _ := treeBytes(filepath.Join(dir, e.Name()))
						agents += n
						break
					}
				}
			}
			out = append(out, RuntimeCache{Name: "claude_tmp", Path: dir, Bytes: total, AgentBytes: &agents})
		}
	}
	// The runtimes' own data only where the node has its own login: otherwise it is the person's ~/.claude or
	// ~/.codex, not the node's.
	for _, d := range []struct{ name, path string }{{"claude_data", c.Drivers.Claude.ConfigDir}, {"codex_data", c.Drivers.Codex.Home}} {
		if d.path == "" {
			continue
		}
		p := ExpandHome(d.path)
		if n, ok := treeBytes(p); ok {
			out = append(out, RuntimeCache{Name: d.name, Path: p, Bytes: n})
		}
	}
	return out
}

func claudeProjectName(path string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, filepath.Clean(path))
}

// treeBytes is the size of the regular files under dir; ok is false when dir does not exist.
func treeBytes(dir string) (int64, bool) {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return 0, false
	}
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, true
}

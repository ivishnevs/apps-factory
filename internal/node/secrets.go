package node

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Node secrets (external-mcp-servers): node.yaml secrets_file is a KEY=VALUE file only the node's user may
// read. Runs resolve the ${secret:NAME} references of their external MCP servers from it; the values stay on
// the node (never sent to Core, redacted from run logs and events).

var secretKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// ReadSecrets reads a secrets file: one KEY=VALUE per line, blank lines and # comments ignored, an optional
// "export " prefix, a value optionally in matching single or double quotes. A file readable or writable by its
// group or others is refused. Errors name the file and line, never a value.
func ReadSecrets(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("secrets_file: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("secrets_file: %w", err)
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("secrets_file %s is not a regular file", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("secrets_file %s has mode %04o: it must be readable by its owner only (chmod 600)", path, st.Mode().Perm())
	}
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		k = strings.TrimSpace(k)
		if !ok || !secretKey.MatchString(k) {
			return nil, fmt.Errorf("secrets_file %s line %d: expected KEY=VALUE", path, n)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("secrets_file %s: %w", path, err)
	}
	return out, nil
}

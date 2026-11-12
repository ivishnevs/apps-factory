package main

import "testing"

// TestPersonality (node-packaging-updates 3.3): one executable, three programs by the name it runs under: the node,
// the MCP adapter (factory-mcp) and the command shim (factory-shim, or a guarded command linked to it).
func TestPersonality(t *testing.T) {
	for argv0, want := range map[string]string{
		"/home/u/.app-factory/bin/factory-node": "node",
		"factory-node.test":                     "node",
		"/x/versions/1.0/bin/factory-mcp":       "mcp",
		"/x/bin/factory-shim":                   "shim",
		"/tmp/run-shim/git":                     "shim",
		"gh":                                    "shim",
	} {
		if got := personality(argv0); got != want {
			t.Errorf("%s: %s, want %s", argv0, got, want)
		}
	}
}

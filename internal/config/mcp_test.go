package config

import (
	"encoding/json"
	"strings"
	"testing"
)

const mcpServersYAML = `mcp_servers:
  appstoretracker:
    transport: http
    url: https://mcp.example.test/api/mcp
    headers:
      Authorization: "Bearer ${secret:ASTR_API_KEY}"
      X-Client: factory
    deny_tools: [add_to_watchlist]
  local-proxy:
    transport: http
    url: http://127.0.0.1:3310/mcp
`

func claudeAgent(t *testing.T, root, mcp string) {
	t.Helper()
	put(t, root, "configs/agents/agent.yaml", mustRead(t, root, "configs/agents/agent.yaml")+"  runtime:\n    provider: claude\n    quota_key: claude-team\n"+mcp)
}

// TestMCPServers (external-mcp-servers): servers declared once per company, agents opt in with spec.mcp, and
// the agent snapshot carries the definitions with the ${secret:NAME} references unresolved.
func TestMCPServers(t *testing.T) {
	root := fixture(t)
	put(t, root, "configs/mcp_servers.yaml", mcpServersYAML)
	claudeAgent(t, root, "  mcp: [appstoretracker, appstoretracker]\n")
	c, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	a := c.Agents["agent"]
	s, ok := a.MCPServers["appstoretracker"]
	if len(a.MCPServers) != 1 || !ok || s.DenyTools[0] != "add_to_watchlist" || s.Headers["Authorization"] != "Bearer ${secret:ASTR_API_KEY}" {
		t.Fatalf("agent servers %+v", a.MCPServers)
	}
	if got := s.Secrets(); len(got) != 1 || got[0] != "ASTR_API_KEY" {
		t.Fatalf("secrets %v", got)
	}
	b, _ := json.Marshal(a)
	if !strings.Contains(string(b), `"mcp_servers":{"appstoretracker":`) || !strings.Contains(string(b), `${secret:ASTR_API_KEY}`) {
		t.Fatalf("snapshot lacks the unresolved server: %s", b)
	}
	if b, _ := json.Marshal(c.Agents["agent"].Spec); strings.Contains(string(b), "local-proxy") {
		t.Fatal("a server the agent did not opt in to reached it")
	}
}

func TestMCPServersInvalid(t *testing.T) {
	for name, tc := range map[string]struct{ servers, mcp, want string }{
		"unknown server":      {mcpServersYAML, "  mcp: [nope]\n", `unknown MCP server "nope"`},
		"literal auth header": {"mcp_servers:\n  x:\n    transport: http\n    url: https://x.test/mcp\n    headers:\n      Authorization: Bearer abc123\n", "", "must reference a node secret"},
		"literal api key":     {"mcp_servers:\n  x:\n    transport: http\n    url: https://x.test/mcp\n    headers:\n      X-Api-Key: abc123\n", "", "must reference a node secret"},
		"other reference":     {"mcp_servers:\n  x:\n    transport: http\n    url: https://x.test/mcp\n    headers:\n      X-Who: ${HOME}\n", "", "only ${secret:NAME} references"},
		"bad url":             {"mcp_servers:\n  x:\n    transport: http\n    url: not a url\n", "", "url"},
		"remote http":         {"mcp_servers:\n  x:\n    transport: http\n    url: http://x.test/mcp\n", "", "loopback"},
		"stdio":               {"mcp_servers:\n  x:\n    transport: stdio\n    url: https://x.test/mcp\n", "", "only http"},
		"reserved name":       {"mcp_servers:\n  factory:\n    transport: http\n    url: https://x.test/mcp\n", "", "Factory MCP server"},
		"bad tool":            {"mcp_servers:\n  x:\n    transport: http\n    url: https://x.test/mcp\n    deny_tools: [\"a b\"]\n", "", "invalid tool name"},
	} {
		t.Run(name, func(t *testing.T) {
			root := fixture(t)
			put(t, root, "configs/mcp_servers.yaml", tc.servers)
			claudeAgent(t, root, tc.mcp)
			if _, err := LoadCatalog(root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	// Codex agents cannot use external MCP servers.
	root := fixture(t)
	put(t, root, "configs/mcp_servers.yaml", mcpServersYAML)
	put(t, root, "configs/agents/agent.yaml", mustRead(t, root, "configs/agents/agent.yaml")+"  mcp: [appstoretracker]\n")
	if _, err := LoadCatalog(root); err == nil || !strings.Contains(err.Error(), "claude agents only") {
		t.Fatal("codex agent with external MCP accepted:", err)
	}
}

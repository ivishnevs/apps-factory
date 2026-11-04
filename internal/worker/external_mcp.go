package worker

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"appfactory/internal/driver"
	"appfactory/internal/protocol"
)

// SecretEnvPrefix prefixes the runtime environment variable that carries a resolved node secret: the Claude
// runtime expands ${FACTORY_MCP_SECRET_<NAME>} in the MCP server headers from its environment, so the value is
// never on a command line or in the launch line.
const SecretEnvPrefix = "FACTORY_MCP_SECRET_"

// resolvedMCP is a run's external MCP servers with their secrets: Env holds NAME=value pairs for the runtime
// environment and Values the secret values to redact.
type resolvedMCP struct {
	Servers []driver.ExternalMCP
	Env     []string
	Values  []string
}

// externalMCP reads the agent's external MCP servers from the run's config snapshot and resolves their
// ${secret:NAME} references from the company secrets Core delivered with the claim (core-secret-store D5). A
// server the driver cannot pass or a secret that was not delivered fails the run's setup; errors name the server
// and secret, never a value.
func externalMCP(spec protocol.RunSpec, secrets map[string]string, caps driver.Capabilities) (resolvedMCP, error) {
	var out resolvedMCP
	var agent struct {
		MCPServers map[string]protocol.MCPServer `json:"mcp_servers"`
	}
	if len(spec.ConfigSnapshot) > 0 {
		if err := json.Unmarshal(spec.ConfigSnapshot, &agent); err != nil {
			return out, fmt.Errorf("invalid agent snapshot: %w", err)
		}
	}
	if len(agent.MCPServers) == 0 {
		return out, nil
	}
	names := make([]string, 0, len(agent.MCPServers))
	for n := range agent.MCPServers {
		names = append(names, n)
	}
	slices.Sort(names)
	if !caps.ExternalMCP {
		return out, fmt.Errorf("external MCP servers (%s) are not supported by the %s runtime", strings.Join(names, ", "), spec.Provider)
	}
	for _, n := range names {
		s := agent.MCPServers[n]
		if s.Transport != "http" {
			return out, fmt.Errorf("MCP server %s: unsupported transport %q", n, s.Transport)
		}
		for _, k := range s.Secrets() {
			v, ok := secrets[k]
			if !ok || v == "" {
				return out, fmt.Errorf("MCP server %s needs company secret %s, which Core did not deliver with the claim (set it on the Secrets page)", n, k)
			}
			env := SecretEnvPrefix + k + "=" + v
			if !slices.Contains(out.Env, env) {
				out.Env = append(out.Env, env)
				out.Values = append(out.Values, v)
			}
		}
		headers := map[string]string{}
		for h, v := range s.Headers {
			headers[h] = protocol.ExpandSecrets(v, func(k string) string { return "${" + SecretEnvPrefix + k + "}" })
		}
		out.Servers = append(out.Servers, driver.ExternalMCP{Name: n, URL: s.URL, Headers: headers, DenyTools: s.DenyTools})
	}
	return out, nil
}

package protocol

import (
	"regexp"
	"slices"
	"strings"
)

// MCPServer is one external MCP server of a company (configs/<company>/mcp_servers.yaml, external-mcp-servers):
// it travels in a run's config snapshot (agent mcp_servers) with its header values unresolved; the node replaces
// each ${secret:NAME} with its own secrets file's value just before the runtime starts.
type MCPServer struct {
	// Transport is "http" (streamable HTTP), the only one supported.
	Transport string `yaml:"transport" json:"transport"`
	URL       string `yaml:"url" json:"url"`
	// Headers are sent with every request; a value may hold ${secret:NAME} references.
	Headers map[string]string `yaml:"headers" json:"headers,omitempty"`
	// DenyTools are tool names of this server the agent may not call (Claude: --disallowedTools
	// mcp__<server>__<tool>).
	DenyTools []string `yaml:"deny_tools" json:"deny_tools,omitempty"`
}

// SecretRef matches one ${secret:NAME} reference; NAME is an environment-variable-style key of the node's
// secrets file.
var SecretRef = regexp.MustCompile(`\$\{secret:([A-Za-z_][A-Za-z0-9_]{0,127})\}`)

// Secrets lists the secret names the server's headers reference, sorted and unique.
func (s MCPServer) Secrets() []string {
	var out []string
	for _, v := range s.Headers {
		for _, m := range SecretRef.FindAllStringSubmatch(v, -1) {
			if !slices.Contains(out, m[1]) {
				out = append(out, m[1])
			}
		}
	}
	slices.Sort(out)
	return out
}

// ExpandSecrets replaces every ${secret:NAME} in v with value(NAME).
func ExpandSecrets(v string, value func(name string) string) string {
	return SecretRef.ReplaceAllStringFunc(v, func(ref string) string {
		return value(strings.TrimSuffix(strings.TrimPrefix(ref, "${secret:"), "}"))
	})
}

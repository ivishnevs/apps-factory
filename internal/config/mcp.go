package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"appfactory/internal/configfile"
	"appfactory/internal/protocol"
)

// External MCP servers (external-mcp-servers): a company declares them once in mcp_servers.yaml, agents opt
// in with spec.mcp. Header values that carry credentials name a node secret (${secret:NAME}); the value
// itself lives only in the node's secrets file, never in the catalog, Core or a run's config snapshot.

// ReservedMCPServer is the Factory MCP server's own name; no external server may take it.
const ReservedMCPServer = "factory"

// mcpToolName is an MCP tool name as Claude Code addresses it (mcp__<server>__<tool>).
var mcpToolName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// credentialHeader matches header names whose values are credentials: they must be ${secret:NAME} references.
var credentialHeader = regexp.MustCompile(`(?i)(auth|token|key|secret|cookie|password|credential|session)`)

// loadMCPServers reads <company dir>/mcp_servers.yaml (optional).
func loadMCPServers(root, dir string) (map[string]protocol.MCPServer, error) {
	p := filepath.Join(root, dir, "mcp_servers.yaml")
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return map[string]protocol.MCPServer{}, nil
	}
	var doc struct {
		MCPServers map[string]protocol.MCPServer `yaml:"mcp_servers"`
	}
	if err := configfile.Read(p, &doc); err != nil {
		return nil, err
	}
	for _, name := range sortedKeys(doc.MCPServers) {
		if err := validMCPServer(name, doc.MCPServers[name]); err != nil {
			return nil, fmt.Errorf("mcp_servers: %s: %w", name, err)
		}
	}
	if doc.MCPServers == nil {
		doc.MCPServers = map[string]protocol.MCPServer{}
	}
	return doc.MCPServers, nil
}

func validMCPServer(name string, s protocol.MCPServer) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("invalid name: lowercase letters, digits and '-'")
	}
	if name == ReservedMCPServer {
		return fmt.Errorf("the name %s is the Factory MCP server's", ReservedMCPServer)
	}
	if s.Transport != "http" {
		return fmt.Errorf("transport %q: only http is supported", s.Transport)
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Host == "" || u.User != nil || strings.Contains(s.URL, "${") {
		return fmt.Errorf("url %q: an absolute http(s) URL without user info or references", s.URL)
	}
	switch u.Scheme {
	case "https":
	case "http":
		// Plain HTTP only to this machine (a local proxy); credentials never cross the network in clear text.
		if h := u.Hostname(); h != "localhost" && !net.ParseIP(h).IsLoopback() {
			return fmt.Errorf("url %q: http is allowed only for a loopback host; use https", s.URL)
		}
	default:
		return fmt.Errorf("url %q: scheme must be https (or http on loopback)", s.URL)
	}
	for _, h := range sortedKeys(s.Headers) {
		v := s.Headers[h]
		if h == "" || strings.ContainsAny(h, " :\r\n") {
			return fmt.Errorf("invalid header name %q", h)
		}
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("header %s: line breaks are not allowed", h)
		}
		if strings.Count(v, "${") != len(protocol.SecretRef.FindAllString(v, -1)) {
			return fmt.Errorf("header %s: only ${secret:NAME} references are expanded (NAME: letters, digits, '_')", h)
		}
		if credentialHeader.MatchString(h) && !protocol.SecretRef.MatchString(v) {
			return fmt.Errorf("header %s carries a credential: its value must reference a node secret as ${secret:NAME}, never a literal", h)
		}
	}
	for _, t := range s.DenyTools {
		if !mcpToolName.MatchString(t) {
			return fmt.Errorf("deny_tools: invalid tool name %q", t)
		}
	}
	return nil
}

// agentMCP resolves the agent's spec.mcp against the company's servers; external MCP servers run on Claude only.
func (c *Catalog) agentMCP(a Agent) (map[string]protocol.MCPServer, error) {
	if len(a.Spec.MCP) == 0 {
		return nil, nil
	}
	if a.Spec.Runtime.Provider == string(protocol.RuntimeCodex) {
		return nil, fmt.Errorf("spec.mcp: external MCP servers are supported for claude agents only, not codex")
	}
	out := map[string]protocol.MCPServer{}
	for i, n := range a.Spec.MCP {
		s, ok := c.MCPServers[n]
		if !ok {
			return nil, fmt.Errorf("spec.mcp: unknown MCP server %q (declare it in mcp_servers.yaml)", n)
		}
		if slices.Contains(a.Spec.MCP[:i], n) {
			continue
		}
		out[n] = s
	}
	return out, nil
}

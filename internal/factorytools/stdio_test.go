package factorytools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestMCPStdioLifecycleAndToolErrors(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}
{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":3,"method":"tools/list"}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"denied","arguments":{}}}
{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"good","arguments":{}}}
{"jsonrpc":"2.0","id":6,"method":"bogus"}
`
	var out bytes.Buffer
	err := Serve(context.Background(), strings.NewReader(input), &out, func(context.Context) (any, error) { return Definitions(), nil }, func(_ context.Context, c Call) (any, error) {
		if c.Name == "denied" {
			return nil, fmt.Errorf("403 denied")
		}
		return map[string]string{"status": "ok"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 6 {
		t.Fatal(out.String())
	}
	for i, line := range lines {
		var m map[string]any
		if err = json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		if m["jsonrpc"] != "2.0" {
			t.Fatal(m)
		}
		if i == 0 || i == 5 {
			if m["error"] == nil {
				t.Fatal("expected protocol error", m)
			}
			continue
		}
		result := m["result"].(map[string]any)
		if i == 1 && result["protocolVersion"] != "2025-06-18" {
			t.Fatal(m)
		}
		if i == 2 {
			for _, raw := range result["tools"].([]any) {
				schema := raw.(map[string]any)["inputSchema"].(map[string]any)
				if schema["required"] == nil {
					t.Fatal("invalid required:null schema")
				}
			}
		}
		if i == 3 && result["isError"] != true {
			t.Fatal("tool error became protocol error", m)
		}
	}
}

func TestLargeMCPOutputIsBoundedAndExplicit(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"large","arguments":{}}}
`
	var out bytes.Buffer
	err := Serve(context.Background(), strings.NewReader(input), &out, func(context.Context) (any, error) { return Definitions(), nil }, func(context.Context, Call) (any, error) {
		return map[string]string{"content": strings.Repeat("\\\"日本語", 100000)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Len() > 256*1024 || !strings.Contains(out.String(), "response truncated at 64 KiB") {
		t.Fatal("unbounded or silent truncation", out.Len())
	}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatal("invalid MCP output")
		}
	}
}

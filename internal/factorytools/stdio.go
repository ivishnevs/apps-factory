package factorytools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"
)

// Serve runs newline-delimited MCP JSON-RPC on stdio. Only tools are advertised.
// Calls are serialized; each callback is responsible for its bounded context.
func Serve(ctx context.Context, in io.Reader, out io.Writer, list func(context.Context) (any, error), call func(context.Context, Call) (any, error)) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 65536), 1<<20)
	enc := json.NewEncoder(out)
	initialized := false
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			if err = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "invalid JSON"}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue
		}
		response := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		var result any
		var err error
		code := -32603
		switch {
		case req.JSONRPC != "2.0":
			code = -32600
			err = fmt.Errorf("JSON-RPC 2.0 required")
		case req.Method == "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			version := p.ProtocolVersion
			if version != "2024-11-05" && version != "2025-03-26" && version != "2025-06-18" {
				version = "2025-06-18"
			}
			result = map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "factory", "version": "0.1.0"}}
			initialized = true
		case !initialized:
			code = -32002
			err = fmt.Errorf("initialize first")
		case req.Method == "ping":
			result = map[string]any{}
		case req.Method == "tools/list":
			var defs any
			defs, err = list(ctx)
			result = map[string]any{"tools": defs}
		case req.Method == "tools/call":
			var p Call
			if e := json.Unmarshal(req.Params, &p); e != nil {
				code = -32602
				err = e
				break
			}
			value, e := call(ctx, p)
			if e != nil {
				result = map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": boundedText(e.Error())}}}
			} else {
				b, e := json.Marshal(value)
				err = e
				result = map[string]any{"content": []any{map[string]string{"type": "text", "text": boundedText(string(b))}}}
			}
		default:
			code = -32601
			err = fmt.Errorf("method not found")
		}
		if err != nil {
			response["error"] = map[string]any{"code": code, "message": err.Error()}
		} else {
			response["result"] = result
		}
		if err = enc.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// Keep large tool content below runtime event/broker limits. Domain data stays in Core.
func boundedText(s string) string {
	const limit = 64 * 1024
	if len(s) <= limit {
		return s
	}
	end := limit
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "\n[Factory MCP response truncated at 64 KiB; narrow the query. Full data remains available through the owner API.]"
}

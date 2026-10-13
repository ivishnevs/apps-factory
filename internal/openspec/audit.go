package openspec

import (
	"bytes"
	"encoding/json"
)

// AuditData omits document bodies returned by the OpenSpec read tool from runtime
// telemetry. The runtime still receives the original response; files remain the source.
// This is not a general-purpose filter for arbitrary trusted shell/model output.
func AuditData(raw json.RawMessage) json.RawMessage {
	if !bytes.Contains(raw, []byte("factory_openspec_read")) {
		return raw
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return raw
	}
	var scrub func(any)
	scrub = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				scrub(item)
			}
		case map[string]any:
			tool, _ := x["tool"].(string)
			server, _ := x["server"].(string)
			if invocation, ok := x["invocation"].(map[string]any); ok {
				tool, _ = invocation["tool"].(string)
				server, _ = invocation["server"].(string)
			}
			if tool == "factory_openspec_read" && (server == "factory" || server == "") {
				if result, ok := x["result"].(map[string]any); ok {
					if result["document_body_omitted"] != true {
						x["result"] = readMetadata(result)
					}
				}
			}
			for _, item := range x {
				scrub(item)
			}
		}
	}
	scrub(value)
	b, e := json.Marshal(value)
	if e != nil {
		return raw
	}
	return b
}
func readMetadata(result map[string]any) map[string]any {
	out := map[string]any{"document_body_omitted": true}
	if ok, exists := result["Ok"].(map[string]any); exists {
		result = ok
	}
	if content, ok := result["content"].([]any); ok {
		for _, block := range content {
			b, ok := block.(map[string]any)
			if !ok {
				continue
			}
			text, _ := b["text"].(string)
			var d Document
			if json.Unmarshal([]byte(text), &d) == nil && d.Path != "" && len(d.SHA256) == 64 {
				out["path"] = d.Path
				out["sha256"] = d.SHA256
				out["offset"] = d.Offset
				out["next_offset"] = d.NextOffset
				out["has_more"] = d.HasMore
				out["bytes"] = d.Bytes
				break
			}
		}
	}
	return out
}

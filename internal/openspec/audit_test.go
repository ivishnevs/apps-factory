package openspec

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReadBodiesOmittedFromRuntimeAudit(t *testing.T) {
	doc, _ := json.Marshal(Document{Path: "changes/feature/proposal.md", SHA256: strings.Repeat("a", 64), Content: "FULL_PRODUCT_DOCUMENT", Bytes: 21, NextOffset: 21})
	result := map[string]any{"content": []any{map[string]any{"type": "text", "text": string(doc)}}}
	for _, item := range []any{
		map[string]any{"type": "mcpToolCall", "tool": "factory_openspec_read", "server": "factory", "result": result},
		map[string]any{"invocation": map[string]any{"tool": "factory_openspec_read", "server": "factory"}, "result": map[string]any{"Ok": result}},
	} {
		raw, _ := json.Marshal(map[string]any{"turn": map[string]any{"items": []any{item}}})
		got := AuditData(raw)
		if strings.Contains(string(got), "FULL_PRODUCT_DOCUMENT") || !strings.Contains(string(got), "document_body_omitted") || !strings.Contains(string(got), "proposal.md") {
			t.Fatal(string(got))
		}
		if string(AuditData(got)) != string(got) {
			t.Fatal("not idempotent")
		}
	}
	raw := json.RawMessage(`{"item":{"tool":"factory_report_get","server":"factory","result":{"text":"preserved"}}}`)
	if string(AuditData(raw)) != string(raw) {
		t.Fatal("unrelated output changed")
	}
}

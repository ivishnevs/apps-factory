package core

import (
	"context"
	"strings"
	"testing"

	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// TestStrictDelegation (people-topics D17): an agent without owner in can_message is not refused but routed: its
// question to the owner goes to its manager, and its artifact approval that would reach people is its manager's to
// decide; a brief is not asked of it, since nothing reaches people.
func TestStrictDelegation(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	demo := h.c.Agents["demo"]
	demo.Spec.Communication.CanMessage = []string{"manager"}
	h.c.Agents["demo"] = demo
	h.create("t", "notes", "demo")
	r := h.start("t")
	v := h.mcp(r, "factory_owner_ask", map[string]any{"body": "Which store region first?", "options": []any{"EU", "US"}, "request_id": "q"})
	if v["recipient"] != "manager" {
		t.Fatalf("question went to %v", v["recipient"])
	}
	ref := tasks.Artifact{ID: "mock", Version: "v1", URI: "https://example.com/mock-v1.png"}
	h.mcp(r, "factory_artifact_register", map[string]any{"artifact": ref, "request_id": "reg"})
	a := h.mcp(r, "factory_artifact_request_approval", map[string]any{"artifact_id": "mock", "artifact_version": "v1", "reason": "v1", "request_id": "ap"})
	if a["delegate"] != "manager" || !strings.Contains(a["setting"].(string), "strict delegation") {
		t.Fatalf("approval %+v", a)
	}
	if items, _ := h.s.Inbox(ctx, storage.DefaultCompany, ""); len(items) != 0 {
		for _, m := range items {
			if m.DelegatedTo == "" {
				t.Fatalf("reached people: %+v", m)
			}
		}
	}
}

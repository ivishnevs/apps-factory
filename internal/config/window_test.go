package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// work-and-conversation-runs 1.1, 2.1: conversation limits and the context window per run kind.
func TestConversationLimitsAndWindow(t *testing.T) {
	root := fixture(t)
	base := mustRead(t, root, "configs/profiles/base.yaml")
	for extra, ok := range map[string]bool{
		"    conversation:\n      max_concurrent_runs: 2\n":                                       true,
		"    conversation:\n      share_work_limit: true\n":                                       true,
		"    conversation:\n      max_concurrent_runs: 9\n":                                       false,
		"    conversation:\n      max_concurrent_runs: -1\n":                                      false,
		"    conversation:\n      max_concurrent_runs: 1\n      unknown: 1\n":                     false,
		"  runtime:\n    provider: codex\n    context_window: 200k\n":                             true,
		"  runtime:\n    provider: codex\n    context_window: 50k\n":                              false,
		"  runtime:\n    provider: codex\n    context_window: 2M\n":                               false,
		"  runtime:\n    provider: codex\n    context_window: lots\n":                             false,
		"  runtime:\n    provider: codex\n    context_window: {work: max}\n":                      true,
		"  runtime:\n    provider: codex\n    context_window: {other: 200k}\n":                    false,
		"  runtime:\n    provider: codex\n    context_window: [200k]\n":                           false,
		"  runtime:\n    provider: codex\n    context_window: {work: 1M, conversation: 200000}\n": true,
	} {
		doc := base
		if strings.HasPrefix(extra, "  runtime:") {
			doc = strings.Replace(base, "  runtime:\n    provider: codex\n", extra, 1)
		} else {
			doc = strings.Replace(base, "    max_retries: 3\n", "    max_retries: 3\n"+extra, 1)
		}
		put(t, root, "configs/profiles/base.yaml", doc)
		if _, err := LoadCatalog(root); (err == nil) != ok {
			t.Fatalf("%q: %v", extra, err)
		}
	}
	put(t, root, "configs/profiles/base.yaml", strings.Replace(strings.Replace(base, "    provider: codex\n", "    provider: codex\n    context_window: {work: max, conversation: 200k}\n", 1),
		"    max_retries: 3\n", "    max_retries: 3\n    conversation:\n      max_concurrent_runs: 2\n", 1))
	c, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	a := c.Agents["agent"]
	if a.Spec.Execution.Conversation.MaxConcurrentRuns != 2 || a.Spec.Runtime.ContextWindow.Tokens(false) != 0 || a.Spec.Runtime.ContextWindow.Tokens(true) != 200_000 {
		t.Fatalf("agent %+v %+v", a.Spec.Execution.Conversation, a.Spec.Runtime.ContextWindow)
	}
	if o := a.RunOptions(true); o["context_window"] != 200_000 {
		t.Fatalf("conversation options %v", o)
	}
	if o := a.RunOptions(false); o["context_window"] != nil {
		t.Fatalf("work options %v (max: the runtime's default)", o)
	}
	// The run snapshot round trip (JSON) and back through the catalog editor's YAML keep both values.
	b, _ := json.Marshal(a)
	if !strings.Contains(string(b), `"context_window":{"work":"max","conversation":"200k"}`) || !strings.Contains(string(b), `"conversation":{"max_concurrent_runs":2}`) {
		t.Fatalf("snapshot %s", b)
	}
	var back Agent
	if err = json.Unmarshal(b, &back); err != nil || back.Spec.Runtime.ContextWindow != a.Spec.Runtime.ContextWindow || back.Spec.Execution.Conversation != a.Spec.Execution.Conversation {
		t.Fatalf("round trip %+v %v", back.Spec.Runtime.ContextWindow, err)
	}
}

func TestContextWindowShapes(t *testing.T) {
	for in, want := range map[string]ContextWindow{
		`"200k"`:                               {Work: "200k", Conversation: "200k"},
		`400000`:                               {Work: "400000", Conversation: "400000"},
		`{"work":"max","conversation":200000}`: {Work: "max", Conversation: "200000"},
		`null`:                                 {},
	} {
		var w ContextWindow
		if err := json.Unmarshal([]byte(in), &w); err != nil || w != want {
			t.Fatalf("%s: %+v %v", in, w, err)
		}
	}
	for in, want := range map[string]int{"max": 0, "": 0, "200k": 200_000, "1M": 1_000_000, "1.5M": 1_500_000, "300000": 300_000} {
		if n, err := ParseWindow(in); err != nil || n != want {
			t.Fatalf("%q: %d %v", in, n, err)
		}
	}
	if _, err := ParseWindow("-5k"); err == nil {
		t.Fatal("negative window accepted")
	}
}

package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/driver"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/rpc"
)

var (
	_ node.DriverProbe = Driver{}
	_ driver.Driver    = Driver{}
)

func TestOptionsRegistered(t *testing.T) {
	if !slices.Equal(config.RuntimeOptions[protocol.RuntimeCodex], Options) {
		t.Fatalf("config.RuntimeOptions[codex] = %v, driver reads %v", config.RuntimeOptions[protocol.RuntimeCodex], Options)
	}
}

// TestFakeAppServer is a fake app-server for a resumed thread: token usage updates (one for
// another turn), an exhausted rate-limit window and a usageLimitExceeded failure.
func TestFakeAppServer(t *testing.T) {
	if os.Getenv("FACTORY_FAKE_CODEX") != "1" {
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	notify := func(method, params string) {
		_ = enc.Encode(map[string]any{"method": method, "params": json.RawMessage(params)})
	}
	for sc.Scan() {
		var m rpc.Message
		if json.Unmarshal(sc.Bytes(), &m) != nil || len(m.ID) == 0 {
			continue
		}
		var result any = map[string]any{}
		switch m.Method {
		case "account/read":
			result = map[string]any{"account": map[string]string{"type": "chatgpt"}}
		case "thread/start", "thread/resume":
			result = map[string]any{"thread": map[string]string{"id": "thread-1"}, "model": "gpt-5.5-codex"}
		case "turn/start":
			result = map[string]any{"turn": map[string]string{"id": "turn-2"}}
		}
		_ = enc.Encode(map[string]any{"id": m.ID, "result": result})
		if m.Method != "turn/start" {
			continue
		}
		notify("thread/tokenUsage/updated", `{"threadId":"thread-1","turnId":"turn-1","tokenUsage":{"total":{"inputTokens":9,"cachedInputTokens":0,"outputTokens":9,"reasoningOutputTokens":0,"totalTokens":18},"last":{"inputTokens":9,"cachedInputTokens":0,"outputTokens":9,"reasoningOutputTokens":0,"totalTokens":18}}}`)
		notify("thread/tokenUsage/updated", `{"threadId":"thread-1","turnId":"turn-2","tokenUsage":{"total":{"inputTokens":1000,"cachedInputTokens":400,"outputTokens":100,"reasoningOutputTokens":20,"totalTokens":1100},"last":{"inputTokens":300,"cachedInputTokens":100,"outputTokens":50,"reasoningOutputTokens":10,"totalTokens":350},"modelContextWindow":200000}}`)
		notify("thread/tokenUsage/updated", `{"threadId":"thread-1","turnId":"turn-2","tokenUsage":{"total":{"inputTokens":1500,"cachedInputTokens":600,"outputTokens":180,"reasoningOutputTokens":30,"totalTokens":1680},"last":{"inputTokens":500,"cachedInputTokens":200,"outputTokens":80,"reasoningOutputTokens":10,"totalTokens":580}}}`)
		notify("account/rateLimits/updated", `{"rateLimits":{"primary":{"usedPercent":100,"windowDurationMins":300,"resetsAt":1792018800},"secondary":{"usedPercent":40,"resetsAt":1792500000},"rateLimitReachedType":"rate_limit_reached"}}`)
		notify("account/rateLimits/updated", `{"rateLimits":{"secondary":{"usedPercent":41,"resetsAt":1792500000}}}`)
		notify("error", `{"threadId":"thread-1","turnId":"turn-2","willRetry":false,"error":{"message":"You've hit your usage limit.","codexErrorInfo":"usageLimitExceeded"}}`)
		notify("turn/completed", `{"threadId":"thread-1","turn":{"id":"turn-2","status":"failed","error":{"message":"You've hit your usage limit.","codexErrorInfo":"usageLimitExceeded"}}}`)
	}
	os.Exit(0)
}

func TestUsageAndQuota(t *testing.T) {
	t.Setenv("FACTORY_FAKE_CODEX", "1")
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := Driver{Binary: bin, Args: []string{"-test.run=^TestFakeAppServer$"}}.Launch(ctx, driver.Launch{Spec: protocol.RunSpec{Prompt: "work", SessionID: "thread-1"}, Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var usage []protocol.Usage
	var quotas []protocol.Quota
	kinds := map[driver.EventKind]int{}
	for e := range s.Events() {
		kinds[e.Kind]++
		if e.Usage != nil {
			usage = append(usage, *e.Usage)
		}
		if e.Quota != nil {
			quotas = append(quotas, *e.Quota)
		}
	}
	if r := <-s.Done(); r.Status != "FAILED" {
		t.Fatalf("%+v", r)
	}
	// Another turn's update stays activity; this run counts from its own baseline.
	want := []protocol.Usage{{Model: "gpt-5.5-codex", Input: 200, Output: 50, CacheRead: 100}, {Model: "gpt-5.5-codex", Input: 500, Output: 130, CacheRead: 300}}
	if !slices.Equal(usage, want) || kinds[driver.EventActivity] != 5 {
		t.Fatalf("usage %+v, kinds %v", usage, kinds)
	}
	if len(quotas) != 1 || !quotas[0].ResetsAt.Equal(time.Unix(1792018800, 0)) || quotas[0].Type != "rate_limit_reached" {
		t.Fatalf("quota %+v", quotas)
	}
}

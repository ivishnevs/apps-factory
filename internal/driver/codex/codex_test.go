package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
		if log := os.Getenv("FACTORY_FAKE_CODEX_LOG"); log != "" {
			f, _ := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			_ = json.NewEncoder(f).Encode(map[string]any{"method": m.Method, "params": m.Params})
			f.Close()
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

// TestFakeExitingAppServer completes the turn and exits at once.
func TestFakeExitingAppServer(t *testing.T) {
	if os.Getenv("FACTORY_FAKE_CODEX") != "exit" {
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var m rpc.Message
		if json.Unmarshal(sc.Bytes(), &m) != nil || len(m.ID) == 0 {
			continue
		}
		var result any = map[string]any{}
		switch m.Method {
		case "account/read":
			result = map[string]any{"account": map[string]string{"type": "chatgpt"}}
		case "thread/start":
			result = map[string]any{"thread": map[string]string{"id": "thread-1"}}
		case "turn/start":
			result = map[string]any{"turn": map[string]string{"id": "turn-1"}}
		}
		_ = enc.Encode(map[string]any{"id": m.ID, "result": result})
		if m.Method == "turn/start" {
			for i := range 300 {
				_ = enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]string{"type": "agentMessage", "text": fmt.Sprint(i)}}})
			}
			_ = enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]string{"id": "turn-1", "status": "completed"}}})
			os.Exit(0)
		}
	}
	os.Exit(0)
}

// TestTurnCompletedBeforeExitIsTerminal: a turn/completed buffered before the app-server exited is
// terminal evidence; the disconnect must not win over it.
func TestTurnCompletedBeforeExitIsTerminal(t *testing.T) {
	t.Setenv("FACTORY_FAKE_CODEX", "exit")
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		s, err := Driver{Binary: bin, Args: []string{"-test.run=^TestFakeExitingAppServer$"}}.Launch(ctx, driver.Launch{Spec: protocol.RunSpec{Prompt: "work"}, Cwd: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Start(ctx); err != nil {
			t.Fatal(err)
		}
		// A slow consumer: the server has exited before the loop reads its last lines.
		time.Sleep(200 * time.Millisecond)
		for range s.Events() {
		}
		r := <-s.Done()
		s.Close()
		cancel()
		if r.Status != "COMPLETED" {
			t.Fatalf("result %+v; want COMPLETED", r)
		}
	}
}

// TestSkillsExtraRoot: the run's materialized skills root is set before the thread starts.
func TestSkillsExtraRoot(t *testing.T) {
	t.Setenv("FACTORY_FAKE_CODEX", "1")
	log := filepath.Join(t.TempDir(), "calls.jsonl")
	t.Setenv("FACTORY_FAKE_CODEX_LOG", log)
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	s, err := Driver{Binary: bin, Args: []string{"-test.run=^TestFakeAppServer$"}}.Launch(ctx, driver.Launch{Spec: protocol.RunSpec{Prompt: "work"}, Cwd: t.TempDir(),
		Skills: []driver.Skill{{Name: "openspec", Hash: "sha256:x"}}, SkillsRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	var roots []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var c struct {
			Method string `json:"method"`
			Params struct {
				ExtraRoots []string `json:"extraRoots"`
			} `json:"params"`
		}
		if err = json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		methods = append(methods, c.Method)
		if c.Method == "skills/extraRoots/set" {
			roots = c.Params.ExtraRoots
		}
	}
	if !slices.Equal(methods, []string{"initialize", "account/read", "skills/extraRoots/set", "thread/start"}) || !slices.Equal(roots, []string{root}) {
		t.Fatalf("calls %v, roots %v", methods, roots)
	}
}

// TestModelAndEffort: runtime_options model goes to thread/start, effort to the run's turn/start (the
// app-server keeps it for the thread's later turns); without effort none is sent.
func TestModelAndEffort(t *testing.T) {
	t.Setenv("FACTORY_FAKE_CODEX", "1")
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, effort := range []string{"low", ""} {
		log := filepath.Join(t.TempDir(), "calls.jsonl")
		t.Setenv("FACTORY_FAKE_CODEX_LOG", log)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		options := map[string]any{"model": "gpt-6-luna"}
		if effort != "" {
			options["effort"] = effort
		}
		s, err := Driver{Binary: bin, Args: []string{"-test.run=^TestFakeAppServer$"}}.Launch(ctx, driver.Launch{Spec: protocol.RunSpec{Prompt: "work", RuntimeOptions: options}, Cwd: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Start(ctx); err != nil {
			t.Fatal(err)
		}
		for range s.Events() {
		}
		<-s.Done()
		s.Close()
		cancel()
		b, err := os.ReadFile(log)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]map[string]any{}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			var c struct {
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if err = json.Unmarshal([]byte(line), &c); err != nil {
				t.Fatal(err)
			}
			got[c.Method] = c.Params
		}
		if got["thread/start"]["model"] != "gpt-6-luna" {
			t.Fatal("thread/start", got["thread/start"])
		}
		if e, ok := got["turn/start"]["effort"]; effort == "" && ok || effort != "" && e != effort {
			t.Fatalf("effort %q: turn/start %v", effort, got["turn/start"])
		}
	}
}

// TestFakeTurnsServer completes every turn at once and reports how many turns it saw.
func TestFakeTurnsServer(t *testing.T) {
	if os.Getenv("FACTORY_FAKE_CODEX") != "turns" {
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	turns := 0
	for sc.Scan() {
		var m rpc.Message
		if json.Unmarshal(sc.Bytes(), &m) != nil || len(m.ID) == 0 {
			continue
		}
		var result any = map[string]any{}
		switch m.Method {
		case "account/read":
			result = map[string]any{"account": map[string]string{"type": "chatgpt"}}
		case "thread/start":
			result = map[string]any{"thread": map[string]string{"id": "thread-1"}}
		case "turn/start":
			turns++
			result = map[string]any{"turn": map[string]string{"id": fmt.Sprint("turn-", turns)}}
		}
		_ = enc.Encode(map[string]any{"id": m.ID, "result": result})
		if m.Method == "turn/start" {
			_ = enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]string{"type": "agentMessage", "text": fmt.Sprint("turn ", turns)}}})
			_ = enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]string{"id": fmt.Sprint("turn-", turns), "status": "completed"}}})
		}
	}
	os.Exit(0)
}

// TestExitGuard: a completed turn without an exit gets follow-up turns (at most ExitGuardRetries); the
// guard stops as soon as the exit exists.
func TestExitGuard(t *testing.T) {
	t.Setenv("FACTORY_FAKE_CODEX", "turns")
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(exitAfter int) (driver.Result, []string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		checks := 0
		s, err := Driver{Binary: bin, Args: []string{"-test.run=^TestFakeTurnsServer$"}}.Launch(ctx, driver.Launch{Spec: protocol.RunSpec{Prompt: "work"}, Cwd: t.TempDir(),
			ExitGuard: func() bool { checks++; return checks > exitAfter }, ExitGuardRetries: 2})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err = s.Start(ctx); err != nil {
			t.Fatal(err)
		}
		var guards []string
		for e := range s.Events() {
			if e.Method == "factory/exit_guard" {
				guards = append(guards, string(e.Data))
			}
		}
		return <-s.Done(), guards
	}
	if r, g := run(1); r.Status != "COMPLETED" || len(g) != 1 || !strings.Contains(r.Text, "turn 2") {
		t.Fatalf("exit on the follow-up: %+v %v", r, g)
	}
	if r, g := run(100); r.Status != "COMPLETED" || len(g) != 2 || !strings.Contains(r.Text, "turn 3") {
		t.Fatalf("bounded follow-ups: %+v %v", r, g)
	}
}

// TestFakeSlowFollowUpServer completes the first turn at once; a later turn/start answers after 300 ms and
// its turn ends only when turn/interrupt names it.
func TestFakeSlowFollowUpServer(t *testing.T) {
	if os.Getenv("FACTORY_FAKE_CODEX") != "slow-follow-up" {
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	turns := 0
	for sc.Scan() {
		var m rpc.Message
		if json.Unmarshal(sc.Bytes(), &m) != nil || len(m.ID) == 0 {
			continue
		}
		var result any = map[string]any{}
		switch m.Method {
		case "account/read":
			result = map[string]any{"account": map[string]string{"type": "chatgpt"}}
		case "thread/start":
			result = map[string]any{"thread": map[string]string{"id": "thread-1"}}
		case "turn/start":
			turns++
			if turns > 1 {
				time.Sleep(300 * time.Millisecond)
			}
			result = map[string]any{"turn": map[string]string{"id": fmt.Sprint("turn-", turns)}}
		}
		_ = enc.Encode(map[string]any{"id": m.ID, "result": result})
		switch {
		case m.Method == "turn/start" && turns == 1:
			_ = enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]string{"id": "turn-1", "status": "completed"}}})
		case m.Method == "turn/interrupt":
			var p struct {
				TurnID string `json:"turnId"`
			}
			if json.Unmarshal(m.Params, &p) == nil && p.TurnID == fmt.Sprint("turn-", turns) {
				_ = enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]string{"id": p.TurnID, "status": "interrupted"}}})
			}
		}
	}
	os.Exit(0)
}

// TestExitGuardInterrupt: an interrupt that arrives while the exit guard starts its follow-up turn
// interrupts that turn (not the finished one): the run ends CANCELLED instead of running on.
func TestExitGuardInterrupt(t *testing.T) {
	t.Setenv("FACTORY_FAKE_CODEX", "slow-follow-up")
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	guarding := make(chan struct{}, 1)
	s, err := Driver{Binary: bin, Args: []string{"-test.run=^TestFakeSlowFollowUpServer$"}}.Launch(ctx, driver.Launch{Spec: protocol.RunSpec{Prompt: "work"}, Cwd: t.TempDir(),
		ExitGuard: func() bool { guarding <- struct{}{}; return false }, ExitGuardRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	go func() {
		for range s.Events() {
		}
	}()
	<-guarding // the follow-up's turn/start is on its way
	time.Sleep(100 * time.Millisecond)
	if err = s.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-s.Done():
		if r.Status != "CANCELLED" {
			t.Fatalf("result %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the follow-up turn was not interrupted")
	}
}

package fake

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"appfactory/internal/driver"
)

func TestScriptedToolCalls(t *testing.T) {
	d := &Driver{Script: Script{Status: "COMPLETED", Activity: []Activity{
		{Tool: &ToolCall{Name: "factory_task_get", Arguments: map[string]any{"id": "t1"}}},
		{Method: "custom", Tool: &ToolCall{Name: "factory_task_list"}},
	}}, CallTool: func(_ context.Context, l driver.Launch, c ToolCall) (json.RawMessage, error) {
		if l.Cwd != "/w" {
			t.Errorf("launch %+v", l)
		}
		if c.Name == "factory_task_list" {
			return nil, errors.New("denied")
		}
		return json.RawMessage(`{"content":[]}`), nil
	}}
	s, err := d.Launch(context.Background(), driver.Launch{Cwd: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got []string
	for e := range s.Events() {
		got = append(got, e.Method+" "+string(e.Data))
	}
	want := []string{
		ToolMethod + ` {"name":"factory_task_get","arguments":{"id":"t1"},"result":{"content":[]}}`,
		`custom {"name":"factory_task_list","arguments":null,"error":"denied"}`,
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("events %q", got)
	}
	if r := <-s.Done(); r.Status != "COMPLETED" {
		t.Fatal(r)
	}
}

// The exit guard refuses a stop without an exit at most ExitGuardRetries times; an exit ends it.
func TestExitGuard(t *testing.T) {
	for _, c := range []struct {
		exitAfter, want int
	}{{exitAfter: 99, want: 2}, {exitAfter: 1, want: 1}, {exitAfter: 0, want: 0}} {
		checks := 0
		d := &Driver{ExitGuard: true, Script: Script{Status: "COMPLETED"}}
		s, err := d.Launch(context.Background(), driver.Launch{ExitGuardRetries: 2, ExitGuard: func() bool { checks++; return checks > c.exitAfter }})
		if err != nil || !d.Capabilities().ExitGuard {
			t.Fatal(err)
		}
		_ = s.Start(context.Background())
		refusals := 0
		for e := range s.Events() {
			if e.Method == ExitGuardMethod {
				refusals++
			}
		}
		if r := <-s.Done(); r.Status != "COMPLETED" || refusals != c.want {
			t.Fatalf("exit after %d checks: %d refusals, %+v", c.exitAfter, refusals, r)
		}
	}
}

func TestStdioCallWithoutServer(t *testing.T) {
	if _, err := StdioCall(context.Background(), driver.Launch{}, ToolCall{Name: "factory_project_get"}); err == nil {
		t.Fatal("call without MCP config succeeded")
	}
}

func TestScriptedUsageCostQuota(t *testing.T) {
	var sc Script
	if err := json.Unmarshal([]byte(`{"status":"FAILED","activity":[
		{"usage":{"model":"m","input":1,"output":2}},
		{"method":"x/cost","cost":{"usd":0.5,"source":"claude_estimate"}},
		{"quota":{"resets_at":"2026-10-14T05:00:00Z","type":"five_hour"}}]}`), &sc); err != nil {
		t.Fatal(err)
	}
	s, err := (&Driver{Script: sc}).Launch(context.Background(), driver.Launch{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got []driver.Event
	for e := range s.Events() {
		got = append(got, e)
	}
	if len(got) != 3 || got[0].Kind != driver.EventUsage || got[0].Method != "fake/usage" || got[0].Usage.Output != 2 ||
		got[1].Kind != driver.EventCost || got[1].Method != "x/cost" || got[1].Cost.USD != 0.5 ||
		got[2].Kind != driver.EventQuota || got[2].Quota.Type != "five_hour" || got[2].Quota.ResetsAt.Hour() != 5 {
		t.Fatalf("events %+v", got)
	}
}

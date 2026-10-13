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

func TestStdioCallWithoutServer(t *testing.T) {
	if _, err := StdioCall(context.Background(), driver.Launch{}, ToolCall{Name: "factory_project_get"}); err == nil {
		t.Fatal("call without MCP config succeeded")
	}
}

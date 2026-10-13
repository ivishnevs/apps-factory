// Package fake replays scripted runtime sessions so executor tests run without a model.
package fake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"appfactory/internal/driver"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
)

// Script is a recorded session. Without Status the session runs until interrupted and then
// ends CANCELLED; with Unknown it ends without terminal evidence.
type Script struct {
	SessionID string     `json:"session_id"`
	Activity  []Activity `json:"activity"`
	Status    string     `json:"status"`
	Text      string     `json:"text"`
	Unknown   string     `json:"unknown"`
	LaunchErr string     `json:"launch_error"`
}

// Activity is one event. With Tool the session performs that Factory MCP call and the event's
// Data is the exchange (ToolEvent); Method defaults to ToolMethod.
type Activity struct {
	Method string          `json:"method"`
	Data   json.RawMessage `json:"data"`
	Tool   *ToolCall       `json:"tool,omitempty"`
}
type ToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

const ToolMethod = "fake/mcp/tool_call"

// ToolEvent is the Data of a scripted tool call: the MCP tools/call result or the call error.
type ToolEvent struct {
	ToolCall
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func Load(path string) (Script, error) {
	var s Script
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	return s, err
}

var _ driver.Driver = (*Driver)(nil)

type Driver struct {
	Provider protocol.RuntimeProvider
	Script   Script
	MCP      bool
	// Checks is the Probe result; nil passes.
	Checks []node.CheckResult
	// CallTool performs scripted tool calls; nil uses StdioCall.
	CallTool func(ctx context.Context, l driver.Launch, c ToolCall) (json.RawMessage, error)

	mu       sync.Mutex
	launches []driver.Launch
}

func (d *Driver) Name() protocol.RuntimeProvider {
	if d.Provider == "" {
		return protocol.RuntimeCodex
	}
	return d.Provider
}
func (d *Driver) Capabilities() driver.Capabilities {
	return driver.Capabilities{Resume: true, MCP: d.MCP, Activity: true}
}
func (d *Driver) CheckVersion(context.Context) (string, error) { return "fake", nil }
func (d *Driver) Installed() bool                              { return true }
func (d *Driver) Probe(context.Context) []node.CheckResult {
	if d.Checks != nil {
		return d.Checks
	}
	return []node.CheckResult{{Check: string(d.Name()) + ".fake", Status: node.Pass, Detail: "fake"}}
}

// Launches returns the launch parameters seen so far.
func (d *Driver) Launches() []driver.Launch {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]driver.Launch{}, d.launches...)
}
func (d *Driver) Launch(_ context.Context, l driver.Launch) (driver.Session, error) {
	d.mu.Lock()
	d.launches = append(d.launches, l)
	d.mu.Unlock()
	if d.Script.LaunchErr != "" {
		return nil, errors.New(d.Script.LaunchErr)
	}
	id := d.Script.SessionID
	if id == "" {
		id = l.Spec.SessionID
	}
	if id == "" {
		id = "fake-session"
	}
	call := d.CallTool
	if call == nil {
		call = StdioCall
	}
	return &session{script: d.Script, launch: l, call: call, id: id, events: make(chan driver.Event, len(d.Script.Activity)), done: make(chan driver.Result, 1), interrupt: make(chan struct{}), stop: make(chan struct{})}, nil
}

// StdioCall performs one tools/call through the Factory MCP stdio server of l.MCP as a runtime
// does: the server gets only the variables named in MCP.EnvVars, taken from l.Env.
func StdioCall(ctx context.Context, l driver.Launch, c ToolCall) (json.RawMessage, error) {
	if l.MCP == nil {
		return nil, errors.New("no Factory MCP server in launch")
	}
	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	for _, m := range []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18"}},
		{"jsonrpc": "2.0", "method": "notifications/initialized"},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": c},
	} {
		_ = enc.Encode(m)
	}
	env := l.Env
	if env == nil {
		env = os.Environ()
	}
	cmd := exec.CommandContext(ctx, l.MCP.Command)
	cmd.Env, cmd.Stdin = []string{}, &in
	for _, v := range env {
		k, _, _ := strings.Cut(v, "=")
		for _, name := range l.MCP.EnvVars {
			if k == name {
				cmd.Env = append(cmd.Env, v)
			}
		}
	}
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			err = fmt.Errorf("%w: %s", err, node.FirstLine(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("factory-mcp: %w", err)
	}
	for _, line := range bytes.Split(out, []byte("\n")) {
		var r struct {
			ID            json.RawMessage
			Result, Error json.RawMessage
		}
		if json.Unmarshal(line, &r) != nil || string(r.ID) != "2" {
			continue
		}
		if len(r.Error) > 0 {
			return nil, fmt.Errorf("tools/call: %s", r.Error)
		}
		return r.Result, nil
	}
	return nil, errors.New("factory-mcp: no tools/call response")
}

type session struct {
	script                  Script
	launch                  driver.Launch
	call                    func(context.Context, driver.Launch, ToolCall) (json.RawMessage, error)
	id                      string
	events                  chan driver.Event
	done                    chan driver.Result
	interrupt, stop         chan struct{}
	interruptOnce, stopOnce sync.Once
}

func (s *session) ID() string                  { return s.id }
func (s *session) Events() <-chan driver.Event { return s.events }
func (s *session) Done() <-chan driver.Result  { return s.done }
func (s *session) Start(ctx context.Context) error {
	go func() {
		for _, a := range s.script.Activity {
			if a.Tool != nil {
				a.Data = s.tool(ctx, *a.Tool)
				if a.Method == "" {
					a.Method = ToolMethod
				}
			}
			s.events <- driver.Event{Kind: driver.EventActivity, Method: a.Method, Data: a.Data}
		}
		r := s.result()
		close(s.events)
		s.done <- r
	}()
	return nil
}
func (s *session) tool(ctx context.Context, c ToolCall) json.RawMessage {
	e := ToolEvent{ToolCall: c}
	var err error
	if e.Result, err = s.call(ctx, s.launch, c); err != nil {
		e.Error = err.Error()
	}
	b, _ := json.Marshal(e)
	return b
}
func (s *session) result() driver.Result {
	switch {
	case s.script.Unknown != "":
		return driver.Result{Err: errors.New(s.script.Unknown)}
	case s.script.Status != "":
		return driver.Result{Status: s.script.Status, Text: s.script.Text}
	}
	select {
	case <-s.interrupt:
		return driver.Result{Status: "CANCELLED", Text: s.script.Text}
	case <-s.stop:
		return driver.Result{Err: errors.New("session closed")}
	}
}
func (s *session) Interrupt(context.Context) error {
	s.interruptOnce.Do(func() { close(s.interrupt) })
	return nil
}
func (s *session) ProcessGroup() int { return 0 }
func (s *session) Close()            { s.stopOnce.Do(func() { close(s.stop) }) }

// Package codex drives `codex app-server` over stdio: initialize → account/read →
// thread/start|resume → turn/start, finishing on turn/completed.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"appfactory/internal/driver"
	"appfactory/internal/protocol"
	"appfactory/internal/rpc"
)

const SupportedVersion = "codex-cli 0.162.1"

type Driver struct {
	Binary string
	// Args replace the default app-server arguments (tests use a fake server).
	Args []string
}

func (d Driver) Name() protocol.RuntimeProvider { return protocol.RuntimeCodex }
func (d Driver) Capabilities() driver.Capabilities {
	return driver.Capabilities{Resume: true, MCP: true, Activity: true}
}
func (d Driver) binary() string {
	if d.Binary == "" {
		return "codex"
	}
	return d.Binary
}
func (d Driver) CheckVersion(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, d.binary(), "--version").Output()
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(out))
	if v != SupportedVersion {
		return v, fmt.Errorf("unsupported Codex %q: expected 0.162.1; regenerate and verify schema before upgrade", v)
	}
	return v, nil
}

func (d Driver) Launch(ctx context.Context, l driver.Launch) (driver.Session, error) {
	args := d.Args
	if args == nil {
		args = []string{"app-server", "--listen", "stdio://", "-c", "model_provider=\"openai\""}
	}
	if l.MCP != nil {
		vars := make([]string, len(l.MCP.EnvVars))
		for i, v := range l.MCP.EnvVars {
			vars[i] = strconv.Quote(v)
		}
		args = append(append([]string{}, args...), "-c", "mcp_servers.factory.command="+strconv.Quote(l.MCP.Command), "-c", "mcp_servers.factory.env_vars=["+strings.Join(vars, ",")+"]", "-c", "mcp_servers.factory.required=true", "-c", "mcp_servers.factory.enabled=true")
	}
	// The runtime has its own lifetime so cancellation can first send turn/interrupt.
	runtimeCtx, stopRuntime := context.WithCancel(context.Background())
	client, err := rpc.StartEnv(runtimeCtx, d.binary(), args, l.Env)
	if err != nil {
		stopRuntime()
		return nil, err
	}
	s := &session{client: client, stopRuntime: stopRuntime, prompt: l.Spec.Prompt, events: make(chan driver.Event, 64), done: make(chan driver.Result, 1), stop: make(chan struct{})}
	if err = s.open(ctx, l); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

type session struct {
	client           *rpc.Client
	stopRuntime      context.CancelFunc
	prompt           string
	threadID, turnID string
	events           chan driver.Event
	done             chan driver.Result
	stop             chan struct{}
	closeOnce        sync.Once
	interrupted      atomic.Bool
}

func (s *session) call(ctx context.Context, method string, params, result any) error {
	callCtx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	return s.client.Call(callCtx, method, params, result)
}
func (s *session) open(ctx context.Context, l driver.Launch) error {
	if err := s.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "appfactory", "title": "App Factory", "version": "0.1.0"}}, nil); err != nil {
		return err
	}
	if err := s.client.Notify("initialized", map[string]any{}); err != nil {
		return err
	}
	var account struct {
		Account *struct {
			Type string `json:"type"`
		} `json:"account"`
	}
	if err := s.call(ctx, "account/read", map[string]bool{"refreshToken": false}, &account); err != nil {
		return err
	}
	if account.Account == nil || account.Account.Type != "chatgpt" {
		return fmt.Errorf("ChatGPT login required; API-key auth is not supported")
	}
	params := map[string]any{"cwd": l.Cwd, "approvalPolicy": "never", "sandbox": "danger-full-access", "developerInstructions": l.Instructions}
	if model, ok := l.Spec.RuntimeOptions["model"].(string); ok && model != "" && model != "default" {
		params["model"] = model
	}
	method := "thread/start"
	if l.Spec.SessionID != "" {
		method = "thread/resume"
		params["threadId"] = l.Spec.SessionID
		params["excludeTurns"] = true
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := s.call(ctx, method, params, &thread); err != nil {
		return err
	}
	if thread.Thread.ID == "" {
		return fmt.Errorf("missing thread ID")
	}
	s.threadID = thread.Thread.ID
	return nil
}

func (s *session) ProcessGroup() int           { return s.client.Pid() }
func (s *session) ID() string                  { return s.threadID }
func (s *session) Events() <-chan driver.Event { return s.events }
func (s *session) Done() <-chan driver.Result  { return s.done }
func (s *session) Start(ctx context.Context) error {
	var turn struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := s.call(ctx, "turn/start", map[string]any{"threadId": s.threadID, "input": []any{map[string]any{"type": "text", "text": s.prompt, "text_elements": []any{}}}}, &turn); err != nil {
		return err
	}
	if turn.Turn.ID == "" {
		return fmt.Errorf("missing turn ID")
	}
	s.turnID = turn.Turn.ID
	go func() {
		r := s.loop()
		close(s.events)
		s.done <- r
	}()
	return nil
}
func (s *session) Interrupt(ctx context.Context) error {
	s.interrupted.Store(true)
	// Acknowledgement only means interruption requested; terminal evidence follows.
	return s.client.Call(ctx, "turn/interrupt", map[string]string{"threadId": s.threadID, "turnId": s.turnID}, nil)
}
func (s *session) Close() {
	s.closeOnce.Do(func() {
		close(s.stop)
		s.client.Close()
		s.stopRuntime()
	})
}
func (s *session) send(e driver.Event) bool {
	select {
	case s.events <- e:
		return true
	case <-s.stop:
		return false
	}
}

func (s *session) loop() driver.Result {
	var text strings.Builder
	for {
		select {
		case <-s.stop:
			return driver.Result{Err: errors.New("session closed")}
		case <-s.client.Done():
			if s.interrupted.Load() {
				return driver.Result{Err: s.client.Err()}
			}
			return driver.Result{Err: fmt.Errorf("app-server disconnected: %w", s.client.Err())}
		case m := <-s.client.Events:
			if !s.send(driver.Event{Kind: driver.EventActivity, Method: m.Method, Data: m.Params}) {
				return driver.Result{Err: errors.New("session closed")}
			}
			if len(m.ID) > 0 {
				if s.interrupted.Load() {
					_ = s.client.Respond(m.ID, nil, map[string]any{"code": -32601, "message": "run interrupted"})
					continue
				}
				var err error
				switch m.Method {
				case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
					err = s.client.Respond(m.ID, map[string]string{"decision": "decline"}, nil)
				default:
					_ = s.client.Respond(m.ID, nil, map[string]any{"code": -32601, "message": "Foundation cannot service this request; owner intervention required"})
					return driver.Result{Err: fmt.Errorf("server request requires owner: %s", m.Method)}
				}
				if err != nil {
					return driver.Result{Err: err}
				}
				continue
			}
			switch m.Method {
			case "item/completed":
				var p struct {
					Item struct{ Type, Text string } `json:"item"`
				}
				if !s.interrupted.Load() && json.Unmarshal(m.Params, &p) == nil && p.Item.Type == "agentMessage" {
					if text.Len() > 0 {
						text.WriteString("\n")
					}
					text.WriteString(p.Item.Text)
				}
			case "turn/completed":
				return finish(m.Params, text.String())
			}
		}
	}
}
func finish(params json.RawMessage, text string) driver.Result {
	var p struct {
		Turn struct {
			Status string          `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return driver.Result{Err: err}
	}
	switch p.Turn.Status {
	case "completed":
		return driver.Result{Status: "COMPLETED", Text: text}
	case "failed":
		return driver.Result{Status: "FAILED", Text: text + "\n" + string(p.Turn.Error)}
	case "interrupted":
		return driver.Result{Status: "CANCELLED", Text: text}
	}
	return driver.Result{Err: fmt.Errorf("unknown turn status %q", p.Turn.Status)}
}

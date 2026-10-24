// Package codex drives `codex app-server` over stdio: initialize → account/read →
// thread/start|resume → turn/start, finishing on turn/completed. Usage comes from
// thread/tokenUsage/updated (tokens only, no cost); quota from a usageLimitExceeded turn error.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"appfactory/internal/driver"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/rpc"
)

// SupportedVersion is the only Codex app-server version whose schema is verified.
const SupportedVersion = "codex-cli 0.162.1"

// Options are the runtime_options keys the driver reads; config.RuntimeOptions must match.
var Options = []string{"model", "effort"}

type Driver struct {
	Binary string
	// Args replace the default app-server arguments (tests use a fake server).
	Args []string
	// Host runs Probe; nil is the local machine.
	Host *node.Host
}

func (d Driver) Name() protocol.RuntimeProvider { return protocol.RuntimeCodex }
func (d Driver) Capabilities() driver.Capabilities {
	return driver.Capabilities{Resume: true, MCP: true, Activity: true, ToolPolicy: true, ExitGuard: true}
}

// ResumePolicy resumes only a started session this node ran (driver.ResumeSameNode).
func (d Driver) ResumePolicy(prev driver.Outcome, sameNode bool) bool {
	return driver.ResumeSameNode(prev, sameNode)
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
	if len(l.ExternalMCP) > 0 {
		// external-mcp-servers: config validation refuses spec.mcp for codex agents; this is the backstop.
		return nil, fmt.Errorf("codex: external MCP servers are not supported")
	}
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
	s := &session{client: client, stopRuntime: stopRuntime, prompt: l.Spec.Prompt, guards: newGuards(l.Guards, l.Env), exitGuard: l.ExitGuard, guardLeft: l.ExitGuardRetries, events: make(chan driver.Event, 64), done: make(chan driver.Result, 1), stop: make(chan struct{})}
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
	// turnMu orders a follow-up turn's turnID with Interrupt, which runs outside the loop.
	turnMu      sync.Mutex
	events      chan driver.Event
	done        chan driver.Result
	stop        chan struct{}
	closeOnce   sync.Once
	interrupted atomic.Bool
	guards      *guards
	exitGuard   func() bool // see driver.Launch.ExitGuard
	guardLeft   int
	model       string
	effort      string  // runtime_options effort: reasoning effort sent with the run's first turn/start
	base        *tokens // thread total before this run's turn
	limits      rateLimits
	quotaSent   bool
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
		return loginRequired{}
	}
	// The run's skills, materialized and verified by the executor, become an extra skills root of this
	// app-server (one process per run). User and system skills of CODEX_HOME stay visible as before.
	if len(l.Skills) > 0 && l.SkillsRoot != "" {
		root, err := filepath.Abs(l.SkillsRoot)
		if err != nil {
			return err
		}
		if err = s.call(ctx, "skills/extraRoots/set", map[string]any{"extraRoots": []string{root}}, nil); err != nil {
			return fmt.Errorf("skills/extraRoots/set: %w", err)
		}
	}
	// Command rules need approval requests: "untrusted" asks for every command outside Codex's
	// known-safe read-only set (those still meet the PATH shims); without rules nothing asks.
	approval := "never"
	if s.guards.active() {
		approval = "untrusted"
	}
	params := map[string]any{"cwd": l.Cwd, "approvalPolicy": approval, "sandbox": "danger-full-access", "developerInstructions": l.Instructions}
	if model, ok := l.Spec.RuntimeOptions["model"].(string); ok && model != "" && model != "default" {
		params["model"] = model
	}
	if effort, ok := l.Spec.RuntimeOptions["effort"].(string); ok && effort != "" {
		s.effort = effort
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
		Model string `json:"model"`
	}
	if err := s.call(ctx, method, params, &thread); err != nil {
		return err
	}
	if thread.Thread.ID == "" {
		return fmt.Errorf("missing thread ID")
	}
	s.threadID, s.model = thread.Thread.ID, thread.Model
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
	params := map[string]any{"threadId": s.threadID, "input": []any{map[string]any{"type": "text", "text": s.prompt, "text_elements": []any{}}}}
	if s.effort != "" {
		params["effort"] = s.effort // this turn and the thread's later ones (exit guard follow-ups)
	}
	if err := s.call(ctx, "turn/start", params, &turn); err != nil {
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
	s.turnMu.Lock()
	turn := s.turnID
	s.turnMu.Unlock()
	// Acknowledgement only means interruption requested; terminal evidence follows.
	return s.client.Call(ctx, "turn/interrupt", map[string]string{"threadId": s.threadID, "turnId": turn}, nil)
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
			if len(s.client.Events) > 0 {
				continue // messages read before the disconnect come first (turn/completed is terminal evidence)
			}
			if s.interrupted.Load() {
				return driver.Result{Err: s.client.Err()}
			}
			return driver.Result{Err: fmt.Errorf("app-server disconnected: %w", s.client.Err())}
		case m := <-s.client.Events:
			ev := driver.Event{Kind: driver.EventActivity, Method: m.Method, Data: m.Params}
			var quota *protocol.Quota
			switch m.Method {
			case "thread/tokenUsage/updated":
				if u := s.usage(m.Params); u != nil {
					ev.Kind, ev.Usage = driver.EventUsage, u
				}
			case "account/rateLimits/updated":
				s.limits.merge(m.Params)
			case "error", "turn/completed":
				quota = s.quota(m.Method, m.Params)
			}
			if !s.send(ev) || quota != nil && !s.send(driver.Event{Kind: driver.EventQuota, Method: "codex/quota", Data: m.Params, Quota: quota}) {
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
					decision, note := s.guards.decide(context.Background(), m.Method, m.Params)
					if err = s.client.Respond(m.ID, map[string]string{"decision": decision}, nil); err == nil && note != "" {
						s.steer(note)
					}
				case "mcpServer/elicitation/request":
					err = s.client.Respond(m.ID, elicitationAnswer(m.Params), nil)
				default:
					_ = s.client.Respond(m.ID, nil, map[string]any{"code": -32601, "message": "Foundation cannot service this request; owner intervention required"})
					// A known failure: the runtime asked for something no run can answer (retry is safe).
					return driver.Result{Err: unservedRequest{m.Method}}
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
				r := finish(m.Params, text.String())
				if r.Status == "COMPLETED" && s.exitGuard != nil && s.guardLeft > 0 && !s.interrupted.Load() && !s.exitGuard() && s.followUp() {
					continue // the runtime gets a turn to exit
				}
				return r
			}
		}
	}
}

// elicitationAnswer answers an MCP elicitation. With approvalPolicy untrusted (command rules), Codex asks
// before every MCP tool call; calls of Factory MCP are accepted, since Core authorizes each one with the
// run credential and the agent's policies. Any other elicitation (another server, a form for the user) is
// declined: there is nobody to answer it during a run.
func elicitationAnswer(params json.RawMessage) map[string]any {
	var p struct {
		ServerName string `json:"serverName"`
		Meta       struct {
			Kind string `json:"codex_approval_kind"`
		} `json:"_meta"`
	}
	if json.Unmarshal(params, &p) == nil && p.ServerName == "factory" && p.Meta.Kind == "mcp_tool_call" {
		return map[string]any{"action": "accept", "content": map[string]any{}}
	}
	return map[string]any{"action": "decline", "content": nil}
}

// followUp starts the exit guard's turn in the same thread (one of guardLeft).
func (s *session) followUp() bool {
	s.guardLeft--
	var turn struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	input := []any{map[string]any{"type": "text", "text": driver.ExitGuardPrompt, "text_elements": []any{}}}
	if err := s.call(context.Background(), "turn/start", map[string]any{"threadId": s.threadID, "input": input}, &turn); err != nil || turn.Turn.ID == "" {
		return false
	}
	s.turnMu.Lock()
	s.turnID = turn.Turn.ID
	s.turnMu.Unlock()
	if s.interrupted.Load() {
		// Interrupt ran while this turn was starting and named the finished one: interrupt this one too.
		_ = s.call(context.Background(), "turn/interrupt", map[string]string{"threadId": s.threadID, "turnId": turn.Turn.ID}, nil)
	}
	_ = s.send(driver.Event{Kind: driver.EventActivity, Method: "factory/exit_guard", Data: json.RawMessage(`{"turn":"` + turn.Turn.ID + `"}`)})
	return true
}

// steer gives the agent a declined command's instruction: the approval response carries only the
// decision. A turn that already ended rejects it, which is harmless.
func (s *session) steer(note string) {
	input := []any{map[string]any{"type": "text", "text": "Factory command policy: " + note, "text_elements": []any{}}}
	_ = s.call(context.Background(), "turn/steer", map[string]any{"threadId": s.threadID, "expectedTurnId": s.turnID, "input": input}, nil)
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

// tokens is a TokenUsageBreakdown of thread/tokenUsage/updated (Codex 0.162.1 schema): inputTokens
// include cachedInputTokens, outputTokens include reasoningOutputTokens.
type tokens struct {
	Input      int64 `json:"inputTokens"`
	Cached     int64 `json:"cachedInputTokens"`
	CacheWrite int64 `json:"cacheWriteInputTokens"`
	Output     int64 `json:"outputTokens"`
}

func (a tokens) minus(b tokens) tokens {
	return tokens{a.Input - b.Input, a.Cached - b.Cached, a.CacheWrite - b.CacheWrite, a.Output - b.Output}
}

// usage is this run's cumulative usage: tokenUsage.total is per thread (a resumed thread
// includes earlier runs), so the first update of the turn fixes the baseline total - last.
func (s *session) usage(params json.RawMessage) *protocol.Usage {
	var p struct {
		TurnID     string `json:"turnId"`
		TokenUsage struct {
			Total, Last tokens
		} `json:"tokenUsage"`
	}
	if json.Unmarshal(params, &p) != nil || p.TurnID != s.turnID {
		return nil
	}
	if s.base == nil {
		b := p.TokenUsage.Total.minus(p.TokenUsage.Last)
		s.base = &b
	}
	t := p.TokenUsage.Total.minus(*s.base)
	return &protocol.Usage{Model: s.model, Input: t.Input - t.Cached, Output: t.Output, CacheRead: t.Cached, CacheWrite: t.CacheWrite}
}

type window struct {
	UsedPercent int    `json:"usedPercent"`
	ResetsAt    *int64 `json:"resetsAt"`
}

// rateLimits is the merged account/rateLimits/updated snapshot; updates are sparse.
type rateLimits struct {
	Primary, Secondary *window
	Reached            *string `json:"rateLimitReachedType"`
}

func (r *rateLimits) merge(params json.RawMessage) {
	var p struct {
		RateLimits rateLimits `json:"rateLimits"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	if p.RateLimits.Primary != nil {
		r.Primary = p.RateLimits.Primary
	}
	if p.RateLimits.Secondary != nil {
		r.Secondary = p.RateLimits.Secondary
	}
	if p.RateLimits.Reached != nil {
		r.Reached = p.RateLimits.Reached
	}
}

// quota maps a final usageLimitExceeded turn error (error notification without retry, or a
// failed turn/completed) to a quota event, once. The reset time is the latest reset of an
// exhausted rate-limit window, unknown without one.
func (s *session) quota(method string, params json.RawMessage) *protocol.Quota {
	var p struct {
		WillRetry bool `json:"willRetry"`
		Error     *struct {
			Info    json.RawMessage `json:"codexErrorInfo"`
			Message string          `json:"message"`
		} `json:"error"`
		Turn struct {
			Error *struct {
				Info    json.RawMessage `json:"codexErrorInfo"`
				Message string          `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	if s.quotaSent || json.Unmarshal(params, &p) != nil || p.WillRetry {
		return nil
	}
	e := p.Error
	if method == "turn/completed" {
		e = p.Turn.Error
	}
	if e == nil || string(e.Info) != `"usageLimitExceeded"` {
		return nil
	}
	s.quotaSent = true
	q := &protocol.Quota{}
	if s.limits.Reached != nil {
		q.Type = *s.limits.Reached
	}
	for _, w := range []*window{s.limits.Primary, s.limits.Secondary} {
		if w != nil && w.UsedPercent >= 100 && w.ResetsAt != nil {
			if t := time.Unix(*w.ResetsAt, 0).UTC(); t.After(q.ResetsAt) {
				q.ResetsAt = t
			}
		}
	}
	if q.ResetsAt.IsZero() {
		// No rate-limit snapshot: the error text names the time ("... or try again at 10:45 PM.").
		q.ResetsAt = retryAt(e.Message, time.Now())
	}
	return q
}

// The time may be formatted with a narrow no-break space before AM/PM, which \s does not match.
var tryAgainAt = regexp.MustCompile(`try again at (\d{1,2}:\d{2}[\s\x{202f}]?[AP]M)`)

// retryAt is the next local time named by "try again at H:MM AM|PM" after now, zero when absent.
func retryAt(message string, now time.Time) time.Time {
	m := tryAgainAt.FindStringSubmatch(message)
	if m == nil {
		return time.Time{}
	}
	clock, err := time.ParseInLocation("3:04 PM", strings.Replace(m[1], "\u202f", " ", 1), now.Location())
	if err != nil {
		if clock, err = time.ParseInLocation("3:04PM", m[1], now.Location()); err != nil {
			return time.Time{}
		}
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), clock.Hour(), clock.Minute(), 0, 0, now.Location())
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at.UTC()
}

// unservedRequest is a server request the driver cannot answer; the run fails (outcome failed).
type unservedRequest struct{ method string }

func (e unservedRequest) Error() string      { return "server request requires owner: " + e.method }
func (e unservedRequest) RunOutcome() string { return driver.OutcomeFailed }

// loginRequired: the app-server has no ChatGPT login. The run ends paused_out and the node disables Codex
// until its probe (codex login status) passes (driver.AuthFailure).
type loginRequired struct{}

func (loginRequired) Error() string      { return "ChatGPT login required; API-key auth is not supported" }
func (loginRequired) RunOutcome() string { return driver.OutcomePausedOut }
func (loginRequired) AuthFailed() bool   { return true }

// Package claude drives the Claude Agent SDK through the per-run Node shim
// (workers/claude/src/shim.ts, docs/claude-runtime.md): one launch line on stdin, normalized
// event lines on stdout; {"start": true} delivers the prompt after the session is persisted and
// {"interrupt": true} requests cancellation.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"appfactory/internal/driver"
	"appfactory/internal/protocol"
)

const (
	DefaultShim         = "workers/claude/dist/src/shim.js"
	SupportedSDK        = "0.3.284"
	SupportedClaudeCode = "2.1.284"
)

type Driver struct {
	// Node runs the shim; default "node".
	Node string
	// Shim is the built shim script; default DefaultShim, relative to the working directory.
	Shim string
	// Args replace [Shim] (tests use a fake shim).
	Args []string
	// Executable is the Claude Code binary; empty uses CLAUDE_CODE_EXECUTABLE or the bundled one.
	Executable     string
	AllowAPIKey    bool
	SettingSources []string
}

func (d Driver) Name() protocol.RuntimeProvider { return protocol.RuntimeClaude }
func (d Driver) Capabilities() driver.Capabilities {
	return driver.Capabilities{Resume: true, MCP: true, Activity: true, Cost: true}
}
func (d Driver) command(extra ...string) *exec.Cmd {
	node, args := d.Node, d.Args
	if node == "" {
		node = "node"
	}
	if args == nil {
		shim := d.Shim
		if shim == "" {
			shim = DefaultShim
		}
		args = []string{shim}
	}
	return exec.Command(node, append(append([]string{}, args...), extra...)...)
}

// CheckVersion runs `shim.js --probe`: Claude Code version and account, no model call.
func (d Driver) CheckVersion(ctx context.Context) (string, error) {
	cmd := d.command("--probe")
	if d.Executable != "" {
		cmd.Env = append(os.Environ(), "CLAUDE_CODE_EXECUTABLE="+d.Executable)
	}
	stop := context.AfterFunc(ctx, func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	defer stop()
	out, runErr := cmd.Output()
	var p struct {
		Event, Class, Message string
		ClaudeCodeVersion     string `json:"claude_code_version"`
		SDKVersion            string `json:"sdk_version"`
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &p); err != nil {
		return "", fmt.Errorf("claude probe: %w (%v)", err, runErr)
	}
	if p.Event != "probe" {
		return "", &Error{Class: p.Class, Message: p.Message}
	}
	v := "claude-agent-sdk " + p.SDKVersion + " (Claude Code " + p.ClaudeCodeVersion + ")"
	if p.SDKVersion != SupportedSDK || p.ClaudeCodeVersion != SupportedClaudeCode {
		return v, fmt.Errorf("unsupported %s: expected SDK %s with Claude Code %s; rerecord streams before upgrade", v, SupportedSDK, SupportedClaudeCode)
	}
	return v, nil
}

// ValidateOptions refuses runtime options the shim does not apply.
func ValidateOptions(o map[string]any) error {
	for k, v := range o {
		ok := false
		switch k {
		case "model":
			_, ok = v.(string)
		case "effort":
			switch v {
			case "low", "medium", "high", "xhigh", "max":
				ok = true
			}
		case "max_turns":
			var n float64
			switch x := v.(type) {
			case int:
				n = float64(x)
			case int64:
				n = float64(x)
			case float64:
				n = x
			}
			ok = n >= 1 && n == math.Trunc(n)
		default:
			return fmt.Errorf("unsupported Claude runtime option %q", k)
		}
		if !ok {
			return fmt.Errorf("invalid Claude runtime option %s: %v", k, v)
		}
	}
	return nil
}

// Error is a failure without terminal evidence; Class is the shim's error class, "auth_error",
// or "protocol" for a broken shim stream.
type Error struct{ Class, Message string }

func (e *Error) Error() string { return "claude " + e.Class + ": " + e.Message }

type launchLine struct {
	Spec           protocol.RunSpec `json:"spec"`
	Cwd            string           `json:"cwd"`
	Instructions   string           `json:"instructions"`
	MCPCommand     string           `json:"mcp_command,omitempty"`
	Executable     string           `json:"executable,omitempty"`
	AllowAPIKey    bool             `json:"allow_api_key,omitempty"`
	SettingSources []string         `json:"setting_sources,omitempty"`
}

// Launch starts the shim and returns once it reported the session, before the prompt. The run
// token and FACTORY_URL reach the shim and factory-mcp only through l.Env (MCP.EnvVars are
// inherited), never through the launch line.
func (d Driver) Launch(ctx context.Context, l driver.Launch) (driver.Session, error) {
	if err := ValidateOptions(l.Spec.RuntimeOptions); err != nil {
		return nil, err
	}
	launch := launchLine{Spec: l.Spec, Cwd: l.Cwd, Instructions: l.Instructions, Executable: d.Executable, AllowAPIKey: d.AllowAPIKey, SettingSources: d.SettingSources}
	if l.MCP != nil {
		launch.MCPCommand = l.MCP.Command
	}
	line, err := json.Marshal(launch)
	if err != nil {
		return nil, err
	}
	// The shim has its own lifetime so cancellation can first send an interrupt.
	cmd := d.command()
	cmd.Env = l.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	s := &session{cmd: cmd, stdin: stdin, lines: make(chan []byte), exited: make(chan struct{}), events: make(chan driver.Event, 64), done: make(chan driver.Result, 1), stop: make(chan struct{})}
	go s.read(stdout)
	if err = s.control(line); err == nil {
		err = s.open(ctx)
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

type event struct {
	Event   string          `json:"event"`
	ID      string          `json:"id"`
	Kind    string          `json:"kind"`
	Data    json.RawMessage `json:"data"`
	Status  string          `json:"status"`
	Result  string          `json:"result"`
	Class   string          `json:"class"`
	Reason  string          `json:"reason"`
	Message string          `json:"message"`
}

type session struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	write     sync.Mutex
	lines     chan []byte // closed at stdout EOF
	exited    chan struct{}
	waitErr   error
	id        string
	failure   *Error // last classified failure, reported when the result has no status
	events    chan driver.Event
	done      chan driver.Result
	stop      chan struct{}
	closeOnce sync.Once
}

func (s *session) read(out io.Reader) {
	defer close(s.exited)
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
scan:
	for sc.Scan() {
		select {
		case s.lines <- append([]byte(nil), sc.Bytes()...):
		case <-s.stop:
			break scan
		}
	}
	close(s.lines)
	s.waitErr = s.cmd.Wait()
}

func (s *session) control(line []byte) error {
	s.write.Lock()
	defer s.write.Unlock()
	_, err := s.stdin.Write(append(line, '\n'))
	return err
}

func decode(line []byte) (event, error) {
	var e event
	if err := json.Unmarshal(line, &e); err != nil || e.Event == "" {
		return e, &Error{Class: "protocol", Message: fmt.Sprintf("invalid shim line %.200q", line)}
	}
	return e, nil
}

// note keeps the classification of failures that end without terminal evidence.
func (s *session) note(e event) {
	switch e.Event {
	case "auth_error":
		s.failure = &Error{Class: "auth_error", Message: e.Reason}
	case "error":
		s.failure = &Error{Class: e.Class, Message: e.Message}
	}
}

func (s *session) result(e event) driver.Result {
	switch e.Status {
	case "COMPLETED", "FAILED", "CANCELLED":
		return driver.Result{Status: e.Status, Text: e.Result}
	}
	if s.failure != nil {
		return driver.Result{Err: s.failure}
	}
	return driver.Result{Err: &Error{Class: "no_result", Message: e.Result}}
}

func (s *session) exitErr(what string) error {
	select {
	case <-s.exited:
		return &Error{Class: "no_result", Message: fmt.Sprintf("shim exited %s: %v", what, s.waitErr)}
	case <-s.stop:
		return errors.New("session closed")
	}
}

func (s *session) open(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-s.lines:
			if !ok {
				return s.exitErr("before the session")
			}
			e, err := decode(line)
			if err != nil {
				return err
			}
			switch e.Event {
			case "session":
				if e.ID == "" {
					return &Error{Class: "protocol", Message: "empty session ID"}
				}
				s.id = e.ID
				return nil
			case "result":
				if r := s.result(e); r.Err != nil {
					return r.Err
				}
				return &Error{Class: "protocol", Message: "result before the session"}
			}
			s.note(e)
		}
	}
}

func (s *session) ProcessGroup() int           { return s.cmd.Process.Pid }
func (s *session) ID() string                  { return s.id }
func (s *session) Events() <-chan driver.Event { return s.events }
func (s *session) Done() <-chan driver.Result  { return s.done }
func (s *session) Start(context.Context) error {
	if err := s.control([]byte(`{"start":true}`)); err != nil {
		return err
	}
	go func() {
		r := s.loop()
		close(s.events)
		s.done <- r
	}()
	return nil
}

// Interrupt only requests cancellation; the shim reports the result evidence.
func (s *session) Interrupt(context.Context) error {
	return s.control([]byte(`{"interrupt":true}`))
}
func (s *session) Close() {
	s.closeOnce.Do(func() {
		close(s.stop)
		_ = s.stdin.Close()
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
		<-s.exited
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

// loop forwards SDK messages as activity and the shim's normalized signals (usage, cost,
// quota, auth_error, max_turns, error) as activity `claude/shim/<event>` until the result.
func (s *session) loop() driver.Result {
	for {
		select {
		case <-s.stop:
			return driver.Result{Err: errors.New("session closed")}
		case line, ok := <-s.lines:
			if !ok {
				return driver.Result{Err: s.exitErr("without a result")}
			}
			e, err := decode(line)
			if err != nil {
				return driver.Result{Err: err}
			}
			ev := driver.Event{Kind: driver.EventActivity, Method: e.Kind, Data: e.Data}
			switch e.Event {
			case "result":
				s.flush()
				return s.result(e)
			case "activity":
			default:
				s.note(e)
				ev.Method, ev.Data = "claude/shim/"+e.Event, line
			}
			if !s.send(ev) {
				return driver.Result{Err: errors.New("session closed")}
			}
		}
	}
}

// flush lets the shim exit on its own after the result (at most 5 s) so Claude Code writes the
// transcript a later run resumes; Close then kills what is left of the group.
func (s *session) flush() {
	t := time.NewTimer(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case _, ok := <-s.lines:
			if !ok {
				return
			}
		case <-t.C:
			return
		case <-s.stop:
			return
		}
	}
}

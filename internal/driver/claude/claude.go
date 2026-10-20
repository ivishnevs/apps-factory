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
	"appfactory/internal/node"
	"appfactory/internal/node/shim"
	"appfactory/internal/policies"
	"appfactory/internal/procgroup"
	"appfactory/internal/protocol"
)

const (
	DefaultShim         = "workers/claude/dist/src/shim.js"
	SupportedSDK        = "0.3.284"
	SupportedClaudeCode = "2.1.284"
	// CostSource names the SDK's own cost figures (modelUsage costUSD, total_cost_usd).
	CostSource = "claude_estimate"
)

type Driver struct {
	// Node runs the shim; default "node".
	Node string
	// Shim is the built shim script; default BinDir/../workers/claude/dist/src/shim.js (DefaultShim,
	// relative to the working directory, when BinDir is unset).
	Shim string
	// Args replace [Shim] (tests use a fake shim).
	Args []string
	// Executable is the Claude Code binary; empty uses CLAUDE_CODE_EXECUTABLE or the bundled one.
	Executable     string
	AllowAPIKey    bool
	SettingSources []string
	// Host runs Probe; nil is the local machine. BinDir is the Factory bin directory (default "bin";
	// the built shim is BinDir/../workers/claude/dist/src/shim.js).
	Host   *node.Host
	BinDir string
}

func (d Driver) Name() protocol.RuntimeProvider { return protocol.RuntimeClaude }
func (d Driver) Capabilities() driver.Capabilities {
	return driver.Capabilities{Resume: true, MCP: true, Activity: true, Cost: true, ToolPolicy: true, ExitGuard: true}
}

// ResumePolicy resumes only a started session this node ran (driver.ResumeSameNode).
func (d Driver) ResumePolicy(prev driver.Outcome, sameNode bool) bool {
	return driver.ResumeSameNode(prev, sameNode)
}
func (d Driver) command(extra ...string) *exec.Cmd {
	args := d.Args
	if args == nil {
		args = []string{d.shimPath()}
	}
	return exec.Command(d.nodeBinary(), append(append([]string{}, args...), extra...)...)
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
	p, err := parseProbe(string(out), runErr)
	if err != nil {
		var stderr string
		if ee := (*exec.ExitError)(nil); errors.As(runErr, &ee) {
			stderr = string(ee.Stderr)
		}
		if node.TransientRun(string(out), stderr, runErr) || strings.TrimSpace(string(out)) == "" && strings.TrimSpace(stderr) == "" {
			return "", node.Transient(err) // killed, no output or not started: says nothing about the version
		}
		return "", err
	}
	return p.version()
}

// probeLine is the last line of `shim.js --probe`: a probe event, or an error event.
type probeLine struct {
	Event, Class, Message string
	ClaudeCodeVersion     string `json:"claude_code_version"`
	SDKVersion            string `json:"sdk_version"`
	Account               struct {
		Email            string `json:"email"`
		Organization     string `json:"organization"`
		SubscriptionType string `json:"subscriptionType"`
		TokenSource      string `json:"tokenSource"`
		APIKeySource     string `json:"apiKeySource"`
		APIProvider      string `json:"apiProvider"`
	} `json:"account"`
}

func parseProbe(out string, runErr error) (probeLine, error) {
	var p probeLine
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &p); err != nil {
		return p, fmt.Errorf("claude probe: %w (%v)", err, runErr)
	}
	if p.Event != "probe" {
		return p, &Error{Class: p.Class, Message: p.Message}
	}
	return p, nil
}

// version is the probed runtime, with an error outside the supported pair.
func (p probeLine) version() (string, error) {
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

// AuthFailed: Claude Code's login failed or an API key was refused (driver.AuthFailure).
func (e *Error) AuthFailed() bool { return e.Class == "auth_error" || e.Class == "auth" }

// RunOutcome is the known outcome of a failure without terminal evidence (driver.OutcomeError):
// auth and a missing or unsupported Claude Code executable are paused_out; a broken shim stream
// and failures before the prompt reached Claude Code (launch, startup, Factory MCP, session
// mismatch) are failed. Anything else (no_result, runtime, interrupt_*) stays unknown.
func (e *Error) RunOutcome() string {
	switch e.Class {
	case "auth_error", "auth", "executable":
		return driver.OutcomePausedOut
	case "protocol", "launch", "startup_timeout", "mcp_unavailable", "session_mismatch", "not_started":
		return driver.OutcomeFailed
	}
	return ""
}

type launchLine struct {
	Spec           protocol.RunSpec `json:"spec"`
	Cwd            string           `json:"cwd"`
	Instructions   string           `json:"instructions"`
	MCPCommand     string           `json:"mcp_command,omitempty"`
	Executable     string           `json:"executable,omitempty"`
	AllowAPIKey    bool             `json:"allow_api_key,omitempty"`
	SettingSources []string         `json:"setting_sources,omitempty"`
	// ExitFile and ExitGuardRetries turn on the Stop-hook exit guard (driver.Launch.ExitGuard).
	ExitFile         string `json:"exit_file,omitempty"`
	ExitGuardRetries int    `json:"exit_guard_retries,omitempty"`
	// GuardCommand is factory-shim: a PreToolUse hook runs `GuardCommand --decide` for every Bash
	// command (driver.Launch.Guards; the PATH shims stay in place).
	GuardCommand string `json:"guard_command,omitempty"`
}

// Launch starts the shim and returns once it reported the session, before the prompt. The run
// token and FACTORY_URL reach the shim and factory-mcp only through l.Env (MCP.EnvVars are
// inherited), never through the launch line.
func (d Driver) Launch(ctx context.Context, l driver.Launch) (driver.Session, error) {
	if err := ValidateOptions(l.Spec.RuntimeOptions); err != nil {
		return nil, err
	}
	launch := launchLine{Spec: l.Spec, Cwd: l.Cwd, Instructions: l.Instructions, Executable: d.Executable, AllowAPIKey: d.AllowAPIKey, SettingSources: d.SettingSources}
	if l.ExitGuard != nil && l.ExitFile != "" {
		launch.ExitFile, launch.ExitGuardRetries = l.ExitFile, l.ExitGuardRetries
	}
	if l.MCP != nil {
		launch.MCPCommand = l.MCP.Command
	}
	guard, err := guardCommand(l.Guards, l.Env)
	if err != nil {
		return nil, err
	}
	launch.GuardCommand = guard
	line, err := json.Marshal(launch)
	if err != nil {
		return nil, err
	}
	removeSkills, err := installSkills(l.Cwd, l.SkillsRoot, l.Skills, d.SettingSources)
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
		removeSkills()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		removeSkills()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		removeSkills()
		return nil, err
	}
	s := &session{removeSkills: removeSkills, cmd: cmd, stdin: stdin, lines: make(chan []byte), exited: make(chan struct{}), events: make(chan driver.Event, 64), done: make(chan driver.Result, 1), stop: make(chan struct{})}
	// Recorded before read can reap the shim, so Close never signals a group that reused its PID.
	s.group = procgroup.RecordOrLeader(cmd.Process.Pid, "claude")
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

// guardCommand is the factory-shim binary behind the run's shim directory when the rules deny or gate
// a command; "" when nothing is guarded.
func guardCommand(rules []policies.Rule, env []string) (string, error) {
	if len(policies.Guarded(rules)) == 0 {
		return "", nil
	}
	dir := os.Getenv(shim.EnvDir)
	if env != nil {
		dir = ""
		for _, v := range env {
			if k, val, _ := strings.Cut(v, "="); k == shim.EnvDir {
				dir = val
			}
		}
	}
	if dir == "" {
		return "", fmt.Errorf("command guards need %s (the run's shim directory)", shim.EnvDir)
	}
	return shim.Executable(dir, rules)
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
	// removeSkills removes the skills materialized in the workspace for this run (Close).
	removeSkills func()
	cmd          *exec.Cmd
	// group is the shim's process group (leader start time); zero when it could not be recorded.
	group     procgroup.Group
	stdin     io.WriteCloser
	write     sync.Mutex
	lines     chan []byte // closed at stdout EOF
	exited    chan struct{}
	waitErr   error
	id        string
	failure   *Error // last classified failure, reported when the result has no status
	auth      *Error // auth failure seen during the run: the run ends paused_out
	maxTurns  bool   // the shim reported max_turns: a FAILED result ends max_turns
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
		s.auth = s.failure
	case "max_turns":
		s.maxTurns = true
	case "error":
		s.failure = &Error{Class: e.Class, Message: e.Message}
	}
}

func (s *session) result(e event) driver.Result {
	switch e.Status {
	case "COMPLETED", "CANCELLED":
		return driver.Result{Status: e.Status, Text: e.Result}
	case "FAILED":
		r := driver.Result{Status: e.Status, Text: e.Result}
		switch {
		case s.maxTurns:
			r.Outcome = driver.OutcomeMaxTurns
		case s.auth != nil:
			r.Outcome = driver.OutcomePausedOut
		}
		if s.auth != nil {
			r.Auth = s.auth
		}
		return r
	}
	if s.auth != nil {
		return driver.Result{Err: s.auth, Auth: s.auth}
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
		killGroup(s.group, s.cmd.Process)
		<-s.exited
		s.removeSkills()
	})
}

// killLimit bounds how long Close waits for the killed shim group to disappear.
const killLimit = 5 * time.Second

// killGroup kills the shim's group only while it is still the recorded one (its leader may already
// be reaped and its PID reused). Without a record it kills just the leader, which os.Process never
// signals after reaping.
func killGroup(g procgroup.Group, leader *os.Process) {
	if g.ID > 1 {
		_ = g.Stop(0, killLimit)
		return
	}
	_ = leader.Kill()
}

func (s *session) send(e driver.Event) bool {
	select {
	case s.events <- e:
		return true
	case <-s.stop:
		return false
	}
}

// loop forwards SDK messages as activity and the shim's normalized signals as `claude/shim/<event>`
// with the line as Data until the result: usage, cost and quota as their driver kinds, the
// others (auth_error, max_turns, error) as activity.
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
				if err = signal(&ev, e.Event, line); err != nil {
					return driver.Result{Err: err}
				}
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

// signal sets the driver kind of a usage, cost or quota line.
func signal(ev *driver.Event, kind string, line []byte) error {
	var l struct {
		Model         string     `json:"model"`
		Input         int64      `json:"input"`
		Output        int64      `json:"output"`
		CacheRead     int64      `json:"cache_read"`
		CacheWrite    int64      `json:"cache_write"`
		CostUSD       *float64   `json:"cost_usd"`
		USD           float64    `json:"usd"`
		Source        string     `json:"source"`
		ResetsAt      *time.Time `json:"resets_at"`
		RateLimitType string     `json:"rate_limit_type"`
	}
	switch kind {
	case "usage", "cost", "quota":
		if err := json.Unmarshal(line, &l); err != nil {
			return &Error{Class: "protocol", Message: fmt.Sprintf("invalid %s line: %v", kind, err)}
		}
	}
	switch kind {
	case "usage":
		u := &protocol.Usage{Model: l.Model, Input: l.Input, Output: l.Output, CacheRead: l.CacheRead, CacheWrite: l.CacheWrite}
		if l.CostUSD != nil {
			u.CostUSD, u.CostSource = *l.CostUSD, CostSource
		}
		ev.Kind, ev.Usage = driver.EventUsage, u
	case "cost":
		ev.Kind, ev.Cost = driver.EventCost, &driver.Cost{USD: l.USD, Source: l.Source}
	case "quota":
		q := &protocol.Quota{Type: l.RateLimitType}
		if l.ResetsAt != nil {
			q.ResetsAt = l.ResetsAt.UTC()
		}
		ev.Kind, ev.Quota = driver.EventQuota, q
	}
	return nil
}

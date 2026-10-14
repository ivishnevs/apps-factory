package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/driver"
	"appfactory/internal/driver/claude/claudetest"
	"appfactory/internal/procgroup"
	"appfactory/internal/protocol"
)

var _ driver.Driver = Driver{}

// TestFakeShim is the fake shim process: it replays FACTORY_FAKE_SHIM from
// FACTORY_FAKE_SHIM_DIR (default testdata) and records the launch line and run token.
func TestFakeShim(t *testing.T) {
	name := os.Getenv("FACTORY_FAKE_SHIM")
	if name == "" {
		return
	}
	if slices.Contains(os.Args, "--probe") {
		fmt.Println(os.Getenv("FACTORY_FAKE_PROBE"))
		os.Exit(0)
	}
	dir := os.Getenv("FACTORY_FAKE_SHIM_DIR")
	if dir == "" {
		dir = claudetest.Dir()
	}
	err := claudetest.Serve(dir, name, os.Stdin, os.Stdout, func(line []byte) {
		b, _ := json.Marshal(map[string]any{"launch": json.RawMessage(line), "token": os.Getenv("FACTORY_RUN_TOKEN")})
		_ = os.WriteFile(os.Getenv("FACTORY_FAKE_SHIM_RECORD"), b, 0o600)
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

type record struct {
	Raw    json.RawMessage `json:"launch"`
	Token  string          `json:"token"`
	Launch launchLine      `json:"-"`
}

func fakeDriver(t *testing.T, name, dir string) (Driver, string) {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rec := filepath.Join(t.TempDir(), "launch.json")
	t.Setenv("FACTORY_FAKE_SHIM", name)
	t.Setenv("FACTORY_FAKE_SHIM_DIR", dir)
	t.Setenv("FACTORY_FAKE_SHIM_RECORD", rec)
	return Driver{Node: bin, Args: []string{"-test.run=^TestFakeShim$", "--"}}, rec
}

// scenarioLaunch is the recorded launch line as a driver.Launch with the run token in Env.
func scenarioLaunch(t *testing.T, name string) (driver.Launch, string) {
	t.Helper()
	var s struct {
		Launch launchLine        `json:"launch"`
		Env    map[string]string `json:"env"`
	}
	b, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	if err != nil {
		t.Fatal(err)
	}
	token := s.Env["FACTORY_RUN_TOKEN"]
	return driver.Launch{Spec: s.Launch.Spec, Cwd: s.Launch.Cwd, Instructions: s.Launch.Instructions, MCP: &driver.MCPConfig{Command: s.Launch.MCPCommand, EnvVars: []string{"FACTORY_RUN_TOKEN", "FACTORY_URL"}},
		Env: append(os.Environ(), "FACTORY_URL="+s.Env["FACTORY_URL"], "FACTORY_RUN_TOKEN="+token)}, token
}

// expected is the recorded stream as driver events: activity as is, signals as claude/shim/<event>.
func expected(t *testing.T, name string) (methods []string, session string, result map[string]string) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name+".events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var e map[string]any
		if err = json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		switch e["event"] {
		case "session":
			session = e["id"].(string)
		case "result":
			result = map[string]string{"status": e["status"].(string), "result": e["result"].(string)}
		case "activity":
			methods = append(methods, e["kind"].(string))
		default:
			methods = append(methods, "claude/shim/"+e["event"].(string))
		}
	}
	return methods, session, result
}

func run(t *testing.T, name string, interrupt bool) (driver.Session, []driver.Event, driver.Result, record) {
	t.Helper()
	d, rec := fakeDriver(t, name, claudetest.Dir())
	l, token := scenarioLaunch(t, name)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, err := d.Launch(ctx, l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if g := s.(*session).group; s.ProcessGroup() <= 0 || g.ID != s.ProcessGroup() || g.Started == "" {
		t.Fatalf("process group %d not recorded: %+v", s.ProcessGroup(), g)
	}
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if interrupt {
		if err = s.Interrupt(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var events []driver.Event
	for e := range s.Events() {
		events = append(events, e)
	}
	r := <-s.Done()
	var got record
	b, err := os.ReadFile(rec)
	if err == nil {
		err = json.Unmarshal(b, &got)
	}
	if err == nil {
		err = json.Unmarshal(got.Raw, &got.Launch)
	}
	if err != nil {
		t.Fatal(err)
	}
	// The run token reaches the shim only through the environment.
	if got.Token != token || strings.Contains(string(got.Raw), token) {
		t.Fatalf("token %q in env %q, launch %s", token, got.Token, got.Raw)
	}
	if got.Launch.Spec.ID != l.Spec.ID || got.Launch.MCPCommand != l.MCP.Command || got.Launch.Instructions != l.Instructions || got.Launch.Cwd != l.Cwd {
		t.Fatalf("launch %s", got.Raw)
	}
	methods, session, result := expected(t, name)
	if s.ID() != session {
		t.Fatalf("session %q, want %q", s.ID(), session)
	}
	var gotMethods []string
	kinds := map[string]driver.EventKind{"claude/shim/usage": driver.EventUsage, "claude/shim/cost": driver.EventCost, "claude/shim/quota": driver.EventQuota}
	for _, e := range events {
		want, ok := kinds[e.Method]
		if !ok {
			want = driver.EventActivity
		}
		if e.Kind != want || len(e.Data) == 0 || (e.Kind == driver.EventUsage) != (e.Usage != nil) || (e.Kind == driver.EventCost) != (e.Cost != nil) || (e.Kind == driver.EventQuota) != (e.Quota != nil) {
			t.Fatalf("event %+v", e)
		}
		gotMethods = append(gotMethods, e.Method)
	}
	if !slices.Equal(gotMethods, methods) {
		t.Fatalf("events %v, want %v", gotMethods, methods)
	}
	if r.Err != nil || r.Status != result["status"] || r.Text != result["result"] {
		t.Fatalf("result %+v, want %v", r, result)
	}
	return s, events, r, got
}

func TestRecordedStart(t *testing.T) {
	s, events, _, rec := run(t, "start", false)
	if rec.Launch.Spec.SessionID != "" || rec.Launch.Spec.Prompt == "" {
		t.Fatalf("launch %s", rec.Raw)
	}
	opus, haiku, cost := events[len(events)-3], events[len(events)-2], events[len(events)-1]
	if *opus.Usage != (protocol.Usage{Model: "claude-opus-4-7", Input: 8, Output: 70, CacheRead: 14210, CacheWrite: 14590, CostUSD: 0.0912, CostSource: "claude_estimate"}) ||
		haiku.Usage.Model != "claude-haiku-4-5" || *cost.Cost != (driver.Cost{USD: 0.0941, Source: "claude_estimate"}) {
		t.Fatalf("usage/cost %+v %+v %+v", opus.Usage, haiku.Usage, cost.Cost)
	}
	group := s.ProcessGroup()
	s.Close()
	if err := syscall.Kill(-group, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process group %d still alive: %v", group, err)
	}
}

func TestRecordedResume(t *testing.T) {
	_, _, _, rec := run(t, "resume", false)
	if rec.Launch.Spec.SessionID != "0b9e4d27-8c51-4f3a-b6e2-7d18a5c94e60" {
		t.Fatalf("launch %s", rec.Raw)
	}
}

// An interrupted turn reports running usage before the result evidence and the final usage after.
func TestRecordedInterrupt(t *testing.T) {
	_, events, _, _ := run(t, "interrupt", true)
	var usage []protocol.Usage
	for _, e := range events {
		if e.Usage != nil {
			usage = append(usage, *e.Usage)
		}
	}
	want := protocol.Usage{Model: "claude-opus-4-7", Input: 3, Output: 48, CacheWrite: 14190}
	if len(usage) != 2 || usage[0] != want || usage[1].CostUSD != 0.0398 {
		t.Fatalf("usage %+v", usage)
	}
}

func TestRecordedMaxTurns(t *testing.T) {
	_, events, r, rec := run(t, "max_turns", false)
	if r.Status != "FAILED" || events[len(events)-1].Method != "claude/shim/max_turns" {
		t.Fatalf("%+v", r)
	}
	o := rec.Launch.Spec.RuntimeOptions
	if o["max_turns"] != 1.0 || o["effort"] != "low" || o["model"] != "claude-opus-4-7" {
		t.Fatalf("options %v", o)
	}
}

func TestRecordedRateLimit(t *testing.T) {
	_, events, r, _ := run(t, "rate_limit", false)
	var quota protocol.Quota
	for _, e := range events {
		if e.Quota != nil {
			quota = *e.Quota
		}
	}
	if r.Status != "FAILED" || !quota.ResetsAt.Equal(time.Date(2026, 10, 13, 23, 0, 0, 0, time.UTC)) || quota.Type != "five_hour" {
		t.Fatalf("%+v %+v", r, quota)
	}
}

func stream(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "s.events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFailuresWithoutEvidence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	launch := func(d Driver) (driver.Session, error) {
		l, _ := scenarioLaunch(t, "start")
		return d.Launch(ctx, l)
	}

	d, _ := fakeDriver(t, "s", stream(t, `{"event":"auth_error","reason":"api_key_source","api_key_source":"ANTHROPIC_API_KEY"}`, `{"event":"result","status":"UNKNOWN","result":"login required"}`))
	var e *Error
	if _, err := launch(d); !errors.As(err, &e) || e.Class != "auth_error" {
		t.Fatalf("auth: %v", err)
	}

	d, _ = fakeDriver(t, "s", stream(t, `{"event":"session","id":"s1"}`, `{"event":"activity","kind":"claude/system/init","data":{}}`))
	s, err := launch(d)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for range s.Events() {
	}
	if r := <-s.Done(); !errors.As(r.Err, &e) || e.Class != "no_result" {
		t.Fatalf("crash: %+v", r)
	}

	d, _ = fakeDriver(t, "s", stream(t, `{"event":"session","id":"s1"}`, `{"event":"error","class":"interrupt_unknown","message":"interrupt result unknown"}`, `{"event":"result","status":"UNKNOWN","result":"interrupt result unknown","session_id":"s1"}`))
	if s, err = launch(d); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for range s.Events() {
	}
	if r := <-s.Done(); !errors.As(r.Err, &e) || e.Class != "interrupt_unknown" {
		t.Fatalf("unknown: %+v", r)
	}
}

func TestOptions(t *testing.T) {
	for _, o := range []map[string]any{nil, {"model": "default", "effort": "high", "max_turns": 3}, {"max_turns": 2.0}} {
		if err := ValidateOptions(o); err != nil {
			t.Fatal(o, err)
		}
	}
	for _, o := range []map[string]any{{"sandbox": "x"}, {"effort": "huge"}, {"max_turns": 0}, {"max_turns": 1.5}, {"model": 1}} {
		if err := ValidateOptions(o); err == nil {
			t.Fatal(o)
		}
	}
	d, _ := fakeDriver(t, "start", claudetest.Dir())
	if _, err := d.Launch(context.Background(), driver.Launch{Spec: protocol.RunSpec{RuntimeOptions: map[string]any{"sandbox": "x"}}}); err == nil {
		t.Fatal("unsupported option launched")
	}
}

func TestCheckVersion(t *testing.T) {
	d, _ := fakeDriver(t, "probe", "")
	t.Setenv("FACTORY_FAKE_PROBE", `{"event":"probe","claude_code_version":"2.1.284","sdk_version":"0.3.284","account":{"apiKeySource":"none"}}`)
	if v, err := d.CheckVersion(context.Background()); err != nil || v != "claude-agent-sdk 0.3.284 (Claude Code 2.1.284)" {
		t.Fatal(v, err)
	}
	t.Setenv("FACTORY_FAKE_PROBE", `{"event":"probe","claude_code_version":"2.1.300","sdk_version":"0.3.284","account":{}}`)
	if _, err := d.CheckVersion(context.Background()); err == nil {
		t.Fatal("unsupported version accepted")
	}
	t.Setenv("FACTORY_FAKE_PROBE", `{"event":"error","class":"executable","message":"ENOENT"}`)
	var e *Error
	if _, err := d.CheckVersion(context.Background()); !errors.As(err, &e) || e.Class != "executable" {
		t.Fatal(err)
	}
}

// The built shim refuses a Factory MCP launch without the run token before starting the SDK.
func TestBuiltShimLaunchError(t *testing.T) {
	shim := filepath.Join("..", "..", "..", DefaultShim)
	if _, err := os.Stat(shim); err != nil {
		t.Skip("workers/claude not built")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	l, _ := scenarioLaunch(t, "start")
	l.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var e *Error
	if _, err := (Driver{Shim: shim}).Launch(ctx, l); !errors.As(err, &e) || e.Class != "launch" {
		t.Fatal(err)
	}
}

func TestOptionsMatchConfigRegistry(t *testing.T) {
	sample := map[string]any{"model": "claude-opus-5-5", "effort": "low", "max_turns": float64(3)}
	keys := config.RuntimeOptions[protocol.RuntimeClaude]
	for _, k := range keys {
		if err := ValidateOptions(map[string]any{k: sample[k]}); err != nil {
			t.Fatalf("config allows %q but the driver refuses it: %v", k, err)
		}
	}
	for k := range sample {
		if !slices.Contains(keys, k) {
			t.Fatalf("driver reads %q but config.RuntimeOptions[claude] = %v", k, keys)
		}
	}
}

// foreignGroup starts a process group the session does not own; exited closes when it ends.
func foreignGroup(t *testing.T) (pid int, exited <-chan struct{}) {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); <-done })
	return cmd.Process.Pid, done
}

// TestCloseSparesReusedGroup: when the shim was reaped and its PID now leads another group (a
// different start time), Close must not kill that group; the recorded group itself is killed.
func TestCloseSparesReusedGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid, foreignDone := foreignGroup(t)
	s := &session{removeSkills: func() {}, cmd: cmd, stdin: stdin, lines: make(chan []byte), exited: make(chan struct{}), stop: make(chan struct{}),
		group: procgroup.Group{ID: pid, Started: "Thu Jan  1 00:00:00 1970"}}
	go s.read(stdout)
	<-s.exited // the shim is reaped before Close
	s.Close()
	select {
	case <-foreignDone:
		t.Fatal("Close killed a group that reused the shim's PID")
	case <-time.After(300 * time.Millisecond):
	}
	g, err := procgroup.Record(pid)
	if err != nil {
		t.Fatal(err)
	}
	killGroup(g, nil)
	select {
	case <-foreignDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the recorded group survived killGroup")
	}
}

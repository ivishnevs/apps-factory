package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"appfactory/internal/approvals"
	"appfactory/internal/bus"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/node/shim"
	"appfactory/internal/policies"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

// e2e is a real Core (HTTP + JetStream) with one v2 node running the fake driver through the real
// factory-mcp and factory-shim binaries.
type e2e struct {
	t      *testing.T
	ctx    context.Context
	store  *storage.Store
	server *httptest.Server
	done   chan error
}

func startE2E(t *testing.T, explicitExits bool, agents map[string]config.Agent, d *fake.Driver) *e2e {
	t.Helper()
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to a dedicated JetStream instance")
	}
	bin := t.TempDir()
	for _, name := range []string{"factory-mcp", "factory-shim"} {
		if out, err := exec.Command("go", "build", "-o", filepath.Join(bin, name), "appfactory/cmd/"+name).CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = b.JS.DeleteStream("FACTORY_COMMANDS")
		_ = b.JS.DeleteStream("FACTORY_EVENTS")
		b.NC.Close()
	})
	beat := storage.HeartbeatSeconds
	storage.HeartbeatSeconds = 1
	t.Cleanup(func() { storage.HeartbeatSeconds = beat })
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	c := &core.Core{Store: s, Bus: b, Agents: agents, Token: "test-token", ExplicitExits: explicitExits}
	server := httptest.NewServer(c.Handler())
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	go c.Run(ctx)
	cfg := node.Defaults()
	cfg.Slots, cfg.StateDir, cfg.ProtocolVersion, cfg.BinDir = 1, t.TempDir(), 2, bin
	nodeCtx, stopNode := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(nodeCtx, Node{Config: cfg, Bus: b, API: worker.API{URL: server.URL, Token: "test-token"}, Drivers: []driver.Driver{perRunSessions{d}}, FetchWait: 200 * time.Millisecond})
	}()
	t.Cleanup(func() {
		stopNode()
		<-done
		cancel()
		server.Close()
		s.DB.Close()
	})
	return &e2e{t: t, ctx: ctx, store: s, server: server, done: done}
}

// task creates a ready task for demo in a fresh project.
func (e *e2e) task(id string) {
	e.t.Helper()
	co := storage.DefaultCompany
	if _, err := e.store.CreateProject(e.ctx, co, tasks.ProjectCreate{ID: "notes", Name: "notes", Workspace: e.t.TempDir()}); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.store.CreateTask(e.ctx, co, tasks.Create{ID: id, ProjectID: "notes", Title: "write the notes", Assignee: "demo", Ready: true, RequestID: "c"}); err != nil {
		e.t.Fatal(err)
	}
}

// wait polls until ok holds, failing when the node stops or the test times out.
func (e *e2e) wait(what string, ok func() bool) {
	e.t.Helper()
	for !ok() {
		select {
		case err := <-e.done:
			e.t.Fatal("node stopped", err)
		case <-e.ctx.Done():
			e.t.Fatal("timeout waiting for", what)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// finished waits for the task's run to end and returns it.
func (e *e2e) finished(taskID string) storage.Run {
	e.t.Helper()
	var run storage.Run
	e.wait("run end", func() bool {
		task, _ := e.store.Task(e.ctx, storage.DefaultCompany, taskID)
		if task.CurrentRunID != "" {
			run, _ = e.store.Get(e.ctx, storage.DefaultCompany, task.CurrentRunID)
		}
		return run.Status == "ENDED"
	})
	return run
}

// activity is the run's stored progress events with the given method.
func (e *e2e) activity(runID, method string) []protocol.Event {
	var out []protocol.Event
	events, _ := e.store.Events(e.ctx, storage.DefaultCompany, runID)
	for _, ev := range events {
		var p protocol.Event
		if json.Unmarshal(ev.Payload, &p) == nil && p.Method == method {
			out = append(out, p)
		}
	}
	return out
}

// TestJetStreamNoExitEndToEnd (runtime-drivers 2.4): a session that keeps trying to stop without an exit
// is refused ExitGuardRetries times by the exit guard, then the run ends and Core records no_exit with a
// no_exit escalation for the task.
func TestJetStreamNoExitEndToEnd(t *testing.T) {
	agents, err := config.Load("../../..")
	if err != nil {
		t.Fatal(err)
	}
	d := &fake.Driver{MCP: true, ExitGuard: true, Script: fake.Script{Status: "COMPLETED", Text: "I think I am done", Activity: []fake.Activity{
		{Tool: &fake.ToolCall{Name: "factory_task_get", Arguments: map[string]any{"id": "t1"}}},
	}}}
	e := startE2E(t, true, agents, d)
	e.task("t1")
	run := e.finished("t1")
	if run.Status != "ENDED" || run.Outcome != "no_exit" {
		t.Fatalf("run %+v", run)
	}
	if got := e.activity(run.Spec.ID, fake.ExitGuardMethod); len(got) != worker.ExitGuardRetries {
		t.Fatalf("%d exit guard refusals, want %d", len(got), worker.ExitGuardRetries)
	}
	if l := d.Launches(); len(l) != 1 || l[0].ExitGuard == nil || l[0].ExitGuardRetries != worker.ExitGuardRetries || l[0].ExitFile == "" {
		t.Fatalf("launch without the exit guard: %+v", l)
	}
	e.wait("no_exit escalation", func() bool {
		open, _ := e.store.Escalations(e.ctx, storage.DefaultCompany, true)
		for _, x := range open {
			if x.TaskID == "t1" && x.Reason == "no_exit" {
				return true
			}
		}
		return false
	})
}

// TestJetStreamCommandGrantEndToEnd (node-agent 4.2): through a real Core, the node executor and
// factory-shim, an approval-gated command is refused (126) without a grant; the agent requests the
// grant through factory-mcp, the owner approves it, and the exact command then runs once; a second
// run and changed arguments are refused.
func TestJetStreamCommandGrantEndToEnd(t *testing.T) {
	tools := t.TempDir()
	approved := filepath.Join(t.TempDir(), "approved")
	for name, body := range map[string]string{
		"mytool": `echo "real $*"`,
		// waitfor blocks the session until the owner decided (the agent finishing its turn and resuming).
		"waitfor": `while [ ! -f "$1" ]; do sleep 0.1; done`,
	} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	agents, err := config.Load("../../..")
	if err != nil {
		t.Fatal(err)
	}
	demo := agents["demo"]
	demo.Spec.Communication.CanMessage = append(demo.Spec.Communication.CanMessage, "owner")
	demo.Rules = append(demo.Rules, policies.Rule{Name: "push", Type: "access", Commands: []string{"mytool push *"}, Effect: "approval", Instruction: "Ask before pushing."})
	agents["demo"] = demo
	push := []string{"mytool", "push", "origin", "main"}
	d := &fake.Driver{MCP: true, Script: fake.Script{Status: "COMPLETED", Text: "pushed", Activity: []fake.Activity{
		{Exec: push},
		{Tool: &fake.ToolCall{Name: "factory_owner_request_approval", Arguments: map[string]any{
			"reason": "push the release branch", "request_id": "req-push", "blocking": false,
			"call": map[string]any{"name": "command", "arguments": map[string]any{"argv": push}}}}},
		{Exec: []string{"waitfor", approved}},
		{Exec: push},
		{Exec: push},
		{Exec: []string{"mytool", "push", "origin", "dev"}},
	}}}
	e := startE2E(t, false, agents, d)
	e.task("t1")

	// The owner approves the pending command grant through Core's API.
	var grant approvals.Approval
	e.wait("command grant request", func() bool {
		list, _ := e.store.Approvals(e.ctx, storage.DefaultCompany, "notes")
		for _, a := range list {
			if a.Kind == "command" && a.Status == "PENDING" {
				grant = a
				return true
			}
		}
		return false
	})
	body, _ := json.Marshal(approvals.Decision{Version: grant.Version, Action: "approve", Reason: "ok", RequestID: "approve"})
	req, _ := http.NewRequest("POST", e.server.URL+"/approvals/"+grant.ID+"/decision", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("approve", res.StatusCode)
	}
	if err = os.WriteFile(approved, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	run := e.finished("t1")
	if run.Status != "ENDED" || run.Outcome != "succeeded" {
		t.Fatalf("run %+v", run)
	}
	var execs []fake.ExecEvent
	for _, p := range e.activity(run.Spec.ID, fake.ExecMethod) {
		var x fake.ExecEvent
		if json.Unmarshal(p.Data, &x) == nil && x.Argv[0] == "mytool" {
			execs = append(execs, x)
		}
	}
	if len(execs) != 4 {
		t.Fatalf("%+v", execs)
	}
	if execs[0].Exit != shim.Refused || execs[0].Stdout != "" || !bytes.Contains([]byte(execs[0].Stderr), []byte("factory_owner_request_approval")) {
		t.Fatalf("without a grant %+v", execs[0])
	}
	if execs[1].Exit != 0 || execs[1].Stdout != "real push origin main\n" {
		t.Fatalf("approved %+v", execs[1])
	}
	if execs[2].Exit != shim.Refused || execs[2].Stdout != "" {
		t.Fatalf("grant used twice %+v", execs[2])
	}
	if execs[3].Exit != shim.Refused || execs[3].Stdout != "" {
		t.Fatalf("changed arguments %+v", execs[3])
	}
	if used, _ := e.store.Approval(e.ctx, storage.DefaultCompany, grant.ID); used.Status != "USED" {
		t.Fatal("grant", used.Status)
	}
	if refused := e.activity(run.Spec.ID, "factory/command_refused"); len(refused) != 3 {
		t.Fatalf("%d refusals recorded on the run, want 3", len(refused))
	}
}

// TestJetStreamHostUIRefusalEndToEnd (node-agent 4.1): through a real Core, the node executor and
// factory-shim, a run of a catalog agent calling cliclick (mandatory host-ui policy) gets exit 126 with the
// policy's instruction, the real cliclick never runs, and Core records the refusal on the run.
func TestJetStreamHostUIRefusalEndToEnd(t *testing.T) {
	tools := t.TempDir()
	clicked := filepath.Join(t.TempDir(), "clicked")
	if err := os.WriteFile(filepath.Join(tools, "cliclick"), []byte("#!/bin/sh\ntouch "+clicked+"\necho clicked\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	agents, err := config.Load("../../..")
	if err != nil {
		t.Fatal(err)
	}
	d := &fake.Driver{MCP: true, Script: fake.Script{Status: "COMPLETED", Text: "tapped", Activity: []fake.Activity{
		{Exec: []string{"cliclick", "c:100,200"}},
	}}}
	e := startE2E(t, false, agents, d)
	e.task("t1")
	run := e.finished("t1")
	if run.Status != "ENDED" || run.Outcome != "succeeded" {
		t.Fatalf("run %+v", run)
	}
	var execs []fake.ExecEvent
	for _, p := range e.activity(run.Spec.ID, fake.ExecMethod) {
		var x fake.ExecEvent
		if json.Unmarshal(p.Data, &x) == nil {
			execs = append(execs, x)
		}
	}
	instruction := policies.HostUI().Spec.Rules[0].Instruction
	if len(execs) != 1 || execs[0].Exit != shim.Refused || execs[0].Stdout != "" || !bytes.Contains([]byte(execs[0].Stderr), []byte(instruction)) {
		t.Fatalf("cliclick %+v", execs)
	}
	if _, err = os.Stat(clicked); !os.IsNotExist(err) {
		t.Fatal("the real cliclick ran", err)
	}
	refused := e.activity(run.Spec.ID, "factory/command_refused")
	if len(refused) != 1 || !bytes.Contains(refused[0].Data, []byte("cliclick")) || !bytes.Contains(refused[0].Data, []byte("host-ui")) {
		t.Fatalf("refusal events on the run: %+v", refused)
	}
}

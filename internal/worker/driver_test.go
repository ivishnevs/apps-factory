package worker

import (
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/testnode"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// asNode makes w an enrolled, registered node of the Core at url (epoch 1), as `factory-node run` sets it up. Nobody
// sends its node heartbeat unless the test does.
func asNode(t testing.TB, s *storage.Store, w *Worker, url string) testnode.Node {
	t.Helper()
	n := testnode.Enroll(t, s, storage.DefaultCompany)
	drv, _ := w.driver()
	if _, err := s.RegisterNode(context.Background(), storage.DefaultCompany, storage.NodeRegistration{NodeID: n.ID, Name: n.ID, Epoch: 1, ProtocolVersion: 2, Slots: 4, Worktrees: true,
		Drivers: []storage.NodeDriver{{Name: string(drv.Name()), Version: "test", OK: true}}, Resources: map[string]int{}, Labels: []string{}}); err != nil {
		t.Fatal(err)
	}
	w.API = API{URL: url, Node: n.ID, Key: n.Key, Now: s.Now}
	w.Box = n.Box
	// A first signed request fixes when Core started accepting signatures, so the test's next request, signed later,
	// is never refused as created before Core started (signatures carry whole seconds).
	_ = w.API.Do(context.Background(), "GET", "/health", nil, nil)
	w.Inventory = node.NewInventory(n.ID, 1)
	if w.NodeID == "" {
		w.NodeID = n.ID
	}
	return n
}

// runFake executes one run on a recorded fake session and applies the published events
// to Core's store until the run is terminal. cancel cancels the run after it started.
func runFake(t *testing.T, script string, cancel bool) (storage.Run, *fake.Driver) {
	t.Helper()
	sc, err := fake.Load(filepath.Join("testdata", "fake", script))
	if err != nil {
		t.Fatal(err)
	}
	d := &fake.Driver{Script: sc}
	r, events := runDriver(t, Worker{Driver: d}, cancel)
	if want := len(sc.Activity) + 2; len(events) != want {
		t.Fatalf("%d events, want started + %d progress + terminal", len(events), len(sc.Activity))
	}
	return r, d
}

// runDriver executes one run with w's driver against a fake Core, applying the published
// events to Core's store until the run is terminal; it returns the run and its stored events.
// stored edits the spec before Core stores the run as well (Core reads the captured config).
type stored func(*protocol.RunSpec)

// withStore prepares Core's store (projects, documents, blobs) before the run is created.
type withStore func(*storage.Store)

// stopCause cancels the execution with cause once the run started, as a node's heartbeat stop does.
type stopCause struct{ cause error }

// mods (func(*protocol.RunSpec) or stored) change the spec the executor receives; a stopCause stops it.
// Events reach Core over the signed HTTP event route as the run emits them.
func runDriver(t *testing.T, w Worker, cancel bool, mods ...any) (storage.Run, []protocol.Envelope) {
	t.Helper()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	for _, mod := range mods {
		if m, ok := mod.(withStore); ok {
			m(s)
		}
	}
	c := &core.Core{Store: s, Token: "test-token", Agents: map[string]config.Agent{"demo": {Spec: config.Spec{Role: "executor"}}}}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	spec := protocol.RunSpec{ID: "fake-run", Project: "fake-project", Agent: "demo", Provider: w.Driver.Name(), Workspace: t.TempDir(), Prompt: "work", Instructions: "base", TimeoutSeconds: 30}
	for _, mod := range mods {
		if m, ok := mod.(stored); ok {
			m(&spec)
		}
	}
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	if err = s.Create(ctx, storage.DefaultCompany, spec); err != nil {
		t.Fatal(err)
	}
	asNode(t, s, &w, server.URL)
	epoch, claim, err := w.ClaimV2(ctx, spec.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.openSecrets(storage.DefaultCompany, spec.ID, w.Inventory.NodeID, claim.Secrets); err != nil {
		t.Fatal(err)
	}
	lease := claim.Lease
	for _, mod := range mods {
		switch m := mod.(type) {
		case stored:
			m(&spec)
		case func(*protocol.RunSpec):
			m(&spec)
		}
	}
	// A stopCause mod cancels the execution with that cause once the run started (a node stop).
	var cause error
	for _, mod := range mods {
		if c, ok := mod.(stopCause); ok {
			cause = c.cause
		}
	}
	execCtx, stopExec := context.WithCancelCause(ctx)
	defer stopExec(nil)
	if !w.Inventory.AddCause(spec.ID, epoch, stopExec) {
		t.Fatal("claim not recorded")
	}
	var once sync.Once
	w.onEmit = func(_ string, p protocol.Event) {
		if p.SessionID == "" || !cancel && cause == nil {
			return
		}
		once.Do(func() {
			if cancel {
				if err := s.Cancel(ctx, storage.DefaultCompany, spec.ID); err != nil {
					t.Error(err)
				}
				// What the node does with the poll or heartbeat answer to a cancelled run (TestCancellation goes
				// through the heartbeat itself).
				stopExec(&node.StopCause{Reason: "revoked"})
				return
			}
			stopExec(cause)
		})
	}
	if err = w.execute(execCtx, spec, lease, "fake-command"); err != nil {
		t.Log("execute:", err)
	}
	// The run as Core serves it, so tests see what an owner sees (usage included).
	var r storage.Run
	if err = (API{URL: server.URL, Token: "test-token"}).Do(ctx, "GET", "/runs/"+spec.ID, nil, &r); err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, storage.DefaultCompany, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	return r, events
}

func TestFakeDriverOutcomes(t *testing.T) {
	for script, want := range map[string]struct{ outcome, result string }{
		"completed.json":    {"succeeded", "foundation ok"},
		"failed.json":       {"failed", "\n{\"message\":\"model error\"}"},
		"disconnected.json": {"lost", "app-server disconnected: EOF"},
	} {
		t.Run(script, func(t *testing.T) {
			r, d := runFake(t, script, false)
			if r.Status != "ENDED" || r.Outcome != want.outcome || r.Result != want.result || r.SessionID != "recorded-thread" {
				t.Fatalf("%+v", r)
			}
			l := d.Launches()
			if len(l) != 1 || l[0].Instructions != "base" || l[0].MCP != nil || len(l[0].Env) == 0 {
				t.Fatalf("launch %+v", l)
			}
		})
	}
}

// TestRuntimeEnvWithoutCredential: a driver without MCP and without command shims gets no
// run credential, yet its runtime still must not inherit the node's FACTORY_TOKEN or NATS settings.
func TestRuntimeEnvWithoutCredential(t *testing.T) {
	t.Setenv("FACTORY_TOKEN", "owner-secret")
	t.Setenv("NATS_URL", "nats://user:nats-secret@nats:4222")
	t.Setenv("NATS_PASSWORD", "nats-secret")
	t.Setenv("FACTORY_RUN_TOKEN", "stale-run-token")
	_, d := runFake(t, "completed.json", false)
	l := d.Launches()
	if len(l) != 1 || l[0].MCP != nil {
		t.Fatalf("launch %+v", l)
	}
	joined := strings.Join(l[0].Env, "\n")
	for _, secret := range []string{"owner-secret", "nats-secret", "stale-run-token", "FACTORY_TOKEN=", "NATS_URL=", "FACTORY_RUN_TOKEN="} {
		if strings.Contains(joined, secret) {
			t.Fatalf("runtime env inherits %q:\n%s", secret, joined)
		}
	}
	if !strings.Contains(joined, "PATH=") {
		t.Fatalf("runtime env lost the host environment:\n%s", joined)
	}
}

// Usage and quota go on their progress events and on the terminal event, here one without a result
// after a quota hit (paused_out); the run's total cost prices its single model.
func TestFakeDriverUsageWithoutResult(t *testing.T) {
	sc, err := fake.Load(filepath.Join("testdata", "fake", "usage_disconnected.json"))
	if err != nil {
		t.Fatal(err)
	}
	r, events := runDriver(t, Worker{Driver: &fake.Driver{Script: sc}}, false)
	if r.Status != "ENDED" || r.Outcome != "paused_out" || len(r.Usage) != 1 || r.Usage[0].Model != "gpt-5.5" || r.Usage[0].Input != 150 || r.Usage[0].CostUSD != 0.25 {
		t.Fatalf("%+v", r)
	}
	payloads := make([]protocol.Event, len(events))
	for i, e := range events {
		if err = json.Unmarshal(e.Payload, &payloads[i]); err != nil {
			t.Fatal(err)
		}
	}
	// started, delta, usage, usage, cost, quota, terminal
	if len(payloads) != 7 {
		t.Fatalf("%d events", len(payloads))
	}
	first := protocol.Usage{Model: "gpt-5.5", Input: 100, Output: 20, CacheRead: 300}
	last := protocol.Usage{Model: "gpt-5.5", Input: 150, Output: 40, CacheRead: 600}
	priced := last
	priced.CostUSD, priced.CostSource = 0.25, "fake_estimate"
	quota := protocol.Quota{ResetsAt: time.Date(2026, 10, 13, 23, 0, 0, 0, time.UTC), Type: "five_hour"}
	if payloads[1].Usage != nil || payloads[2].Method != "fake/usage" || len(payloads[2].Usage) != 1 || payloads[2].Usage[0] != first ||
		len(payloads[3].Usage) != 1 || payloads[3].Usage[0] != last || len(payloads[4].Usage) != 1 || payloads[4].Usage[0] != priced ||
		payloads[5].Quota == nil || !payloads[5].Quota.ResetsAt.Equal(quota.ResetsAt) || payloads[5].Quota.Type != quota.Type {
		t.Fatalf("progress %+v", payloads[1:6])
	}
	terminal := payloads[6]
	if terminal.Status != "ENDED" || terminal.Outcome != "paused_out" || len(terminal.Usage) != 1 || terminal.Usage[0] != priced || terminal.Quota == nil || terminal.Quota.Type != "five_hour" {
		t.Fatalf("terminal %+v", terminal)
	}
}

// endedDriver's turn completes as the run times out: the result is ready, and the runtime refuses the
// interrupt because no turn is active any more.
type endedDriver struct{ *fake.Driver }
type endedSession struct {
	events chan driver.Event
	done   chan driver.Result
	ended  chan struct{}
}

func (d endedDriver) Launch(context.Context, driver.Launch) (driver.Session, error) {
	return &endedSession{events: make(chan driver.Event), done: make(chan driver.Result, 1), ended: make(chan struct{})}, nil
}
func (s *endedSession) ID() string                  { return "ended-thread" }
func (s *endedSession) Start(context.Context) error { return nil }
func (s *endedSession) Events() <-chan driver.Event { return s.events }
func (s *endedSession) Done() <-chan driver.Result  { return s.done }
func (s *endedSession) ProcessGroup() int           { return 0 }
func (s *endedSession) Close()                      {}
func (s *endedSession) Interrupt(context.Context) error {
	// The turn ended meanwhile: its terminal evidence is about to arrive.
	s.done <- driver.Result{Status: "COMPLETED", Text: "finished just in time"}
	close(s.events)
	return errors.New("no active turn to interrupt")
}

// TestInterruptAfterTurnEnded: a refused interrupt is not terminal; the result that follows is.
func TestInterruptAfterTurnEnded(t *testing.T) {
	r, _ := runDriver(t, Worker{Driver: endedDriver{&fake.Driver{}}}, false, func(s *protocol.RunSpec) { s.TimeoutSeconds = 1 })
	if r.Outcome != "succeeded" || r.Result != "finished just in time" {
		t.Fatalf("%+v", r)
	}
}

func TestFakeDriverCancellation(t *testing.T) {
	r, _ := runFake(t, "hanging.json", true)
	if r.Status != "ENDED" || r.Outcome != "revoked" {
		t.Fatalf("%+v", r)
	}
}

// A scripted tool call goes through the real factory-mcp adapter with the run credential.
func TestFakeDriverToolCall(t *testing.T) {
	mcp := filepath.Join(t.TempDir(), "factory-mcp")
	if out, err := exec.Command("go", "build", "-o", mcp, "appfactory/cmd/factory-mcp").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	sc, err := fake.Load(filepath.Join("testdata", "fake", "tool_call.json"))
	if err != nil {
		t.Fatal(err)
	}
	r, events := runDriver(t, Worker{Driver: &fake.Driver{Script: sc, MCP: true}, MCPBinary: mcp}, false)
	if r.Outcome != "succeeded" || len(events) != len(sc.Activity)+2 {
		t.Fatalf("%+v, %d events", r, len(events))
	}
	var call fake.ToolEvent
	for _, e := range events {
		var p protocol.Event
		if json.Unmarshal(e.Payload, &p) == nil && p.Method == fake.ToolMethod {
			if err = json.Unmarshal(p.Data, &call); err != nil {
				t.Fatal(err)
			}
		}
	}
	var result struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	_ = json.Unmarshal(call.Result, &result)
	if call.Name != "factory_agent_list" || call.Error != "" || result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, `"name":"demo"`) {
		t.Fatalf("tool call %+v", call)
	}
}

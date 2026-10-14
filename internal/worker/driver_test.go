package worker

import (
	"appfactory/internal/bus"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// runFake executes one run on a recorded fake session and applies the published events
// to Core's store until the run is terminal. cancel cancels the run after it started.
func runFake(t *testing.T, script string, cancel bool) (storage.Run, *fake.Driver) {
	t.Helper()
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	sc, err := fake.Load(filepath.Join("testdata", "fake", script))
	if err != nil {
		t.Fatal(err)
	}
	d := &fake.Driver{Script: sc}
	r, events := runDriver(t, url, Worker{Driver: d}, cancel)
	if want := len(sc.Activity) + 2; len(events) != want {
		t.Fatalf("%d events, want started + %d progress + terminal", len(events), len(sc.Activity))
	}
	return r, d
}

// runDriver executes one run with w's driver against a fake Core, applying the published
// events to Core's store until the run is terminal; it returns the run and its stored events.
// stored edits the spec before Core stores the run as well (Core reads the captured config).
type stored func(*protocol.RunSpec)

// mods (func(*protocol.RunSpec) or stored) change the spec the executor receives.
func runDriver(t *testing.T, url string, w Worker, cancel bool, mods ...any) (storage.Run, []protocol.Envelope) {
	t.Helper()
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	defer b.JS.DeleteStream("FACTORY_COMMANDS")
	defer b.JS.DeleteStream("FACTORY_EVENTS")
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	c := &core.Core{Store: s, Bus: b, Token: "test-token", Agents: map[string]config.Agent{"demo": {Spec: config.Spec{Role: "executor"}}}}
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
	lease, err := s.Claim(ctx, storage.DefaultCompany, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := b.Subscribe("factory.events.>", "fake-test")
	if err != nil {
		t.Fatal(err)
	}
	for _, mod := range mods {
		switch m := mod.(type) {
		case stored:
			m(&spec)
		case func(*protocol.RunSpec):
			m(&spec)
		}
	}
	w.Bus, w.API = b, API{URL: server.URL, Token: "test-token"}
	done := make(chan error, 1)
	go func() { done <- w.execute(ctx, spec, lease, "fake-command") }()
	for {
		msgs, e := consumer.Fetch(1, nats.MaxWait(15*time.Second))
		if e != nil {
			t.Fatal(e)
		}
		var event protocol.Envelope
		if err = json.Unmarshal(msgs[0].Data, &event); err != nil {
			t.Fatal(err)
		}
		if err = s.Apply(ctx, storage.DefaultCompany, event); err != nil {
			t.Fatal(err)
		}
		_ = msgs[0].AckSync()
		var p protocol.Event
		_ = json.Unmarshal(event.Payload, &p)
		if p.SessionID != "" && cancel {
			if err = s.Cancel(ctx, storage.DefaultCompany, spec.ID); err != nil {
				t.Fatal(err)
			}
		}
		if p.Status != "" {
			break
		}
	}
	<-done
	// The run as Core serves it, so tests see what an owner sees (usage included).
	var r storage.Run
	if err = w.API.Do(ctx, "GET", "/runs/"+spec.ID, nil, &r); err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, storage.DefaultCompany, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	return r, events
}

func TestJetStreamFakeDriverOutcomes(t *testing.T) {
	for script, want := range map[string]struct{ status, result string }{
		"completed.json":    {"COMPLETED", "foundation ok"},
		"failed.json":       {"FAILED", "\n{\"message\":\"model error\"}"},
		"disconnected.json": {"UNKNOWN", "app-server disconnected: EOF"},
	} {
		t.Run(script, func(t *testing.T) {
			r, d := runFake(t, script, false)
			if r.Status != want.status || r.Result != want.result || r.SessionID != "recorded-thread" {
				t.Fatalf("%+v", r)
			}
			l := d.Launches()
			if len(l) != 1 || l[0].Instructions != "base" || l[0].MCP != nil || len(l[0].Env) == 0 {
				t.Fatalf("launch %+v", l)
			}
		})
	}
}

// TestJetStreamRuntimeEnvWithoutCredential: a driver without MCP and without command shims gets no
// run credential, yet its runtime still must not inherit the node's FACTORY_TOKEN or NATS settings.
func TestJetStreamRuntimeEnvWithoutCredential(t *testing.T) {
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

// Usage and quota go on their progress events and on the terminal event, here an UNKNOWN one
// without a result; the run's total cost prices its single model.
func TestJetStreamFakeDriverUsageOnUnknown(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	sc, err := fake.Load(filepath.Join("testdata", "fake", "usage_disconnected.json"))
	if err != nil {
		t.Fatal(err)
	}
	r, events := runDriver(t, url, Worker{Driver: &fake.Driver{Script: sc}}, false)
	if r.Status != "UNKNOWN" || len(r.Usage) != 1 || r.Usage[0].Model != "gpt-5.5" || r.Usage[0].Input != 150 || r.Usage[0].CostUSD != 0.25 {
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
	if terminal.Status != "UNKNOWN" || len(terminal.Usage) != 1 || terminal.Usage[0] != priced || terminal.Quota == nil || terminal.Quota.Type != "five_hour" {
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

// TestJetStreamInterruptAfterTurnEnded: a refused interrupt is not terminal; the result that follows is.
func TestJetStreamInterruptAfterTurnEnded(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	r, _ := runDriver(t, url, Worker{Driver: endedDriver{&fake.Driver{}}}, false, func(s *protocol.RunSpec) { s.TimeoutSeconds = 1 })
	if r.Status != "COMPLETED" || r.Result != "finished just in time" {
		t.Fatalf("%+v", r)
	}
}

func TestJetStreamFakeDriverCancellation(t *testing.T) {
	r, _ := runFake(t, "hanging.json", true)
	if r.Status != "CANCELLED" {
		t.Fatalf("%+v", r)
	}
}

// A scripted tool call goes through the real factory-mcp adapter with the run credential.
func TestJetStreamFakeDriverToolCall(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	mcp := filepath.Join(t.TempDir(), "factory-mcp")
	if out, err := exec.Command("go", "build", "-o", mcp, "appfactory/cmd/factory-mcp").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	sc, err := fake.Load(filepath.Join("testdata", "fake", "tool_call.json"))
	if err != nil {
		t.Fatal(err)
	}
	r, events := runDriver(t, url, Worker{Driver: &fake.Driver{Script: sc, MCP: true}, MCPBinary: mcp}, false)
	if r.Status != "COMPLETED" || len(events) != len(sc.Activity)+2 {
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

package worker

import (
	"appfactory/internal/bus"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/protocol"
	"appfactory/internal/rpc"
	"appfactory/internal/storage"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/nats-io/nats.go"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFakeAppServer(t *testing.T) {
	if os.Getenv("FACTORY_FAKE_SERVER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var m rpc.Message
		_ = json.Unmarshal(scanner.Bytes(), &m)
		if len(m.ID) == 0 {
			continue
		}
		var result any = map[string]any{}
		switch m.Method {
		case "account/read":
			result = map[string]any{"account": map[string]string{"type": "chatgpt"}}
		case "thread/start", "thread/resume":
			result = map[string]any{"thread": map[string]string{"id": "fake-thread"}}
		case "turn/start":
			result = map[string]any{"turn": map[string]string{"id": "fake-turn"}}
		}
		_ = enc.Encode(map[string]any{"id": m.ID, "result": result})
		if m.Method == "turn/interrupt" {
			_ = enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]string{"id": "fake-turn", "status": "interrupted"}}})
		}
		if m.Method == "turn/start" && !strings.Contains(string(m.Params), "CANCEL_TEST") {
			_ = enc.Encode(map[string]any{"method": "item/agentMessage/delta", "params": map[string]string{"delta": "foundation ok"}})
			_ = enc.Encode(map[string]any{"method": "item/completed", "params": map[string]any{"item": map[string]string{"type": "agentMessage", "text": "foundation ok"}}})
			_ = enc.Encode(map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]string{"id": "fake-turn", "status": "completed"}}})
		}
	}
	os.Exit(0)
}
func TestJetStreamFoundation(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to a dedicated JetStream instance")
	}
	t.Setenv("FACTORY_FAKE_SERVER", "1")
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	// This integration test requires a dedicated broker: reset its streams for isolation.
	defer b.JS.DeleteStream("FACTORY_COMMANDS")
	defer b.JS.DeleteStream("FACTORY_EVENTS")
	s, err := storage.Open(filepath.Join(t.TempDir(), "core.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &core.Core{Store: s, Bus: b, Agents: agents, Token: "test-token"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	errs := make(chan error, 2)
	go func() { errs <- c.Run(ctx) }()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	api := API{URL: server.URL, Token: "test-token"}
	w := Worker{Bus: b, API: api, Binary: bin, Args: []string{"-test.run=^TestFakeAppServer$"}, ShimBinary: buildShim(t)}
	go func() { errs <- w.Run(ctx) }()
	var spec protocol.RunSpec
	if err = api.Do(ctx, "POST", "/runs", map[string]string{"project": "test", "agent": "demo", "workspace": t.TempDir(), "prompt": "Reply foundation ok"}, &spec); err != nil {
		t.Fatal(err)
	}
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("integration timeout")
		case err = <-errs:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-tick.C:
			r, e := s.Get(ctx, storage.DefaultCompany, spec.ID)
			if e != nil {
				t.Fatal(e)
			}
			if r.Status == "FAILED" {
				t.Fatalf("run failed: %+v", r)
			}
			if r.Status != "COMPLETED" {
				continue
			}
			if r.Result != "foundation ok" || r.SessionID != "fake-thread" {
				t.Fatal(r)
			}
			events, e := s.Events(ctx, storage.DefaultCompany, spec.ID)
			if e != nil || len(events) < 4 {
				t.Fatal("events missing", e)
			}
			if _, e = s.Claim(ctx, storage.DefaultCompany, spec.ID); e == nil {
				t.Fatal("terminal run reclaimed")
			}
			// Duplicate stored event must not duplicate or alter result.
			if e = s.Apply(ctx, storage.DefaultCompany, events[len(events)-1]); e != nil {
				t.Fatal(e)
			}
			cancel()
			for i := 0; i < 2; i++ {
				select {
				case <-errs:
				case <-time.After(3 * time.Second):
					t.Fatal("service failed to shut down")
				}
			}
			return
		}
	}
}

func TestJetStreamCancellation(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	t.Setenv("FACTORY_FAKE_SERVER", "1")
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
	defer s.DB.Close()
	c := &core.Core{Store: s, Bus: b, Token: "test-token"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	spec := protocol.RunSpec{ID: "cancel-run", Project: "cancel-project", Agent: "demo", Provider: protocol.RuntimeCodex, Workspace: t.TempDir(), Prompt: "CANCEL_TEST", TimeoutSeconds: 30}
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	if err = s.Create(ctx, storage.DefaultCompany, spec); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Claim(ctx, storage.DefaultCompany, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := b.Subscribe("factory.events.>", "cancel-test")
	if err != nil {
		t.Fatal(err)
	}
	bin, _ := os.Executable()
	w := Worker{Bus: b, API: API{URL: server.URL, Token: "test-token"}, Binary: bin, Args: []string{"-test.run=^TestFakeAppServer$"}}
	done := make(chan error, 1)
	go func() { done <- w.execute(ctx, spec, lease, "cancel-command") }()
	// Request cancellation only after start is confirmed. Core's heartbeat must deliver it.
	cancelled := false
	terminal := false
	for !terminal {
		msgs, e := consumer.Fetch(1, nats.MaxWait(15*time.Second))
		if e != nil {
			t.Fatal(e)
		}
		for _, msg := range msgs {
			var event protocol.Envelope
			if err = json.Unmarshal(msg.Data, &event); err != nil {
				t.Fatal(err)
			}
			if err = s.Apply(ctx, storage.DefaultCompany, event); err != nil {
				t.Fatal(err)
			}
			_ = msg.AckSync()
			var payload protocol.Event
			_ = json.Unmarshal(event.Payload, &payload)
			if payload.SessionID != "" && !cancelled {
				if err = s.Cancel(ctx, storage.DefaultCompany, spec.ID); err != nil {
					t.Fatal(err)
				}
				cancelled = true
			}
			if payload.Status != "" {
				if payload.Status != "CANCELLED" {
					t.Fatalf("unexpected terminal %+v", payload)
				}
				terminal = true
			}
		}
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("runtime shutdown timeout")
	}
	saved, err := s.Get(ctx, storage.DefaultCompany, spec.ID)
	if err != nil || saved.Status != "CANCELLED" {
		t.Fatal(saved, err)
	}
}

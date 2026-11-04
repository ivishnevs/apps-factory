package worker

import (
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/protocol"
	"appfactory/internal/rpc"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/testnode"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
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
func TestFoundation(t *testing.T) {
	t.Setenv("FACTORY_FAKE_SERVER", "1")
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &core.Core{Store: s, Agents: agents, Token: "test-token"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	errs := make(chan error, 2)
	coreDone := startCore(ctx, c)
	go func() { errs <- <-coreDone }()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	api := API{URL: server.URL, Token: "test-token"}
	w := Worker{Binary: bin, Args: []string{"-test.run=^TestFakeAppServer$"}, ShimBinary: buildShim(t)}
	asNode(t, s, &w, server.URL)
	go func() { errs <- runOffers(ctx, &w) }()
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
			if r.Status != "ENDED" {
				continue
			}
			if r.Outcome != "succeeded" {
				t.Fatalf("run failed: %+v", r)
			}
			if r.Result != "foundation ok" || r.SessionID != "fake-thread" {
				t.Fatal(r)
			}
			events, e := s.Events(ctx, storage.DefaultCompany, spec.ID)
			if e != nil || len(events) < 4 {
				t.Fatal("events missing", e)
			}
			if _, e = testnode.Claim(t, s, storage.DefaultCompany, spec.ID); e == nil {
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

func TestCancellation(t *testing.T) {
	t.Setenv("FACTORY_FAKE_SERVER", "1")
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	c := &core.Core{Store: s, Token: "test-token"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	spec := protocol.RunSpec{ID: "cancel-run", Project: "cancel-project", Agent: "demo", Provider: protocol.RuntimeCodex, Workspace: t.TempDir(), Prompt: "CANCEL_TEST", TimeoutSeconds: 30}
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	if err = s.Create(ctx, storage.DefaultCompany, spec); err != nil {
		t.Fatal(err)
	}
	bin, _ := os.Executable()
	w := Worker{Binary: bin, Args: []string{"-test.run=^TestFakeAppServer$"}}
	n := asNode(t, s, &w, server.URL)
	epoch, claim, err := w.ClaimV2(ctx, spec.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stopRun := context.WithCancelCause(ctx)
	defer stopRun(nil)
	if !w.Inventory.AddCause(spec.ID, epoch, stopRun) {
		t.Fatal("claim not recorded")
	}
	// Request cancellation only after start is confirmed. The node's heartbeat answer must deliver it.
	var once sync.Once
	var stopRunning func()
	w.onEmit = func(_ string, payload protocol.Event) {
		if payload.SessionID == "" {
			return
		}
		once.Do(func() { go stopRunning() })
	}
	stopRunning = func() {
		if err := s.Cancel(ctx, storage.DefaultCompany, spec.ID); err != nil {
			t.Error(err)
			return
		}
		var answer storage.HeartbeatAnswer
		hb := storage.NodeHeartbeat{Epoch: epoch, Inventory: []storage.InventoryRun{{RunID: spec.ID, State: "running"}}}
		if err := w.API.Do(ctx, "POST", "/nodes/"+n.ID+"/heartbeat", hb, &answer); err != nil {
			t.Error(err)
			return
		}
		if !slices.Contains(answer.Stop, storage.Stop{RunID: spec.ID, Reason: "revoked"}) {
			t.Errorf("%+v", answer)
		}
		for _, stop := range answer.Stop {
			w.Inventory.Stop(stop.RunID, stop.Reason)
		}
	}
	done := make(chan error, 1)
	go func() { done <- w.execute(runCtx, claim.RunSpec, claim.Lease, "cancel-command") }()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("runtime shutdown timeout")
	}
	saved, err := s.Get(ctx, storage.DefaultCompany, spec.ID)
	if err != nil || saved.Outcome != "revoked" {
		t.Fatal(saved, err)
	}
	events, err := s.Events(ctx, storage.DefaultCompany, spec.ID)
	if err != nil || len(events) == 0 {
		t.Fatal(events, err)
	}
	var terminal protocol.Event
	_ = json.Unmarshal(events[len(events)-1].Payload, &terminal)
	if terminal.Status != "ENDED" || terminal.Outcome != "revoked" {
		t.Fatalf("unexpected terminal %+v", terminal)
	}
}

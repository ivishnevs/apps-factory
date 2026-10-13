package worker

import (
	"appfactory/internal/bus"
	"appfactory/internal/core"
	"appfactory/internal/driver/fake"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	if want := len(sc.Activity) + 2; events != want {
		t.Fatalf("%d events, want started + %d progress + terminal", events, len(sc.Activity))
	}
	return r, d
}

// runDriver executes one run with w's driver against a fake Core, applying the published
// events to Core's store until the run is terminal; it returns the run and its event count.
func runDriver(t *testing.T, url string, w Worker, cancel bool) (storage.Run, int) {
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
	c := &core.Core{Store: s, Bus: b, Token: "test-token"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	spec := protocol.RunSpec{ID: "fake-run", Project: "fake-project", Agent: "demo", Provider: w.Driver.Name(), Workspace: t.TempDir(), Prompt: "work", Instructions: "base", TimeoutSeconds: 30}
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
	r, err := s.Get(ctx, storage.DefaultCompany, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, storage.DefaultCompany, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	return r, len(events)
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
			if len(l) != 1 || l[0].Instructions != "base" || l[0].MCP != nil || l[0].Env != nil {
				t.Fatalf("launch %+v", l)
			}
		})
	}
}

func TestJetStreamFakeDriverCancellation(t *testing.T) {
	r, _ := runFake(t, "hanging.json", true)
	if r.Status != "CANCELLED" {
		t.Fatalf("%+v", r)
	}
}

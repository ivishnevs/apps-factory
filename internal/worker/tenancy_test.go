package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/driver/fake"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"

	"github.com/nats-io/nats.go"
)

// TestJetStreamCompaniesDoNotShareCommands (postgres-tenancy 2.5): two companies' run commands go through
// the same stream on factory.<company>.commands...; each company's executors receive only their company's
// commands, and an event of one company never reaches the other company's Core consumer.
func TestJetStreamCompaniesDoNotShareCommands(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated broker")
	}
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	defer b.JS.DeleteStream("FACTORY_COMMANDS")
	defer b.JS.DeleteStream("FACTORY_EVENTS")
	s := storagetest.Open(t)
	ctx := context.Background()
	const acme = "acme"
	if err = s.CreateCompany(ctx, acme, "Acme"); err != nil {
		t.Fatal(err)
	}
	companies := []string{storage.DefaultCompany, acme}
	for _, co := range companies {
		for i := range 3 {
			spec := protocol.RunSpec{ID: co + "-run-" + string(rune('a'+i)), Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex, Workspace: "/tmp/" + co + "-" + string(rune('a'+i)), Prompt: "work", TimeoutSeconds: 30}
			if err = s.Create(ctx, co, spec); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Core's outbox publisher serves every company.
	if n, err := s.PublishOutbox(ctx, func(ctx context.Context, o storage.Outbox) error { return b.Publish(ctx, o.Subject, o.ID, o.Body) }, nil); err != nil || n != 6 {
		t.Fatal(n, err)
	}
	for _, co := range companies {
		w := &Worker{Bus: b, Company: co, Driver: &fake.Driver{}}
		sub, err := w.Subscribe()
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for {
			msgs, err := sub.Fetch(1, nats.MaxWait(500*time.Millisecond))
			if errors.Is(err, nats.ErrTimeout) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			_, run := pointer(msgs[0].Data)
			if msgs[0].Subject != protocol.CommandSubject(co, protocol.RuntimeCodex) || len(run) < len(co) || run[:len(co)] != co {
				t.Fatalf("%s's executor got %s on %s", co, run, msgs[0].Subject)
			}
			got[run] = true
			_ = msgs[0].AckSync()
		}
		if len(got) != 3 {
			t.Fatalf("%s's executor got %v", co, got)
		}
	}
	// An event of acme (its executor's terminal event) is not on the default company's Core subject.
	events, err := b.Subscribe(protocol.EventSubject(storage.DefaultCompany, ">"), "core-events-test")
	if err != nil {
		t.Fatal(err)
	}
	if err = b.Event(ctx, acme, "completed", "", protocol.Event{RunID: acme + "-run-a", Status: "ENDED", Outcome: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	if msgs, err := events.Fetch(1, nats.MaxWait(500*time.Millisecond)); !errors.Is(err, nats.ErrTimeout) {
		var e protocol.Envelope
		if len(msgs) > 0 {
			_ = json.Unmarshal(msgs[0].Data, &e)
		}
		t.Fatalf("default company's Core received %+v (%v)", e, err)
	}
	if got := protocol.SubjectCompany(protocol.EventSubject(acme, "agent.completed")); got != acme {
		t.Fatal(got)
	}
}

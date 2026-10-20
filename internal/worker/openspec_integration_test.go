package worker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"appfactory/internal/bus"
	"appfactory/internal/protocol"

	"github.com/nats-io/nats.go"
)

func TestJetStreamOpenSpecPreflightBeforeRuntime(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated JetStream")
	}
	b, e := bus.Connect(url)
	if e != nil {
		t.Fatal(e)
	}
	defer b.NC.Close()
	defer b.JS.DeleteStream("FACTORY_COMMANDS")
	defer b.JS.DeleteStream("FACTORY_EVENTS")
	sub, e := b.Subscribe("factory.default.events.>", "spec-preflight-test")
	if e != nil {
		t.Fatal(e)
	}
	defer sub.Unsubscribe()
	w := Worker{Bus: b, Binary: "/nonexistent-runtime-must-not-start"}
	spec := protocol.RunSpec{ID: "preflight", Project: "p", Agent: "demo", Workspace: t.TempDir(), Provider: protocol.RuntimeCodex, TimeoutSeconds: 30, OpenSpecChange: "missing"}
	if e = w.execute(context.Background(), spec, "test-lease", "cause"); e != nil {
		t.Fatal(e)
	}
	msgs, e := sub.Fetch(1, nats.MaxWait(2e9))
	if e != nil {
		t.Fatal(e)
	}
	var envelope protocol.Envelope
	var event protocol.Event
	if e = json.Unmarshal(msgs[0].Data, &envelope); e != nil {
		t.Fatal(e)
	}
	json.Unmarshal(envelope.Payload, &event)
	if envelope.Type != "agent.failed" || event.Status != "ENDED" || event.Outcome != "setup_failed" || event.SessionID != "" || !strings.Contains(event.Result, "before runtime startup") {
		t.Fatal(envelope, event)
	}
	msgs[0].AckSync()
}

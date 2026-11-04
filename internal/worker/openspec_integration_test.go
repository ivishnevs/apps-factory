package worker

import (
	"context"
	"strings"
	"testing"

	"appfactory/internal/node"
	"appfactory/internal/protocol"
)

// TestOpenSpecPreflightBeforeRuntime (openspec-in-git D3): a linked change that is not on the
// checkout ends the run change_missing before the runtime starts.
func TestOpenSpecPreflightBeforeRuntime(t *testing.T) {
	type emitted struct {
		kind  string
		event protocol.Event
	}
	var got []emitted
	w := Worker{Binary: "/nonexistent-runtime-must-not-start", Inventory: node.NewInventory("n1", 1)}
	w.onEmit = func(kind string, p protocol.Event) { got = append(got, emitted{kind, p}) }
	spec := protocol.RunSpec{ID: "preflight", Project: "p", Agent: "demo", Workspace: t.TempDir(), Provider: protocol.RuntimeCodex, TimeoutSeconds: 30, OpenSpecChange: "missing"}
	if e := w.execute(context.Background(), spec, "test-lease", "cause"); e != nil {
		t.Fatal(e)
	}
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	event := got[0].event
	if got[0].kind != "failed" || event.Status != "ENDED" || event.Outcome != "change_missing" || event.SessionID != "" || !strings.Contains(event.Result, "is not on the task's checkout") {
		t.Fatal(got[0])
	}
}

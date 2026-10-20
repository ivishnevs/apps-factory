package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// TestJetStreamUrgentStopOverControlSubject (explicit-exits-authority 3.5): with the heartbeat 30 s away and
// ten run commands waiting in the command stream, an urgent directive stops the node's run within a second
// through the node's control subject; the run ends revoked.
func TestJetStreamUrgentStopOverControlSubject(t *testing.T) {
	agents, err := config.Load("../../..")
	if err != nil {
		t.Fatal(err)
	}
	e2eHeartbeat = 30
	t.Cleanup(func() { e2eHeartbeat = 1 })
	e := startE2E(t, false, agents, &fake.Driver{}) // no status: a run lasts until it is stopped
	co := storage.DefaultCompany
	e.task("u")
	var run storage.Run
	e.wait("run started", func() bool {
		task, _ := e.store.Task(e.ctx, co, "u")
		run, _ = e.store.Get(e.ctx, co, task.CurrentRunID)
		return run.Status == "RUNNING"
	})
	// The node's only slot is busy: these commands wait in the stream.
	for i := range 10 {
		spec := protocol.RunSpec{ID: "queued-" + string(rune('a'+i)), Project: "notes", Agent: "demo", Provider: protocol.RuntimeCodex, Workspace: t.TempDir(), Prompt: "later", TimeoutSeconds: 120}
		if err = e.store.Create(e.ctx, co, spec); err != nil {
			t.Fatal(err)
		}
	}
	e.wait("commands published", func() bool {
		var pending int
		_ = e.store.DB.QueryRow("SELECT count(*) FROM outbox WHERE sent=0").Scan(&pending)
		return pending == 0
	})
	body, _ := json.Marshal(map[string]any{"text": "stop: the API changed", "urgent": true, "request_id": "urgent"})
	req, _ := http.NewRequestWithContext(e.ctx, "POST", e.server.URL+"/tasks/u/directives", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	sent := time.Now()
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatal(res, err)
	}
	res.Body.Close()
	e.wait("revoked", func() bool {
		r, _ := e.store.Get(e.ctx, co, run.Spec.ID)
		return r.Status == "ENDED"
	})
	r, _ := e.store.Get(e.ctx, co, run.Spec.ID)
	if took := time.Since(sent); r.Outcome != "revoked" || took > time.Second+200*time.Millisecond {
		// The wait polls every 100 ms; the stop itself must arrive within a second.
		t.Fatalf("stop took %s, outcome %s", took, r.Outcome)
	}
}

// TestControlMessageStopsTheRun: run.stop on the control subject stops the listed run with Core's reason
// (outcome revoked); other or broken messages change nothing.
func TestControlMessageStopsTheRun(t *testing.T) {
	s := &session{inv: node.NewInventory("n1", 1)}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	s.inv.AddCause("r1", 1, cancel)
	s.inv.Set("r1", node.RunRunning)
	s.control([]byte("{"))
	other, _ := json.Marshal(protocol.Wrap("run.stop", "r2", "", protocol.RunStop{RunID: "r2", Reason: "revoked"}))
	s.control(other)
	if ctx.Err() != nil {
		t.Fatal("stopped by another run's message")
	}
	stop, _ := json.Marshal(protocol.Wrap("run.stop", "r1", "", protocol.RunStop{RunID: "r1", Reason: "revoked"}))
	s.control(stop)
	var cause *node.StopCause
	if c, ok := context.Cause(ctx).(*node.StopCause); !ok || c.Reason != "revoked" {
		t.Fatal(context.Cause(ctx), cause)
	}
}

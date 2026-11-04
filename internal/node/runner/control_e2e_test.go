package runner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/driver/fake"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// TestUrgentStopThroughWaitingPoll (explicit-exits-authority 3.5, http-node-transport 6.1): with the heartbeat 30 s
// away and ten runs queued behind the node's busy slot, an urgent directive stops the node's run within a second
// through the answer of its waiting poll; the run ends revoked.
func TestUrgentStopThroughWaitingPoll(t *testing.T) {
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
	// The node's only slot is busy: these runs stay queued and are not offered.
	for i := range 10 {
		spec := protocol.RunSpec{ID: "queued-" + string(rune('a'+i)), Project: "notes", Agent: "demo", Provider: protocol.RuntimeCodex, Workspace: t.TempDir(), Prompt: "later", TimeoutSeconds: 120}
		if err = e.store.Create(e.ctx, co, spec); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(500 * time.Millisecond) // the node waits in its poll
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

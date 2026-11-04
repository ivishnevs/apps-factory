package runner

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/worker"
)

// enrolledIn is enrolled for a node of company.
func enrolledIn(t *testing.T, s *storage.Store, company, stateDir, url string) worker.API {
	t.Helper()
	ctx := context.Background()
	keys, err := node.GenerateKeys(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := node.NodeID(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.CreateNodeEnrollment(ctx, company, "node-"+company)
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.EnrollNode(ctx, storage.EnrollRequest{Code: code.Code, NodeID: id, SignPub: hex.EncodeToString(keys.SignPublic()), BoxPub: hex.EncodeToString(keys.BoxPublic())})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApproveNodeKey(ctx, company, id, k.Fingerprint); err != nil {
		t.Fatal(err)
	}
	return worker.API{URL: url, Node: id, Key: keys.Sign, Now: s.Now}
}

// gate makes Core unreachable while down is set (every request answers 503), as a Core restart does.
type gate struct {
	h    http.Handler
	down atomic.Bool
}

func (g *gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if g.down.Load() {
		http.Error(w, "core restarting", http.StatusServiceUnavailable)
		return
	}
	g.h.ServeHTTP(w, r)
}

// TestTransportEndToEnd (http-node-transport 6.1): real Core, three real nodes over signed HTTP only. Queued runs
// are offered to both nodes of the company and each run is claimed by exactly one (two nodes race every offer);
// the node of another company gets nothing; Core unreachable while a run works and ends leaves the events in
// the node's spool, and once Core is back they arrive in order and the run ends with its real outcome.
func TestTransportEndToEnd(t *testing.T) {
	beat := storage.HeartbeatSeconds
	storage.HeartbeatSeconds = 1
	defer func() { storage.HeartbeatSeconds = beat }()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err = s.EnsureCompany(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	c := &core.Core{Store: s, Token: "test-token"}
	g := &gate{h: c.Handler()}
	server := httptest.NewServer(g)
	defer server.Close()
	go c.Run(ctx)
	co := storage.DefaultCompany
	// Each run works a moment (a shell step) and completes.
	d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "done", Activity: []fake.Activity{{Exec: []string{"sleep", "1"}}}}}
	start := func(company string) string {
		cfg := node.Defaults()
		cfg.Slots, cfg.StateDir, cfg.Poll.WaitSeconds = 1, t.TempDir(), 2
		api := enrolledIn(t, s, company, cfg.StateDir, server.URL)
		nodeCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- Run(nodeCtx, Node{Config: cfg, API: api, Drivers: []driver.Driver{perRunSessions{d}}}) }()
		t.Cleanup(func() { stop(); <-done })
		return api.Node
	}
	a, b, other := start(co), start(co), start("other")
	wait := func(what string, ok func() bool) {
		t.Helper()
		for !ok() {
			select {
			case <-ctx.Done():
				t.Fatal("timeout waiting for", what)
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	wait("nodes registered", func() bool {
		ns, _ := s.Nodes(ctx, co)
		os, _ := s.Nodes(ctx, "other")
		return len(ns) == 2 && len(os) == 1
	})
	create := func(id string) {
		t.Helper()
		spec := protocol.RunSpec{ID: id, Project: id, Agent: "demo", Provider: protocol.RuntimeCodex, Workspace: t.TempDir(), Prompt: "work", TimeoutSeconds: 60}
		if err := s.Create(ctx, co, spec); err != nil {
			t.Fatal(err)
		}
	}
	runIDs := []string{"t-1", "t-2", "t-3", "t-4"}
	for _, id := range runIDs {
		create(id)
	}
	ended := func(id string) storage.Run {
		var r storage.Run
		wait(id+" ended", func() bool { r, _ = s.Get(ctx, co, id); return r.Status == "ENDED" })
		return r
	}
	used := map[string]int{}
	for _, id := range runIDs {
		r := ended(id)
		if r.Outcome != "succeeded" {
			t.Fatalf("%s: %+v", id, r)
		}
		var node string
		_ = s.DB.QueryRow("SELECT node_id FROM runs WHERE id=$1", id).Scan(&node)
		used[node]++
		var attempts int
		_ = s.DB.QueryRow("SELECT count(*) FROM events WHERE run_id=$1 AND body->>'type' IN ('agent.completed','agent.failed')", id).Scan(&attempts)
		if attempts != 1 {
			t.Fatalf("%s ended %d times", id, attempts)
		}
	}
	if used[other] != 0 || used[a]+used[b] != len(runIDs) {
		t.Fatalf("runs per node %v (a %s, b %s, other %s)", used, a, b, other)
	}

	// Core unreachable while a run works and ends: the events wait in the node's spool and arrive in order.
	create("t-down")
	wait("t-down running", func() bool { r, _ := s.Get(ctx, co, "t-down"); return r.Status == "RUNNING" })
	g.down.Store(true)
	time.Sleep(3 * time.Second) // the run's shell step ends meanwhile; its terminal event is spooled
	if r, _ := s.Get(ctx, co, "t-down"); r.Status == "ENDED" {
		t.Fatal("the run ended while Core was unreachable")
	}
	g.down.Store(false)
	if r := ended("t-down"); r.Outcome != "succeeded" {
		t.Fatalf("%+v", r)
	}
	var seqs []int64
	rows, err := s.DB.Query("SELECT seq FROM events WHERE run_id='t-down' ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n int64
		_ = rows.Scan(&n)
		seqs = append(seqs, n)
	}
	rows.Close()
	var applied int64
	_ = s.DB.QueryRow("SELECT events_seq FROM runs WHERE id='t-down'").Scan(&applied)
	if len(seqs) < 2 || applied != int64(len(seqs)) {
		t.Fatalf("events %v, applied seq %d", seqs, applied)
	}
}

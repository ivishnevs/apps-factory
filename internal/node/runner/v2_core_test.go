package runner

import (
	"context"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/worker"

	"net/http/httptest"
)

// enrolled gives the node with stateDir approved keys at Core, as `factory-node enroll` does, and the client it signs with.
func enrolled(t testing.TB, s *storage.Store, stateDir, url string) worker.API {
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
	code, err := s.CreateNodeEnrollment(ctx, storage.DefaultCompany, "test-node")
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.EnrollNode(ctx, storage.EnrollRequest{Code: code.Code, NodeID: id, SignPub: hex.EncodeToString(keys.SignPublic()), BoxPub: hex.EncodeToString(keys.BoxPublic())})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApproveNodeKey(ctx, storage.DefaultCompany, id, k.Fingerprint); err != nil {
		t.Fatal(err)
	}
	return worker.API{URL: url, Node: id, Key: keys.Sign, Now: s.Now}
}

// TestJetStreamNodeV2RealCore runs a protocol v2 node against the real Core (runtime protocol v2
// integration): register, two-phase claim and start, inventory heartbeat, an owner cancel delivered as a
// heartbeat stop, the acknowledged end leaving the inventory, and a Core incarnation change that makes the
// node stop everything and register epoch 2, whose claims Core accepts.
func TestJetStreamNodeV2RealCore(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to a dedicated JetStream instance")
	}
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	defer b.JS.DeleteStream("FACTORY_COMMANDS")
	defer b.JS.DeleteStream("FACTORY_EVENTS")
	beat := storage.HeartbeatSeconds
	storage.HeartbeatSeconds = 1
	defer func() { storage.HeartbeatSeconds = beat }()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &core.Core{Store: s, Bus: b, Agents: agents, Token: "test-token"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	go c.Run(ctx)
	co := storage.DefaultCompany
	create := func(id string) {
		t.Helper()
		spec := protocol.RunSpec{ID: id, Project: id, Agent: id, Provider: protocol.RuntimeCodex, Workspace: t.TempDir(), Prompt: "work", TimeoutSeconds: 120}
		if err := s.Create(ctx, co, spec); err != nil {
			t.Fatal(err)
		}
	}
	type row struct {
		status, node, result, outcome string
		epoch, started                int64
	}
	get := func(id string) row {
		var r row
		_ = s.DB.QueryRow("SELECT status,node_id,result,epoch,run_started,outcome FROM runs WHERE id=?", id).Scan(&r.status, &r.node, &r.result, &r.epoch, &r.started, &r.outcome)
		return r
	}
	cfg := node.Defaults()
	cfg.Slots, cfg.StateDir = 3, t.TempDir()
	api := enrolled(t, s, cfg.StateDir, server.URL)
	nodeCtx, stopNode := context.WithCancel(ctx)
	done := make(chan error, 1)
	d := &fake.Driver{} // no status: a run lasts until it is stopped
	go func() {
		done <- Run(nodeCtx, Node{Config: cfg, Bus: b, API: api, Drivers: []driver.Driver{perRunSessions{d}}, FetchWait: 200 * time.Millisecond})
	}()
	wait := func(what string, ok func() bool) {
		t.Helper()
		for !ok() {
			select {
			case err := <-done:
				t.Fatal("node stopped", err)
			case <-ctx.Done():
				t.Fatal("timeout waiting for", what)
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	nodeOf := func() storage.Node {
		ns, _ := s.Nodes(ctx, co)
		if len(ns) != 1 {
			return storage.Node{}
		}
		return ns[0]
	}
	wait("register", func() bool { return nodeOf().Epoch == 1 })
	create("v2-a")
	create("v2-b")
	wait("both runs started", func() bool {
		a, b := get("v2-a"), get("v2-b")
		return a.status == "RUNNING" && a.started > 0 && b.status == "RUNNING" && b.started > 0
	})
	n := nodeOf()
	for _, id := range []string{"v2-a", "v2-b"} {
		if r := get(id); r.node != n.NodeID || r.epoch != 1 {
			t.Fatalf("%s: %+v", id, r)
		}
	}
	wait("session reported", func() bool { r, _ := s.Get(ctx, co, "v2-a"); return r.SessionID == "session-v2-a" })
	// Leases live on the inventory heartbeat alone: the runs still run after a wait.
	time.Sleep(3 * time.Second)
	if a := get("v2-a"); a.status != "RUNNING" || len(n.Drivers) != 1 || !n.Drivers[0].OK {
		t.Fatalf("%+v %+v", a, n)
	}

	// Owner cancel: CANCELLING reaches the node as a heartbeat stop; the node ends the run and Core
	// records it revoked.
	if err = s.Cancel(ctx, co, "v2-a"); err != nil {
		t.Fatal(err)
	}
	wait("cancelled", func() bool { r := get("v2-a"); return r.status == "ENDED" && r.outcome == "revoked" })

	// Database restored: a new incarnation makes the node stop everything and register epoch 2. Core
	// loses v2-b with epoch 1 (lost); the node's own end of it confirms the runtime gone.
	if _, err = s.DB.Exec("UPDATE core_meta SET value='restored' WHERE key='incarnation'"); err != nil {
		t.Fatal(err)
	}
	wait("epoch 2", func() bool { return nodeOf().Epoch == 2 })
	wait("v2-b ended", func() bool { return get("v2-b").status == "ENDED" })
	create("v2-c")
	wait("epoch 2 run started", func() bool { r := get("v2-c"); return r.status == "RUNNING" && r.started > 0 && r.epoch == 2 })
	stopNode()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	// A node shutdown mid-run is a loss, never a revoke.
	wait("shutdown ends the run", func() bool { r := get("v2-c"); return r.status == "ENDED" && r.outcome == "lost" })
	if r := get("v2-b"); r.status != "ENDED" {
		t.Fatalf("%+v", r)
	}
	t.Logf("v2-b after the incarnation change: %+v", get("v2-b"))
}

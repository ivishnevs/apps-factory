package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/testnode"
	"appfactory/internal/worker"
)

// clockNode is a node's API to c served over HTTP, whose own clock is ahead of Core's by ahead.
func clockNode(t *testing.T, c *Core, s *storage.Store, n testnode.Node, ahead time.Duration, clk *worker.Clock) worker.API {
	t.Helper()
	srv := httptest.NewServer(c.Handler())
	t.Cleanup(srv.Close)
	return worker.API{URL: srv.URL, Node: n.ID, Key: n.Key, Now: func() time.Time { return s.Now().Add(ahead) }, Clock: clk}
}

func feedTypes(t *testing.T, s *storage.Store, kind string) int {
	t.Helper()
	page, err := s.Feed(context.Background(), storage.DefaultCompany, storage.FeedFilter{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, item := range page.Items {
		if item.Type == kind {
			n++
		}
	}
	return n
}

// TestNodeClockSkew (node-wake-recovery 4.1-4.3): Core's clock is 5 minutes behind the node's after a wake. The
// node's first request is refused with Core's time, the retry signed with the corrected time is accepted at once,
// the heartbeat reports the offset and the owner sees one node.clock_skew.
func TestNodeClockSkew(t *testing.T) {
	c, s, _ := nodeCore(t)
	ctx := context.Background()
	n := testnode.Enroll(t, s, storage.DefaultCompany)
	clk := new(worker.Clock)
	api := clockNode(t, c, s, n, 5*time.Minute, clk)
	if err := api.Do(ctx, "POST", "/nodes/register", registration(n.ID, 1), nil); err != nil {
		t.Fatal("register not retried with the corrected time:", err)
	}
	if off := clk.Offset(); off != -5*time.Minute {
		t.Fatalf("offset %s, want -5m", off)
	}
	if got := feedTypes(t, s, "node.signature_refused"); got != 1 {
		t.Fatalf("%d refusals, want the first request only", got)
	}
	for range 3 {
		health, _ := json.Marshal(map[string]int64{"clock_offset_ms": clk.Observed().Milliseconds()})
		if err := api.Do(ctx, "POST", "/nodes/"+n.ID+"/heartbeat", storage.NodeHeartbeat{Epoch: 1, Health: health}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := feedTypes(t, s, "node.clock_skew"); got != 1 {
		t.Fatalf("%d node.clock_skew, want one per episode", got)
	}
	if got := feedTypes(t, s, "node.signature_refused"); got != 1 {
		t.Fatalf("%d refusals after the correction, want none more", got)
	}
}

// TestNodeClockWakeReplay (node-wake-recovery 4.4): after a wake Core refuses the node's heartbeats for the time
// window for 30 s, past the node's TTL; then a corrected heartbeat lands. The node stays online and its run is not
// lost: the gateway credits the refused heartbeats for one more TTL.
func TestNodeClockWakeReplay(t *testing.T) {
	c, s, fake := nodeCore(t)
	ctx := context.Background()
	n := testnode.Enroll(t, s, storage.DefaultCompany)
	api := clockNode(t, c, s, n, 0, new(worker.Clock))
	if err := api.Do(ctx, "POST", "/nodes/register", registration(n.ID, 1), nil); err != nil {
		t.Fatal(err)
	}
	spec := protocol.RunSpec{ID: "r1", Project: "p1", Agent: "dev", Provider: protocol.RuntimeCodex, Workspace: "/tmp/r1", Prompt: "work", TimeoutSeconds: 3600}
	if err := s.Create(ctx, storage.DefaultCompany, spec); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimV2(ctx, storage.DefaultCompany, "r1", storage.ClaimRequest{NodeID: n.ID, Epoch: 1, ClaimID: "1-0-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRun(ctx, storage.DefaultCompany, "r1", claimed.Lease); err != nil {
		t.Fatal(err)
	}
	beat := func(api worker.API) error {
		return api.Do(ctx, "POST", "/nodes/"+n.ID+"/heartbeat", storage.NodeHeartbeat{Epoch: 1, Inventory: []storage.InventoryRun{{RunID: "r1", State: "running"}}}, nil)
	}
	if err = beat(api); err != nil {
		t.Fatal(err)
	}
	// The host slept 50 s; the node's clock is now 5 minutes ahead of Core's. This node does not correct (no
	// Clock), so its heartbeats are refused while Core expires as usual.
	fake.Advance(50 * time.Second)
	stale := clockNode(t, c, s, n, 5*time.Minute, nil)
	for range 4 {
		if err = beat(stale); err == nil {
			t.Fatal("a heartbeat outside the window was accepted")
		}
		if err = s.Expire(ctx, storage.DefaultCompany); err != nil {
			t.Fatal(err)
		}
		if err = s.ExpireNodes(ctx, storage.DefaultCompany); err != nil {
			t.Fatal(err)
		}
		fake.Advance(10 * time.Second)
	}
	// The corrected heartbeat lands 90 s after the last accepted one.
	corrected := clockNode(t, c, s, n, 5*time.Minute, new(worker.Clock))
	if err = beat(corrected); err != nil {
		t.Fatal(err)
	}
	run, err := s.Get(ctx, storage.DefaultCompany, "r1")
	if err != nil || run.Status != "RUNNING" {
		t.Fatalf("run %s %s %v, want RUNNING", run.Status, run.Result, err)
	}
	if got := feedTypes(t, s, "node.offline"); got != 0 {
		t.Fatal("the node went offline during the refusals")
	}
}

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

func nodeCore(t *testing.T) (*Core, *storage.Store, *clock.Fake) {
	t.Helper()
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	var dev config.Agent
	dev.Metadata.Name = "dev"
	dev.Spec.Runtime.Provider = "codex"
	dev.Spec.Workspace.Mode = "worktree"
	return &Core{Store: s, Token: "owner", Agents: map[string]config.Agent{"dev": dev}}, s, fake
}

// call sends one request with token to c's handler and returns the status and body.
func call(t *testing.T, c *Core, method, path, token string, body any) (int, string) {
	t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func registration(node string, epoch int64) storage.NodeRegistration {
	return storage.NodeRegistration{NodeID: node, Name: node, Epoch: epoch, ProtocolVersion: 2, Slots: 2, Worktrees: true,
		Drivers: []storage.NodeDriver{{Name: "codex", Version: "codex-cli 0.162.1", OK: true}}, Resources: map[string]int{}, Labels: []string{}}
}

// TestNodeTokenAuth: a node token reaches only the node routes and only for the node it is bound to (bound at
// issue or by its first register); owner routes refuse it; revoking it locks the node out; the shared token
// keeps working for nodes during the transition.
func TestNodeTokenAuth(t *testing.T) {
	c, s, _ := nodeCore(t)
	ctx := context.Background()
	issue := func(body map[string]string) storage.IssuedNodeToken {
		t.Helper()
		code, out := call(t, c, "POST", "/node-tokens", "owner", body)
		var v storage.IssuedNodeToken
		if code != 200 || json.Unmarshal([]byte(out), &v) != nil || v.Token == "" {
			t.Fatal(code, out)
		}
		return v
	}
	a := issue(map[string]string{"name": "mini-1"})
	b := issue(map[string]string{"name": "mini-2", "node_id": "n2"})
	expect := func(want int, method, path, token string, body any) {
		t.Helper()
		if code, out := call(t, c, method, path, token, body); code != want {
			t.Fatalf("%s %s: %d %s, want %d", method, path, code, out, want)
		}
	}
	expect(403, "POST", "/nodes/n1/heartbeat", a.Token, storage.NodeHeartbeat{Epoch: 1}) // not bound yet
	expect(200, "POST", "/nodes/register", a.Token, registration("n1", 1))               // binds
	expect(403, "POST", "/nodes/register", a.Token, registration("n3", 1))
	expect(403, "POST", "/nodes/register", b.Token, registration("n1", 2))
	expect(200, "POST", "/nodes/register", b.Token, registration("n2", 1))
	expect(200, "POST", "/nodes/n1/heartbeat", a.Token, storage.NodeHeartbeat{Epoch: 1})
	expect(403, "POST", "/nodes/n2/heartbeat", a.Token, storage.NodeHeartbeat{Epoch: 1})
	// Owner routes are closed to nodes.
	for _, r := range [][2]string{{"GET", "/tasks"}, {"GET", "/runs"}, {"GET", "/node-tokens"}, {"GET", "/nodes"}, {"POST", "/runs/x/cancel"}, {"DELETE", "/nodes/n1"}} {
		expect(403, r[0], r[1], a.Token, nil)
	}
	expect(401, "GET", "/tasks", storage.NodeTokenPrefix+"forged", nil)
	// Claims name the token's node; a v1 claim (no body) is refused; run routes check the run's node.
	if err := s.Create(ctx, storage.DefaultCompany, protocol.RunSpec{ID: "r1", Project: "p", Agent: "dev", Provider: protocol.RuntimeCodex, Workspace: "/tmp/node-token-p", Prompt: "x"}); err != nil {
		t.Fatal(err)
	}
	claim := func(node string) storage.ClaimRequest {
		return storage.ClaimRequest{NodeID: node, Epoch: 1, ClaimID: "1-0-1", Slot: 0}
	}
	expect(403, "POST", "/runs/r1/claim", a.Token, claim("n2"))
	expect(403, "POST", "/runs/r1/claim", a.Token, nil)
	code, out := call(t, c, "POST", "/runs/r1/claim", a.Token, claim("n1"))
	var claimed storage.Claimed
	if code != 200 || json.Unmarshal([]byte(out), &claimed) != nil {
		t.Fatal(code, out)
	}
	expect(403, "POST", "/runs/r1/start", b.Token, map[string]string{"lease": claimed.Lease})
	expect(200, "POST", "/runs/r1/start", a.Token, map[string]string{"lease": claimed.Lease})
	// The shared token still serves nodes (transition) and the owner.
	expect(200, "POST", "/nodes/register", "owner", registration("n4", 1))
	code, out = call(t, c, "GET", "/node-tokens", "owner", nil)
	if code != 200 || strings.Contains(out, a.Token) || !strings.Contains(out, `"node_id":"n1"`) {
		t.Fatal(code, out)
	}
	expect(200, "POST", "/node-tokens/"+a.ID+"/revoke", "owner", nil)
	expect(401, "POST", "/nodes/n1/heartbeat", a.Token, storage.NodeHeartbeat{Epoch: 1})
	expect(200, "POST", "/nodes/n2/heartbeat", b.Token, storage.NodeHeartbeat{Epoch: 1})
}

// TestNodeOfflineAfterContinuousTicking (node-agent 1.4): a silent node goes offline after 60 s of
// continuous Core ticking and its started run is lost then, not before; after a Core tick gap (the host
// slept) the 60 s count from the wake, and neither the node nor its v2 run is judged earlier (the 45 s v1
// lease window does not apply to v2 leases). The next heartbeat brings the node back.
func TestNodeOfflineAfterContinuousTicking(t *testing.T) {
	for _, gap := range []bool{false, true} {
		name := "continuous"
		if gap {
			name = "after a tick gap"
		}
		t.Run(name, func(t *testing.T) {
			c, s, fake := nodeCore(t)
			s.Continuity = clock.NewContinuity(3 * tickInterval)
			ctx := context.Background()
			tick := func(d time.Duration) {
				t.Helper()
				fake.Advance(d)
				if err := c.maintain(ctx); err != nil {
					t.Fatal(err)
				}
			}
			for range 70 { // a window older than 60 s
				tick(time.Second)
			}
			if _, err := s.RegisterNode(ctx, storage.DefaultCompany, registration("n1", 1)); err != nil {
				t.Fatal(err)
			}
			if err := s.Create(ctx, storage.DefaultCompany, protocol.RunSpec{ID: "r1", Project: "p", Agent: "dev", Provider: protocol.RuntimeCodex, Workspace: "/tmp/offline-p", Prompt: "x"}); err != nil {
				t.Fatal(err)
			}
			cl, err := s.ClaimV2(ctx, storage.DefaultCompany, "r1", storage.ClaimRequest{NodeID: "n1", Epoch: 1, ClaimID: "1-0-1"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.StartRun(ctx, storage.DefaultCompany, "r1", cl.Lease); err != nil {
				t.Fatal(err)
			}
			if _, err = s.NodeHeartbeat(ctx, storage.DefaultCompany, "n1", storage.NodeHeartbeat{Epoch: 1, Inventory: []storage.InventoryRun{{RunID: "r1", State: "running"}}}); err != nil {
				t.Fatal(err)
			}
			silentFrom := fake.Now()
			if gap {
				tick(2 * time.Hour) // the Core host slept: everything is far past due
				silentFrom = fake.Now()
			}
			online := func() bool {
				nodes, err := s.Nodes(ctx, storage.DefaultCompany)
				if err != nil {
					t.Fatal(err)
				}
				return nodes[0].Online
			}
			status := func() string {
				r, err := s.Get(ctx, storage.DefaultCompany, "r1")
				if err != nil {
					t.Fatal(err)
				}
				return r.Status
			}
			var offlineAt time.Duration
			for offlineAt == 0 && fake.Now().Sub(silentFrom) < 5*time.Minute {
				tick(time.Second)
				if !online() {
					offlineAt = fake.Now().Sub(silentFrom)
				} else if status() != "RUNNING" {
					t.Fatalf("run lost %s into the silence while its node is online", fake.Now().Sub(silentFrom))
				}
			}
			if offlineAt <= storage.NodeOffline || offlineAt > storage.NodeOffline+2*time.Second {
				t.Fatalf("offline after %s of continuous ticking, want just past %s", offlineAt, storage.NodeOffline)
			}
			if st := status(); st != "ENDED" {
				t.Fatal("offline node's run not lost", st)
			}
			if _, err = s.NodeHeartbeat(ctx, storage.DefaultCompany, "n1", storage.NodeHeartbeat{Epoch: 1}); err != nil || !online() {
				t.Fatal("heartbeat did not bring the node back", err)
			}
		})
	}
}

// TestPinnedNodeUnavailable (task-worktrees 4.2): a task whose unpushed work is on a node that stays offline
// past the pin timeout, or was removed, reports pinned_node_unavailable in why and gets exactly one escalation;
// reset_workspace unpins it, why turns runnable, and its next run claims on another node with a fresh session.
func TestPinnedNodeUnavailable(t *testing.T) {
	for _, removed := range []bool{false, true} {
		name := "offline past the timeout"
		if removed {
			name = "removed"
		}
		t.Run(name, func(t *testing.T) {
			c, s, fake := nodeCore(t)
			ctx := context.Background()
			co := storage.DefaultCompany
			if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "app", Name: "app", Workspace: "/tmp/pinned-app", RepoURL: "https://example.com/app.git"}); err != nil {
				t.Fatal(err)
			}
			for _, n := range []string{"n1", "n2"} {
				if _, err := s.RegisterNode(ctx, co, registration(n, 1)); err != nil {
					t.Fatal(err)
				}
			}
			v, err := s.CreateTask(ctx, co, tasks.Create{ID: "t", ProjectID: "app", Title: "t", Assignee: "dev", Ready: true, RequestID: "c"})
			if err != nil {
				t.Fatal(err)
			}
			snapshot := []byte(`{"spec":{"runtime":{"provider":"codex"},"workspace":{"mode":"worktree"}}}`)
			first, err := s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r1", Project: "app", Agent: "dev", Provider: protocol.RuntimeCodex, ConfigSnapshot: snapshot})
			if err != nil {
				t.Fatal(err)
			}
			cl, err := s.ClaimV2(ctx, co, first.ID, storage.ClaimRequest{NodeID: "n1", Epoch: 1, ClaimID: "1-0-1"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.StartRun(ctx, co, first.ID, cl.Lease); err != nil {
				t.Fatal(err)
			}
			if err = s.Apply(ctx, co, protocol.Wrap("agent.started", first.ID, "", protocol.Event{RunID: first.ID, Lease: cl.Lease, SessionID: "session-on-n1"})); err != nil {
				t.Fatal(err)
			}
			if _, err = s.ReportWorkspace(ctx, co, first.ID, cl.Lease, tasks.Workspace{Mode: "worktree", Branch: "af/t", NodeID: "n1", Unpushed: true}); err != nil {
				t.Fatal(err)
			}
			if err = s.Apply(ctx, co, protocol.Wrap("agent.failed", first.ID, "", protocol.Event{RunID: first.ID, Lease: cl.Lease, Status: "ENDED", Outcome: "failed", Result: "tests fail"})); err != nil {
				t.Fatal(err)
			}
			fake.Advance(time.Hour)                                                                   // past the retry backoff
			if _, err = s.NodeHeartbeat(ctx, co, "n1", storage.NodeHeartbeat{Epoch: 1}); err != nil { // sealed, listed nothing
				t.Fatal(err)
			}
			why := func() tasks.Verdict {
				t.Helper()
				code, out := call(t, c, "GET", "/tasks/t/why", "owner", nil)
				var v tasks.Verdict
				if code != 200 || json.Unmarshal([]byte(out), &v) != nil {
					t.Fatal(code, out)
				}
				return v
			}
			beatN2 := func() {
				t.Helper()
				if _, err := s.NodeHeartbeat(ctx, co, "n2", storage.NodeHeartbeat{Epoch: 1}); err != nil {
					t.Fatal(err)
				}
			}
			beatN2()
			if v := why(); v.Reason != tasks.ReasonRunnable {
				t.Fatalf("pinned node online: %+v", v)
			}
			// n1 goes silent; n2 keeps beating.
			fake.Advance(storage.NodeOffline + time.Second)
			beatN2()
			if err = s.ExpireNodes(ctx, co); err != nil {
				t.Fatal(err)
			}
			if v := why(); v.Reason != tasks.ReasonCapacity || !strings.Contains(v.Detail, "offline since") {
				t.Fatalf("pinned node briefly offline: %+v", v)
			}
			if removed {
				if code, out := call(t, c, "DELETE", "/nodes/n1", "owner", nil); code != 200 {
					t.Fatal(code, out)
				}
			} else {
				fake.Advance(storage.PinTimeout)
				beatN2()
			}
			if v := why(); v.Reason != tasks.ReasonPinnedNode || v.Clearer.Who != "owner" || !strings.Contains(v.Clearer.Action, "reset_workspace") {
				t.Fatalf("%+v", v)
			}
			for range 2 {
				fake.Advance(stallSweepEvery)
				if err = c.sweepStalls(ctx); err != nil {
					t.Fatal(err)
				}
			}
			open, err := s.Escalations(ctx, co, true)
			if err != nil || len(open) != 1 || open[0].Reason != "pinned_node_unavailable" || open[0].RefID != "n1" {
				t.Fatalf("%+v %v", open, err)
			}
			q, err := s.Message(ctx, co, open[0].MessageID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: "continue elsewhere", Answers: map[string]any{"answer": "reset_workspace"}, RequestID: "a"}); err != nil {
				t.Fatal(err)
			}
			if v := why(); v.Reason != tasks.ReasonRunnable {
				t.Fatalf("after reset_workspace: %+v", v)
			}
			got, _ := s.Task(ctx, co, "t")
			second, err := s.Dispatch(ctx, co, got.ID, got.Version, protocol.RunSpec{ID: "r2", Project: "app", Agent: "dev", Provider: protocol.RuntimeCodex, ConfigSnapshot: snapshot})
			if err != nil {
				t.Fatal(err)
			}
			if second.SessionID != "" {
				t.Fatal("the run after reset_workspace resumes the old node's session", second.SessionID)
			}
			cl2, err := s.ClaimV2(ctx, co, second.ID, storage.ClaimRequest{NodeID: "n2", Epoch: 1, ClaimID: "1-0-2"})
			if err != nil || cl2.RunSpec.SessionID != "" {
				t.Fatal("not claimable on another node with a fresh session", err, cl2.RunSpec.SessionID)
			}
			if got, _ = s.Task(ctx, co, "t"); got.FreshSession {
				t.Fatal("fresh_session kept after the dispatch")
			}
		})
	}
}

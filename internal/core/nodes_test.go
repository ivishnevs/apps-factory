package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/messaging"
	"appfactory/internal/nodesig"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
	"appfactory/internal/testnode"
)

func nodeCore(t *testing.T) (*Core, *storage.Store, *clock.Fake) {
	t.Helper()
	s, err := storage.Open(storagetest.URL(t))
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

// signedCall sends one request signed by n, at the store's time, to c's handler.
func signedCall(t *testing.T, c *Core, n testnode.Node, method, path string, body any) (int, string) {
	t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	n.Sign(t, c.Store, r, b)
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// TestNodeGateway (node-identity 2.2, 2.4): only a request signed by a node's active key, once, within the window and
// not older than this Core reaches a node route, for that node only and in the company of its key; owner routes are
// closed to nodes; bearer tokens never reach a node route and still reach owner routes.
func TestNodeGateway(t *testing.T) {
	c, s, fake := nodeCore(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	a, b := testnode.Enroll(t, s, co), testnode.Enroll(t, s, co)
	expect := func(want int, n testnode.Node, method, path string, body any) {
		t.Helper()
		code, out := signedCall(t, c, n, method, path, body)
		if code != want {
			t.Fatalf("%s %s: %d %s, want %d", method, path, code, out, want)
		}
		if want == http.StatusForbidden {
			var answer struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal([]byte(out), &answer); err != nil || answer.Reason != "node_forbidden" {
				t.Fatalf("%s %s: permanent node refusal lacks reason: %s", method, path, out)
			}
		}
	}
	serve := func(c *Core, r *http.Request) int {
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w.Code
	}
	beat, _ := json.Marshal(storage.NodeHeartbeat{Epoch: 1})
	beatPath := "/nodes/" + a.ID + "/heartbeat"
	// request is a heartbeat of a signed with key at time at, over the body signedBody.
	request := func(key ed25519.PrivateKey, signedBody []byte, at time.Time) *http.Request {
		t.Helper()
		r := httptest.NewRequest("POST", beatPath, bytes.NewReader(beat))
		if err := nodesig.Sign(r, a.ID, key, signedBody, at); err != nil {
			t.Fatal(err)
		}
		return r
	}

	// Register and heartbeat are for the node itself.
	expect(200, a, "POST", "/nodes/register", registration(a.ID, 1))
	expect(403, a, "POST", "/nodes/register", registration("n3", 1))
	expect(403, b, "POST", "/nodes/register", registration(a.ID, 2))
	expect(200, b, "POST", "/nodes/register", registration(b.ID, 1))
	expect(200, a, "POST", beatPath, storage.NodeHeartbeat{Epoch: 1})
	expect(403, b, "POST", beatPath, storage.NodeHeartbeat{Epoch: 1})

	// Owner routes are closed to nodes, also when the request carries a token too.
	for _, r := range [][2]string{{"GET", "/tasks"}, {"GET", "/runs"}, {"GET", "/nodes"}, {"GET", "/node-keys"}, {"POST", "/runs/x/cancel"}, {"DELETE", "/nodes/" + a.ID}, {"POST", "/nodes/" + a.ID + "/revoke"}} {
		expect(403, a, r[0], r[1], nil)
	}
	mixed := httptest.NewRequest("GET", "/tasks", nil)
	a.Sign(t, s, mixed, nil)
	mixed.Header.Set("Authorization", "Bearer owner")
	if code := serve(c, mixed); code != 403 {
		t.Fatal("a token opened an owner route to a signed request", code)
	}

	// Without a signature nothing reaches a node route.
	unsigned := httptest.NewRequest("POST", beatPath, bytes.NewReader(beat))
	if code := serve(c, unsigned); code != 401 {
		t.Fatal("unsigned", code)
	}
	garbage := httptest.NewRequest("POST", beatPath, bytes.NewReader(beat))
	garbage.Header.Set("Signature-Input", "sig1=();keyid=\""+a.ID+"\"")
	garbage.Header.Set("Authorization", "Bearer owner")
	if code := serve(c, garbage); code != 401 {
		t.Fatal("malformed signature with a token", code)
	}

	// Refused signatures, each with its reason in the node's feed (one event a minute per node).
	refusals := func() []string {
		t.Helper()
		page, err := s.Feed(ctx, co, storage.FeedFilter{}, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, item := range page.Items {
			if item.Type == "node.signature_refused" && item.EntityID == "node:"+a.ID {
				out = append(out, item.Summary)
			}
		}
		return out
	}
	used := request(a.Key, beat, fake.Now().Add(time.Minute))
	fake.Advance(time.Minute)
	if code := serve(c, used); code != 200 {
		t.Fatal("first use", code)
	}
	for _, tc := range []struct {
		name, reason, code string // code: the machine-readable reason in the answer (node-wake-recovery)
		r                  func() *http.Request
	}{
		{"bad signature", "bad signature", "signature_invalid", func() *http.Request { return request(b.Key, beat, fake.Now()) }},
		{"tampered body", "body digest", "signature_invalid", func() *http.Request {
			r := request(a.Key, beat, fake.Now())
			r.Body = io.NopCloser(strings.NewReader(`{"epoch":2}`))
			return r
		}},
		{"tampered path", "bad signature", "signature_invalid", func() *http.Request {
			r := request(a.Key, beat, fake.Now())
			r.URL.Path = "/nodes/" + a.ID + "/keys"
			return r
		}},
		{"stale", "window", "signature_window", func() *http.Request { return request(a.Key, beat, fake.Now().Add(-nodesig.Window-time.Second)) }},
		{"from the future", "window", "signature_window", func() *http.Request { return request(a.Key, beat, fake.Now().Add(nodesig.Window+time.Second)) }},
		{"replayed", "replay", "signature_replay", func() *http.Request {
			// Inside the window, later than the first use by less than a nonce's life.
			r := request(a.Key, beat, fake.Now())
			if code := serve(c, r.Clone(ctx)); code != 200 {
				t.Fatal("first use of the replayed request", code)
			}
			r.Body = io.NopCloser(bytes.NewReader(beat))
			return r
		}},
	} {
		fake.Advance(time.Minute + time.Second)
		before := len(refusals())
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, tc.r())
		if w.Code != 401 {
			t.Fatalf("%s: %d", tc.name, w.Code)
		}
		var answer struct {
			Reason     string `json:"reason"`
			CoreTimeMS int64  `json:"core_time_ms"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &answer)
		if answer.Reason != tc.code {
			t.Fatalf("%s: reason %q, want %q", tc.name, answer.Reason, tc.code)
		}
		if tc.code == "signature_window" && answer.CoreTimeMS != fake.Now().UnixMilli() {
			t.Fatalf("%s: core time %d, want %d", tc.name, answer.CoreTimeMS, fake.Now().UnixMilli())
		}
		if date := w.Header().Get("Date"); tc.code == "signature_window" && date != fake.Now().UTC().Format(http.TimeFormat) {
			t.Fatalf("%s: Date %q, want Core's time %s", tc.name, date, fake.Now().UTC().Format(http.TimeFormat))
		}
		got := refusals()
		if len(got) != before+1 || !strings.Contains(got[len(got)-1], tc.reason) {
			t.Fatalf("%s: feed %q, want a new event with %q", tc.name, got, tc.reason)
		}
	}
	before := len(refusals())
	if code := serve(c, request(b.Key, beat, fake.Now())); code != 401 || len(refusals()) != before {
		t.Fatal("refusals not limited to one a minute", code, refusals())
	}
	// Within the window on either side is fine, and the node keeps working.
	fake.Advance(time.Minute)
	if code := serve(c, request(a.Key, beat, fake.Now().Add(-nodesig.Window+time.Second))); code != 200 {
		t.Fatal("old but inside the window", code)
	}
	if code := serve(c, request(a.Key, beat, fake.Now().Add(nodesig.Window-time.Second))); code != 200 {
		t.Fatal("ahead but inside the window", code)
	}

	// Created before Core started: nonces do not survive a restart, so a request captured before it is refused,
	// whether it is the first request of the new Core or a later one.
	captured := request(a.Key, beat, fake.Now())
	fake.Advance(30 * time.Second)
	restarted := func() *Core { return &Core{Store: s, Token: "owner", Agents: c.Agents} }
	first := restarted()
	if code := serve(first, captured.Clone(ctx)); code != 401 {
		t.Fatal("captured request as the first after a restart", code)
	}
	if code := serve(first, request(a.Key, beat, fake.Now())); code != 200 {
		t.Fatal("fresh request after a restart", code)
	}
	second := restarted()
	if code := serve(second, request(a.Key, beat, fake.Now())); code != 200 {
		t.Fatal(code)
	}
	if code := serve(second, request(a.Key, beat, fake.Now().Add(-10*time.Second))); code != 401 {
		t.Fatal("created before this Core started", code)
	}
	page, err := s.Feed(ctx, co, storage.FeedFilter{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	started := false
	for _, item := range page.Items {
		started = started || item.Type == "node.signature_refused" && strings.Contains(item.Summary, "created before Core started")
	}
	if !started {
		t.Fatal("no feed event for created before Core started")
	}

	// Pending, revoked and unknown keys are not credentials, even with a good signature.
	code, err := s.CreateNodeEnrollment(ctx, co, "pending")
	if err != nil {
		t.Fatal(err)
	}
	p := newEnrollingNode(t)
	if _, err = s.EnrollNode(ctx, p.request(code.Code)); err != nil {
		t.Fatal(err)
	}
	pending := testnode.Node{ID: p.id, Key: p.key}
	expect(401, pending, "POST", "/nodes/register", registration(p.id, 1))
	expect(401, pending, "POST", "/nodes/"+p.id+"/heartbeat", storage.NodeHeartbeat{Epoch: 1})
	expect(401, pending, "GET", "/health", nil)
	u := newEnrollingNode(t)
	expect(401, testnode.Node{ID: u.id, Key: u.key}, "POST", "/nodes/register", registration(u.id, 1))
	revoked := testnode.Enroll(t, s, co)
	expect(200, revoked, "POST", "/nodes/register", registration(revoked.ID, 1))
	if err = s.RevokeNode(ctx, co, revoked.ID); err != nil {
		t.Fatal(err)
	}
	expect(401, revoked, "POST", "/nodes/"+revoked.ID+"/heartbeat", storage.NodeHeartbeat{Epoch: 1})
	expect(401, revoked, "POST", "/nodes/register", registration(revoked.ID, 1))

	// Claims name the signer; run routes check the run's node.
	if err = s.Create(ctx, co, protocol.RunSpec{ID: "r1", Project: "p", Agent: "dev", Provider: protocol.RuntimeCodex, Workspace: "/tmp/node-gateway-p", Prompt: "x"}); err != nil {
		t.Fatal(err)
	}
	claim := func(node string) storage.ClaimRequest {
		return storage.ClaimRequest{NodeID: node, Epoch: 1, ClaimID: "1-0-1", Slot: 0}
	}
	runRoutes := func(lease string) [][3]any {
		l := map[string]string{"lease": lease}
		return [][3]any{{"POST", "/runs/r1/start", l}, {"POST", "/runs/r1/credential", l}, {"POST", "/runs/r1/workspace", l}, {"POST", "/runs/r1/project-setup", l},
			{"GET", "/runs/r1/blobs/" + strings.Repeat("0", 64), nil}, {"PUT", "/runs/r1/logs", nil}}
	}
	for _, r := range runRoutes("") { // an unclaimed run is nobody's
		expect(403, a, r[0].(string), r[1].(string), r[2])
	}
	expect(403, a, "POST", "/runs/r1/claim", claim(b.ID))
	expect(403, a, "POST", "/runs/r1/claim", nil)
	status, out := signedCall(t, c, a, "POST", "/runs/r1/claim", claim(a.ID))
	var claimed storage.Claimed
	if status != 200 || json.Unmarshal([]byte(out), &claimed) != nil || claimed.RunSpec.ID != "r1" {
		t.Fatal(status, out)
	}
	for _, r := range runRoutes(claimed.Lease) {
		expect(403, b, r[0].(string), r[1].(string), r[2])
	}

	// Bearer tokens, the operator's and an owner's, get 401 on every node route and do nothing there; both still reach
	// owner routes.
	issued, err := s.CreateOwnerToken(ctx, co, "first owner", "operator")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{c.Token, issued.Token} {
		for _, r := range append(runRoutes(claimed.Lease), [3]any{"POST", "/runs/r1/claim", claim(a.ID)}, [3]any{"POST", "/nodes/register", registration(a.ID, 1)},
			[3]any{"POST", beatPath, storage.NodeHeartbeat{Epoch: 1}}, [3]any{"POST", "/nodes/" + a.ID + "/keys", map[string]string{}}) {
			if status, out := call(t, c, r[0].(string), r[1].(string), token, r[2]); status != 401 {
				t.Fatalf("%s %s with a token: %d %s", r[0], r[1], status, out)
			}
		}
		for _, path := range []string{"/tasks", "/nodes", "/runs/r1"} {
			if status, out := call(t, c, "GET", path, token, nil); status != 200 {
				t.Fatalf("GET %s with a token: %d %s", path, status, out)
			}
		}
	}
	// What used to be a node token is not a credential anywhere.
	for _, r := range [][2]string{{"POST", "/runs/r1/claim"}, {"POST", "/runs/r1/credential"}, {"POST", "/runs/r1/start"}, {"GET", "/tasks"}} {
		if status, out := call(t, c, r[0], r[1], "fnt_old-node-token", nil); status != 401 {
			t.Fatalf("%s %s with an old node token: %d %s", r[0], r[1], status, out)
		}
	}
	if run, err := s.Get(ctx, co, "r1"); err != nil || run.Status != "CLAIMED" {
		t.Fatalf("a token changed the run: %+v %v", run, err)
	}
	expect(200, a, "POST", "/runs/r1/start", map[string]string{"lease": claimed.Lease})
	expect(200, a, "POST", "/runs/r1/credential", map[string]string{"lease": claimed.Lease})

	// A node acts in the company of its key.
	if err = s.CreateCompany(ctx, "globex", "Globex"); err != nil {
		t.Fatal(err)
	}
	g := testnode.Enroll(t, s, "globex")
	expect(200, g, "POST", "/nodes/register", registration(g.ID, 1))
	expect(404, g, "POST", "/runs/r1/start", map[string]string{"lease": claimed.Lease})
	nodes, err := s.Nodes(ctx, "globex")
	if err != nil || len(nodes) != 1 || nodes[0].NodeID != g.ID {
		t.Fatalf("%+v %v", nodes, err)
	}
}

// TestHealthGateway: /health answers a signed node and a token, nobody else.
func TestHealthGateway(t *testing.T) {
	c, s, _ := nodeCore(t)
	ctx := context.Background()
	n := testnode.Enroll(t, s, storage.DefaultCompany)
	if code, out := signedCall(t, c, n, "GET", "/health", nil); code != 200 {
		t.Fatal("signed", code, out)
	}
	issued, err := s.CreateOwnerToken(ctx, storage.DefaultCompany, "first owner", "operator")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{c.Token, issued.Token} {
		if code, out := call(t, c, "GET", "/health", token, nil); code != 200 {
			t.Fatal("token", code, out)
		}
	}
	if code, _ := call(t, c, "GET", "/health", "nope", nil); code != 401 {
		t.Fatal("bad token", code)
	}
}

// TestNodeOfflineAfterContinuousTicking (node-agent 1.4): a silent node goes offline after 60 s of
// continuous Core ticking and its started run is lost then, not before; after a Core tick gap (the host
// slept) the 60 s count from the wake, and neither the node nor its run is judged earlier (LeaseTTL's 45 s
// window does not apply to expiry). The next heartbeat brings the node back.
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

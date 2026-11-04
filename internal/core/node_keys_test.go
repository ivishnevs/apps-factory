package core

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/node"
	"appfactory/internal/nodesig"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/testnode"
	"appfactory/internal/worker"
)

type enrollingNode struct {
	id, signPub, boxPub string
	key                 ed25519.PrivateKey
}

func newEnrollingNode(t *testing.T) enrollingNode {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		t.Fatal(err)
	}
	box := make([]byte, 32)
	if _, err = rand.Read(box); err != nil {
		t.Fatal(err)
	}
	return enrollingNode{id: hex.EncodeToString(id), signPub: hex.EncodeToString(pub), boxPub: hex.EncodeToString(box), key: key}
}

func (n enrollingNode) request(code string) storage.EnrollRequest {
	proof := ed25519.Sign(n.key, nodesig.EnrollMessage(code, n.id, n.signPub, n.boxPub))
	return storage.EnrollRequest{Code: code, NodeID: n.id, SignPub: n.signPub, BoxPub: n.boxPub, Proof: hex.EncodeToString(proof)}
}

func enrollCall(t *testing.T, c *Core, body any) (int, string) {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/nodes/enroll", strings.NewReader(string(b)))
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

func (n enrollingNode) status(t *testing.T, c *Core, key ed25519.PrivateKey) (int, string) {
	t.Helper()
	r := httptest.NewRequest("GET", "/nodes/"+n.id+"/enrollment", nil)
	if key != nil {
		if err := nodesig.Sign(r, n.id, key, nil, c.Store.Now()); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// TestNodeEnrollment (node-identity 1.2): a code lets a node register keys that stay pending until the owner
// confirms their fingerprint; the proof, the code's single use and the fingerprint are all enforced.
func TestNodeEnrollment(t *testing.T) {
	c, _, _ := nodeCore(t)
	issue := func() storage.IssuedNodeEnrollment {
		t.Helper()
		code, out := call(t, c, "POST", "/node-enrollments", "owner", map[string]string{"name": "mac-mini-1"})
		var v storage.IssuedNodeEnrollment
		if code != 200 || json.Unmarshal([]byte(out), &v) != nil || !strings.HasPrefix(v.Code, storage.EnrollmentCodePrefix) {
			t.Fatal(code, out)
		}
		return v
	}
	if code, _ := call(t, c, "POST", "/node-enrollments", "owner", map[string]string{"name": " "}); code != 400 {
		t.Fatal("unnamed code issued", code)
	}
	if code, _ := call(t, c, "POST", "/node-enrollments", "", map[string]string{"name": "x"}); code != 401 {
		t.Fatal("code issued without a token", code)
	}

	n := newEnrollingNode(t)
	v := issue()

	// A wrong proof, a malformed id and a false code leave the real code usable.
	bad := n.request(v.Code)
	bad.Proof = hex.EncodeToString(ed25519.Sign(newEnrollingNode(t).key, []byte("x")))
	if code, _ := enrollCall(t, c, bad); code != 400 {
		t.Fatal("proof by another key accepted", code)
	}
	short := n
	short.id = "abc.def"
	if code, _ := enrollCall(t, c, short.request(v.Code)); code != 400 {
		t.Fatal("node id of another form accepted", code)
	}
	if code, _ := enrollCall(t, c, n.request(storage.EnrollmentCodePrefix+"00")); code != 403 {
		t.Fatal("false code accepted", code)
	}

	code, out := enrollCall(t, c, n.request(v.Code))
	var a NodeEnrollmentAnswer
	if code != 200 || json.Unmarshal([]byte(out), &a) != nil || a.Status != storage.NodeKeyPending || a.Company == "" || a.Name != "mac-mini-1" {
		t.Fatal(code, out)
	}
	sign, _ := hex.DecodeString(n.signPub)
	box, _ := hex.DecodeString(n.boxPub)
	if a.Fingerprint != nodesig.Fingerprint(sign, box) {
		t.Fatal(a)
	}

	// The stolen-code scenario: whoever comes second is refused.
	if code, _ := enrollCall(t, c, newEnrollingNode(t).request(v.Code)); code != 403 {
		t.Fatal("reused code accepted", code)
	}

	// Status is for the key holder only.
	if code, _ := n.status(t, c, nil); code != 401 {
		t.Fatal("unsigned status", code)
	}
	if code, _ := n.status(t, c, newEnrollingNode(t).key); code != 401 {
		t.Fatal("status signed by another key", code)
	}
	if code, out := n.status(t, c, n.key); code != 200 || !strings.Contains(out, `"pending"`) {
		t.Fatal(code, out)
	}

	// A pending node reaches nothing else: no token gets it through, and its keys are not a credential yet.
	if code, _ := call(t, c, "POST", "/nodes/register", "", map[string]string{"node_id": n.id}); code != 401 {
		t.Fatal("pending node registered", code)
	}

	// Approval needs the right fingerprint.
	if code, _ := call(t, c, "POST", "/node-keys/"+n.id+"/approve", "owner", map[string]string{"fingerprint": "0000"}); code != 409 {
		t.Fatal("wrong fingerprint accepted", code)
	}
	if code, out := n.status(t, c, n.key); code != 200 || !strings.Contains(out, `"pending"`) {
		t.Fatal("wrong fingerprint changed the keys", code, out)
	}
	if code, out := call(t, c, "POST", "/node-keys/"+n.id+"/approve", "owner", map[string]string{"fingerprint": a.Fingerprint}); code != 200 {
		t.Fatal(code, out)
	}
	if code, out := n.status(t, c, n.key); code != 200 || !strings.Contains(out, `"active"`) {
		t.Fatal(code, out)
	}
	if code, out := call(t, c, "GET", "/node-keys", "owner", nil); code != 200 || !strings.Contains(out, n.id) || strings.Contains(out, "code") {
		t.Fatal(code, out)
	}
	if code, _ := call(t, c, "POST", "/node-keys/"+n.id+"/reject", "owner", nil); code != 404 {
		t.Fatal("active keys rejected", code)
	}

	// The same id cannot enroll again while it has keys.
	again := newEnrollingNode(t)
	again.id = n.id
	if code, _ := enrollCall(t, c, again.request(issue().Code)); code != 409 {
		t.Fatal("taken node id accepted", code)
	}

	// Rejecting pending keys ends them.
	x := newEnrollingNode(t)
	if code, out := enrollCall(t, c, x.request(issue().Code)); code != 200 {
		t.Fatal(code, out)
	}
	if code, out := call(t, c, "POST", "/node-keys/"+x.id+"/reject", "owner", nil); code != 200 {
		t.Fatal(code, out)
	}
	if code, _ := x.status(t, c, x.key); code != 401 {
		t.Fatal("rejected keys still known", code)
	}
}

func TestEnrollLimiter(t *testing.T) {
	var l enrollLimiter
	now := time.Unix(1_800_000_000, 0)
	for i := 0; i < enrollPerAddressPerMinute; i++ {
		if !l.allow("a", now) {
			t.Fatal(i)
		}
	}
	if l.allow("a", now) {
		t.Fatal("address limit not applied")
	}
	if !l.allow("b", now) {
		t.Fatal("another address limited")
	}
	for i := 0; i < enrollPerMinute; i++ {
		l.allow(string(rune('c'+i%2000)), now)
	}
	if l.allow("new", now) {
		t.Fatal("overall limit not applied")
	}
	if !l.allow("a", now.Add(time.Minute)) {
		t.Fatal("limit not reset")
	}
}

func TestClientAddress(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	if a := clientAddress(r); a != "10.0.0.1" {
		t.Fatal(a)
	}
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.2")
	if a := clientAddress(r); a != "1.2.3.4" {
		t.Fatal(a)
	}
}

// TestEnrollCommandAgainstCore (node-identity 1.3): the node's enrollment works against the real routes.
func TestEnrollCommandAgainstCore(t *testing.T) {
	c, s, _ := nodeCore(t)
	srv := httptest.NewServer(c.Handler())
	defer srv.Close()
	code, out := call(t, c, "POST", "/node-enrollments", "owner", map[string]string{"name": "mini"})
	var v storage.IssuedNodeEnrollment
	if code != 200 || json.Unmarshal([]byte(out), &v) != nil {
		t.Fatal(code, out)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := node.Enrollment{URL: srv.URL, Code: v.Code, StateDir: dir, Poll: 5 * time.Millisecond, Now: s.Now}.Run(ctx)
		done <- err
	}()
	var pending storage.NodeKey
	for pending.NodeID == "" {
		select {
		case err := <-done:
			t.Fatal("ended before approval:", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
		keys, err := s.NodeKeys(ctx, storage.DefaultCompany)
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) > 0 {
			pending = keys[0]
		}
	}
	local, err := node.LoadKeys(dir)
	if err != nil || local.Fingerprint() != pending.Fingerprint {
		t.Fatal(pending, err)
	}
	if _, err = s.ApproveNodeKey(ctx, storage.DefaultCompany, pending.NodeID, pending.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if company, err := node.LoadCompany(dir); err != nil || company != storage.DefaultCompany {
		t.Fatal(company, err)
	}
}

// TestRevokeNode (node-identity 4.1, 4.4): revoking ends a node at once, online or not: its keys go, its next signed
// request is refused, its live run ends and its credentials with it, a task pinned by its unpushed work is released
// as by RemoveNode, the feed says so, and another company cannot do it.
func TestRevokeNode(t *testing.T) {
	c, s, fake := nodeCore(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	n := testnode.Enroll(t, s, co)
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "app", Name: "app", Workspace: "/tmp/revoke-app", RepoURL: "https://example.com/app.git"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{n.ID, "n2"} {
		if _, err := s.RegisterNode(ctx, co, registration(id, 1)); err != nil {
			t.Fatal(err)
		}
	}
	claim := func(run, id string) string {
		t.Helper()
		code, out := signedCall(t, c, n, "POST", "/runs/"+run+"/claim", storage.ClaimRequest{NodeID: n.ID, Epoch: 1, ClaimID: id})
		var cl storage.Claimed
		if code != 200 || json.Unmarshal([]byte(out), &cl) != nil {
			t.Fatal(code, out)
		}
		if code, out = signedCall(t, c, n, "POST", "/runs/"+run+"/start", map[string]string{"lease": cl.Lease}); code != 200 {
			t.Fatal(code, out)
		}
		return cl.Lease
	}

	// A task pinned to the node by unpushed work, its run over.
	v, err := s.CreateTask(ctx, co, tasks.Create{ID: "t", ProjectID: "app", Title: "t", Assignee: "dev", Ready: true, RequestID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := []byte(`{"spec":{"runtime":{"provider":"codex"},"workspace":{"mode":"worktree"}}}`)
	first, err := s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r1", Project: "app", Agent: "dev", Provider: protocol.RuntimeCodex, ConfigSnapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	lease := claim(first.ID, "1-0-1")
	if _, err = s.ReportWorkspace(ctx, co, first.ID, lease, tasks.Workspace{Mode: "worktree", Branch: "af/t", NodeID: n.ID, Unpushed: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.Apply(ctx, co, protocol.Wrap("agent.failed", first.ID, "", protocol.Event{RunID: first.ID, Lease: lease, Status: "ENDED", Outcome: "failed", Result: "tests fail"})); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Hour) // past the retry backoff
	for _, id := range []string{n.ID, "n2"} {
		if _, err = s.NodeHeartbeat(ctx, co, id, storage.NodeHeartbeat{Epoch: 1}); err != nil {
			t.Fatal(err)
		}
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
	if v := why(); v.Reason != tasks.ReasonRunnable {
		t.Fatalf("pinned to a live node: %+v", v)
	}

	// A live run with a credential.
	if err = s.Create(ctx, co, protocol.RunSpec{ID: "live", Project: "p", Agent: "dev", Provider: protocol.RuntimeCodex, Workspace: "/tmp/revoke-p", Prompt: "x"}); err != nil {
		t.Fatal(err)
	}
	liveLease := claim("live", "1-0-2")
	credential := map[string]string{"lease": liveLease}
	code, out := signedCall(t, c, n, "POST", "/runs/live/credential", credential)
	var cred struct{ Token string }
	if code != 200 || json.Unmarshal([]byte(out), &cred) != nil || cred.Token == "" {
		t.Fatal(code, out)
	}
	if code, out = call(t, c, "GET", "/agent/tools", cred.Token, nil); code != 200 {
		t.Fatal("run credential before revocation", code, out)
	}

	// Another company cannot, and nothing changes.
	if err = s.CreateCompany(ctx, "globex", "Globex"); err != nil {
		t.Fatal(err)
	}
	globex, err := s.CreateOwnerToken(ctx, "globex", "first owner", "operator")
	if err != nil {
		t.Fatal(err)
	}
	if code, out = call(t, c, "POST", "/nodes/"+n.ID+"/revoke", globex.Token, nil); code != 404 {
		t.Fatal("revoked by another company", code, out)
	}
	if err = s.RevokeNode(ctx, "globex", n.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal(err)
	}
	if code, out = signedCall(t, c, n, "POST", "/nodes/"+n.ID+"/heartbeat", storage.NodeHeartbeat{Epoch: 1, Inventory: []storage.InventoryRun{{RunID: "live", State: "running"}}}); code != 200 {
		t.Fatal("another company's attempt cut the node off", code, out)
	}

	// The owner revokes the online node.
	if code, out = call(t, c, "POST", "/nodes/"+n.ID+"/revoke", "owner", nil); code != 200 {
		t.Fatal("online node refused", code, out)
	}
	if _, err = s.NodeKeyOf(ctx, n.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("keys kept", err)
	}
	if code, _ = signedCall(t, c, n, "POST", "/nodes/"+n.ID+"/heartbeat", storage.NodeHeartbeat{Epoch: 1}); code != 401 {
		t.Fatal("signed heartbeat after revocation", code)
	}
	if code, _ = signedCall(t, c, n, "POST", "/runs/live/credential", credential); code != 401 {
		t.Fatal("signed credential after revocation", code)
	}
	if run, err := s.Get(ctx, co, "live"); err != nil || run.Status != "ENDED" || !strings.Contains(run.Result, "revoked") {
		t.Fatalf("live run: %+v %v", run, err)
	}
	// The route itself refuses the lease too, whoever would reach it, and the run's credential is dead.
	r := httptest.NewRequest("POST", "/runs/live/credential", strings.NewReader(`{"lease":"`+liveLease+`"}`))
	r.SetPathValue("id", "live")
	w := httptest.NewRecorder()
	c.credential(w, r)
	if w.Code != 403 {
		t.Fatal("credential route after revocation", w.Code, w.Body.String())
	}
	if code, out = call(t, c, "GET", "/agent/tools", cred.Token, nil); code == 200 {
		t.Fatal("run credential works after revocation", out)
	}
	// The pinned task is released as by RemoveNode.
	if v := why(); v.Reason != tasks.ReasonPinnedNode || !strings.Contains(v.Clearer.Action, "reset_workspace") {
		t.Fatalf("%+v", v)
	}
	if nodes, err := s.Nodes(ctx, co); err != nil || len(nodes) != 1 || nodes[0].NodeID != "n2" {
		t.Fatalf("%+v %v", nodes, err)
	}
	page, err := s.Feed(ctx, co, storage.FeedFilter{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	revoked := 0
	for _, item := range page.Items {
		if item.Type == "node.revoked" && item.EntityID == "node:"+n.ID {
			revoked++
		}
	}
	if revoked != 1 {
		t.Fatalf("%d node.revoked events", revoked)
	}
	if code, _ = call(t, c, "POST", "/nodes/"+n.ID+"/revoke", "owner", nil); code != 404 {
		t.Fatal("revoked twice", code)
	}

	// Keys of a node that never registered are revoked too; an owner token of the company can do it.
	idle := testnode.Enroll(t, s, co)
	owner, err := s.CreateOwnerToken(ctx, co, "first owner", "operator")
	if err != nil {
		t.Fatal(err)
	}
	if code, out = call(t, c, "POST", "/nodes/"+idle.ID+"/revoke", owner.Token, nil); code != 200 {
		t.Fatal(code, out)
	}
	if _, err = s.NodeKeyOf(ctx, idle.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("keys kept", err)
	}
	if code, _ = signedCall(t, c, idle, "POST", "/nodes/register", registration(idle.ID, 1)); code != 401 {
		t.Fatal(code)
	}
}

// rotatingNode is an approved node with its state on disk, as `factory-node enroll` leaves it.
type rotatingNode struct {
	id, dir string
	keys    node.Keys
}

func enrollState(t *testing.T, s *storage.Store, company string) rotatingNode {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	keys, err := node.GenerateKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := node.NodeID(dir)
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.CreateNodeEnrollment(ctx, company, "mini")
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
	return rotatingNode{id: id, dir: dir, keys: keys}
}

func (n rotatingNode) signer() testnode.Node { return testnode.Node{ID: n.id, Key: n.keys.Sign} }

// rotate runs a rotation until Core knows its proposal and returns the proposal's fingerprint and the rotation's end.
func (n rotatingNode) rotate(ctx context.Context, t *testing.T, s *storage.Store, url string) (string, <-chan rotationEnd) {
	t.Helper()
	done := make(chan rotationEnd, 1)
	go func() {
		st, err := node.Rotation{URL: url, StateDir: n.dir, Poll: 5 * time.Millisecond, Out: io.Discard, Now: s.Now}.Run(ctx)
		done <- rotationEnd{st, err}
	}()
	for {
		k, err := s.NodeKeyOf(ctx, n.id)
		if err != nil {
			t.Fatal(err)
		}
		if k.NextFingerprint != "" {
			return k.NextFingerprint, done
		}
		select {
		case end := <-done:
			t.Fatal("rotation ended before Core knew it:", end.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(2 * time.Millisecond):
		}
	}
}

type rotationEnd struct {
	status node.EnrollmentStatus
	err    error
}

func feedKinds(t *testing.T, s *storage.Store, company string) map[string]int {
	t.Helper()
	page, err := s.Feed(context.Background(), company, storage.FeedFilter{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, item := range page.Items {
		out[item.Type]++
	}
	return out
}

// TestKeyRotation (node-identity 4.1): the sequence of design D5 between node.Rotation and the real routes.
func TestKeyRotation(t *testing.T) {
	setup := func(t *testing.T) (*Core, *storage.Store, *clock.Fake, rotatingNode, string, context.Context) {
		t.Helper()
		c, s, fake := nodeCore(t)
		srv := httptest.NewServer(c.Handler())
		t.Cleanup(srv.Close)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		t.Cleanup(cancel)
		n := enrollState(t, s, storage.DefaultCompany)
		if _, err := s.RegisterNode(ctx, storage.DefaultCompany, registration(n.id, 1)); err != nil {
			t.Fatal(err)
		}
		return c, s, fake, n, srv.URL, ctx
	}
	beat := storage.NodeHeartbeat{Epoch: 1}
	beatPath := func(n rotatingNode) string { return "/nodes/" + n.id + "/heartbeat" }
	nextDir := func(n rotatingNode) string { return filepath.Join(n.dir, node.KeysDir, node.RotatingDir) }

	t.Run("the current key is required", func(t *testing.T) {
		c, s, _, n, _, ctx := setup(t)
		next, err := node.GenerateKeys(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		proposal := map[string]string{"sign_pub": hex.EncodeToString(next.SignPublic()), "box_pub": hex.EncodeToString(next.BoxPublic())}
		path := "/nodes/" + n.id + "/keys"
		for _, token := range []string{"", "owner"} {
			if code, _ := call(t, c, "POST", path, token, proposal); code != 401 {
				t.Fatalf("token %q: %d", token, code)
			}
		}
		if code, _ := signedCall(t, c, testnode.Node{ID: n.id, Key: next.Sign}, "POST", path, proposal); code != 401 {
			t.Fatal("signed by the new key itself", code)
		}
		other := testnode.Enroll(t, s, storage.DefaultCompany)
		if code, _ := signedCall(t, c, other, "POST", path, proposal); code != 403 {
			t.Fatal("signed by another node", code)
		}
		if k, err := s.NodeKeyOf(ctx, n.id); err != nil || k.NextFingerprint != "" {
			t.Fatalf("%+v %v", k, err)
		}
		// A key already in use cannot be proposed.
		taken := map[string]string{"sign_pub": hex.EncodeToString(n.keys.SignPublic()), "box_pub": hex.EncodeToString(n.keys.BoxPublic())}
		if code, _ := signedCall(t, c, n.signer(), "POST", path, taken); code != 409 {
			t.Fatal("the current key proposed", code)
		}
		if code, _ := signedCall(t, c, n.signer(), "POST", path, proposal); code != 200 {
			t.Fatal("signed by the current key", code)
		}
	})

	t.Run("approved", func(t *testing.T) {
		c, s, _, n, url, ctx := setup(t)
		co := storage.DefaultCompany
		reload := node.KeyReloader(n.dir, n.keys.Sign)
		api := worker.API{URL: url, Node: n.id, CurrentKey: reload, Now: s.Now}
		// A live run from before the rotation.
		if err := s.Create(ctx, co, protocol.RunSpec{ID: "live", Project: "p", Agent: "dev", Provider: protocol.RuntimeCodex, Workspace: "/tmp/rotate-p", Prompt: "x"}); err != nil {
			t.Fatal(err)
		}
		var claimed storage.Claimed
		if err := api.Do(ctx, "POST", "/runs/live/claim", storage.ClaimRequest{NodeID: n.id, Epoch: 1, ClaimID: "1-0-1"}, &claimed); err != nil {
			t.Fatal(err)
		}
		if err := api.Do(ctx, "POST", "/runs/live/start", map[string]string{"lease": claimed.Lease}, nil); err != nil {
			t.Fatal(err)
		}

		fp, done := n.rotate(ctx, t, s, url)
		next, err := node.LoadKeys(nextDir(n))
		if err != nil || next.Fingerprint() != fp {
			t.Fatal(fp, err)
		}
		// Until approval the old keys are the only ones that work.
		if code, out := signedCall(t, c, n.signer(), "POST", beatPath(n), beat); code != 200 {
			t.Fatal("old key while waiting", code, out)
		}
		newSigner := testnode.Node{ID: n.id, Key: next.Sign}
		if code, _ := signedCall(t, c, newSigner, "POST", beatPath(n), beat); code != 401 {
			t.Fatal("new key before approval", code)
		}
		// The wrong fingerprint changes nothing; the current one is not the proposal's.
		for _, wrong := range []string{"0000", n.keys.Fingerprint()} {
			if code, _ := call(t, c, "POST", "/node-keys/"+n.id+"/approve", "owner", map[string]string{"fingerprint": wrong}); code != 409 {
				t.Fatal(wrong, code)
			}
		}
		if code, _ := signedCall(t, c, n.signer(), "POST", beatPath(n), beat); code != 200 {
			t.Fatal("old key after a wrong fingerprint", code)
		}
		if err := api.Do(ctx, "POST", beatPath(n), beat, nil); err != nil {
			t.Fatal("running node while waiting:", err)
		}

		if code, out := call(t, c, "POST", "/node-keys/"+n.id+"/approve", "owner", map[string]string{"fingerprint": fp}); code != 200 {
			t.Fatal(code, out)
		}
		end := <-done
		if end.err != nil || end.status.Fingerprint != fp || end.status.Status != "active" {
			t.Fatalf("%+v", end)
		}
		// Only the new keys work, and they are in place on disk.
		if code, _ := signedCall(t, c, n.signer(), "POST", beatPath(n), beat); code != 401 {
			t.Fatal("old key after approval", code)
		}
		if code, out := signedCall(t, c, newSigner, "POST", beatPath(n), beat); code != 200 {
			t.Fatal("new key after approval", code, out)
		}
		moved, err := node.LoadKeys(n.dir)
		if err != nil || moved.Fingerprint() != fp {
			t.Fatal(moved.Fingerprint(), fp, err)
		}
		if _, err = os.Stat(nextDir(n)); !os.IsNotExist(err) {
			t.Fatal("next/ kept", err)
		}
		// The running node continues without a restart, and its live run is not affected.
		if !bytes.Equal(reload(), next.Sign) {
			t.Fatal("KeyReloader kept the old key")
		}
		if err = api.Do(ctx, "POST", beatPath(n), beat, nil); err != nil {
			t.Fatal("running node after approval:", err)
		}
		if err = api.Do(ctx, "POST", "/runs/live/credential", map[string]string{"lease": claimed.Lease}, nil); err != nil {
			t.Fatal("live run after the rotation:", err)
		}
		if kinds := feedKinds(t, s, co); kinds["node.keys_rotation_requested"] != 1 || kinds["node.keys_rotated"] != 1 {
			t.Fatalf("%+v", kinds)
		}
	})

	t.Run("rejected", func(t *testing.T) {
		c, s, _, n, url, ctx := setup(t)
		fp, done := n.rotate(ctx, t, s, url)
		if code, out := call(t, c, "POST", "/node-keys/"+n.id+"/reject", "owner", nil); code != 200 {
			t.Fatal(code, out)
		}
		if end := <-done; end.err == nil || !strings.Contains(end.err.Error(), "rejected") {
			t.Fatalf("%+v", end)
		}
		kept, err := node.LoadKeys(n.dir)
		if err != nil || kept.Fingerprint() != n.keys.Fingerprint() || kept.Fingerprint() == fp {
			t.Fatal(kept.Fingerprint(), err)
		}
		if _, err = os.Stat(nextDir(n)); !os.IsNotExist(err) {
			t.Fatal("next/ kept", err)
		}
		if code, out := signedCall(t, c, n.signer(), "POST", beatPath(n), beat); code != 200 {
			t.Fatal("current key after rejection", code, out)
		}
		if k, err := s.NodeKeyOf(ctx, n.id); err != nil || k.NextFingerprint != "" || k.Status != storage.NodeKeyActive || k.Fingerprint != n.keys.Fingerprint() {
			t.Fatalf("%+v %v", k, err)
		}
		// Nothing left to approve, and a new rotation can start.
		if code, _ := call(t, c, "POST", "/node-keys/"+n.id+"/approve", "owner", map[string]string{"fingerprint": fp}); code != 404 {
			t.Fatal("rejected keys approved", code)
		}
		_, again := n.rotate(ctx, t, s, url)
		if code, out := call(t, c, "POST", "/node-keys/"+n.id+"/reject", "owner", nil); code != 200 {
			t.Fatal(code, out)
		}
		if end := <-again; end.err == nil {
			t.Fatalf("%+v", end)
		}
	})

	t.Run("waited too long", func(t *testing.T) {
		c, s, fake, n, url, ctx := setup(t)
		fp, done := n.rotate(ctx, t, s, url)
		fake.Advance(storage.PendingNodeKeyTTL + time.Second)
		if end := <-done; end.err == nil || !strings.Contains(end.err.Error(), "waited too long") {
			t.Fatalf("%+v", end)
		}
		if _, err := os.Stat(nextDir(n)); !os.IsNotExist(err) {
			t.Fatal("next/ kept", err)
		}
		if code, _ := call(t, c, "POST", "/node-keys/"+n.id+"/approve", "owner", map[string]string{"fingerprint": fp}); code != 404 {
			t.Fatal("expired keys approved", code)
		}
		if code, out := signedCall(t, c, n.signer(), "POST", beatPath(n), beat); code != 200 {
			t.Fatal("current key after expiry", code, out)
		}
	})

	t.Run("running it again continues", func(t *testing.T) {
		c, s, _, n, url, ctx := setup(t)
		first, cancel := context.WithCancel(ctx)
		fp, done := n.rotate(first, t, s, url)
		cancel()
		if end := <-done; !errors.Is(end.err, context.Canceled) {
			t.Fatalf("%+v", end)
		}
		again := make(chan rotationEnd, 1)
		go func() {
			st, err := node.Rotation{URL: url, StateDir: n.dir, Poll: 5 * time.Millisecond, Out: io.Discard, Now: s.Now}.Run(ctx)
			again <- rotationEnd{st, err}
		}()
		for feedKinds(t, s, storage.DefaultCompany)["node.keys_rotation_requested"] < 2 { // the repeated proposal reached Core
			select {
			case end := <-again:
				t.Fatal("the second run ended:", end.err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(2 * time.Millisecond):
			}
		}
		if code, out := call(t, c, "POST", "/node-keys/"+n.id+"/approve", "owner", map[string]string{"fingerprint": fp}); code != 200 {
			t.Fatal(code, out)
		}
		if end := <-again; end.err != nil || end.status.Fingerprint != fp {
			t.Fatalf("%+v", end)
		}
	})

	t.Run("revoked", func(t *testing.T) {
		c, s, _, n, url, ctx := setup(t)
		_, done := n.rotate(ctx, t, s, url)
		if code, out := call(t, c, "POST", "/nodes/"+n.id+"/revoke", "owner", nil); code != 200 {
			t.Fatal(code, out)
		}
		if end := <-done; end.err == nil || !strings.Contains(end.err.Error(), "revoked") {
			t.Fatalf("%+v", end)
		}
		// A revoked node cannot start one either.
		if _, err := (node.Rotation{URL: url, StateDir: n.dir, Poll: 5 * time.Millisecond, Out: io.Discard, Now: s.Now}).Run(ctx); err == nil || !strings.Contains(err.Error(), "401") {
			t.Fatal(err)
		}
	})
}

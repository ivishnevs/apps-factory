package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appfactory/internal/nodesig"
	"appfactory/internal/storage"
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

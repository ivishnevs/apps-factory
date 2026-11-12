package core

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/nodesig"
	"appfactory/internal/storage"
)

func (n enrollingNode) connectRequest(name string) storage.NodeConnectRequest {
	proof := ed25519.Sign(n.key, nodesig.ConnectMessage(n.id, name, n.signPub, n.boxPub))
	return storage.NodeConnectRequest{NodeID: n.id, Name: name, OS: "darwin", Arch: "arm64", SignPub: n.signPub, BoxPub: n.boxPub, Proof: hex.EncodeToString(proof)}
}

func connectCall(t *testing.T, c *Core, body any) (int, NodeConnectAnswer) {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/node-connects", strings.NewReader(string(b)))
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	var a NodeConnectAnswer
	_ = json.Unmarshal(w.Body.Bytes(), &a)
	return w.Code, a
}

func (n enrollingNode) connectStatus(t *testing.T, c *Core, id string, key ed25519.PrivateKey) (int, string) {
	t.Helper()
	r := httptest.NewRequest("GET", "/node-connects/"+id+"/status", nil)
	if key != nil {
		if err := nodesig.Sign(r, n.id, key, nil, c.Store.Now()); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// TestNodeConnect (node-install 2.1, 2.2): a node asks for a connection with its keys and no credential; an admin
// approves it for the company and the keys are active at once; refused, used and expired requests give nothing.
func TestNodeConnect(t *testing.T) {
	c, _, fake := nodeCore(t)
	c.PublicURL = "https://app.example"
	n := newEnrollingNode(t)

	bad := n.connectRequest("mac")
	bad.Name = "other" // the proof covers the name
	if code, _ := connectCall(t, c, bad); code != 400 {
		t.Fatal("proof over another name accepted", code)
	}
	code, a := connectCall(t, c, n.connectRequest("mac"))
	if code != 200 || a.ID == "" || len(a.Code) != 9 || a.URL != "https://app.example/connect/"+a.ID {
		t.Fatal(code, a)
	}
	sign, _ := hex.DecodeString(n.signPub)
	box, _ := hex.DecodeString(n.boxPub)
	if a.Fingerprint != nodesig.Fingerprint(sign, box) {
		t.Fatal(a)
	}

	// The status is for the key holder only; before approval the keys are no credential.
	if code, _ := n.connectStatus(t, c, a.ID, nil); code != 401 {
		t.Fatal("unsigned status", code)
	}
	if code, _ := n.connectStatus(t, c, a.ID, newEnrollingNode(t).key); code != 401 {
		t.Fatal("status signed by another key", code)
	}
	if code, out := n.connectStatus(t, c, a.ID, n.key); code != 200 || !strings.Contains(out, `"pending"`) {
		t.Fatal(code, out)
	}
	if code, _ := n.status(t, c, n.key); code != 401 {
		t.Fatal("pending connection is a credential", code)
	}

	// The page shows the request to admins, never its code (security-review-2026-11 F2): approving needs the code
	// typed from the machine's terminal, and makes the keys active in the company.
	if code, out := call(t, c, "GET", "/node-connects/"+a.ID, "owner", nil); code != 200 || strings.Contains(out, a.Code) || !strings.Contains(out, `"mac"`) {
		t.Fatal(code, out)
	}
	if code, _ := call(t, c, "POST", "/node-connects/"+a.ID+"/approve", "", map[string]string{"code": a.Code}); code != 401 {
		t.Fatal("approved without a credential", code)
	}
	for _, typed := range []string{"", "2222-2222"} {
		if code, out := call(t, c, "POST", "/node-connects/"+a.ID+"/approve", "owner", map[string]string{"code": typed}); code != 409 || !strings.Contains(out, "code_mismatch") {
			t.Fatal("approved with the link alone", typed, code, out)
		}
	}
	if code, _ := call(t, c, "POST", "/node-connects/"+a.ID+"/approve", "owner", nil); code != 400 {
		t.Fatal("approved without a code", code)
	}
	if code, out := n.connectStatus(t, c, a.ID, n.key); code != 200 || !strings.Contains(out, `"pending"`) {
		t.Fatal("a wrong code decided the request", code, out)
	}
	typed := strings.ToLower(strings.ReplaceAll(a.Code, "-", " "))
	if code, out := call(t, c, "POST", "/node-connects/"+a.ID+"/approve", "owner", map[string]string{"code": typed}); code != 200 || !strings.Contains(out, `"active"`) {
		t.Fatal(code, out)
	}
	if code, out := n.connectStatus(t, c, a.ID, n.key); code != 200 || !strings.Contains(out, `"approved"`) || !strings.Contains(out, `"company":"`+storage.DefaultCompany+`"`) {
		t.Fatal(code, out)
	}
	if code, out := n.status(t, c, n.key); code != 200 || !strings.Contains(out, `"active"`) {
		t.Fatal(code, out)
	}
	if code, _ := call(t, c, "POST", "/node-connects/"+a.ID+"/approve", "owner", map[string]string{"code": a.Code}); code != 404 {
		t.Fatal("approved twice", code)
	}
	if code, _ := connectCall(t, c, n.connectRequest("mac")); code != 409 {
		t.Fatal("a node with keys asked again", code)
	}

	// Rejected and expired requests cannot be approved.
	x := newEnrollingNode(t)
	_, ax := connectCall(t, c, x.connectRequest("x"))
	if code, _ := call(t, c, "POST", "/node-connects/"+ax.ID+"/reject", "owner", nil); code != 200 {
		t.Fatal(code)
	}
	if code, out := x.connectStatus(t, c, ax.ID, x.key); code != 200 || !strings.Contains(out, `"rejected"`) {
		t.Fatal(code, out)
	}
	if code, _ := call(t, c, "POST", "/node-connects/"+ax.ID+"/approve", "owner", map[string]string{"code": ax.Code}); code != 404 {
		t.Fatal("rejected request approved", code)
	}
	y := newEnrollingNode(t)
	_, ay := connectCall(t, c, y.connectRequest("y"))
	fake.Advance(storage.NodeConnectTTL + time.Second)
	if code, out := y.connectStatus(t, c, ay.ID, y.key); code != 200 || !strings.Contains(out, `"expired"`) {
		t.Fatal(code, out)
	}
	if code, _ := call(t, c, "POST", "/node-connects/"+ay.ID+"/approve", "owner", map[string]string{"code": ay.Code}); code != 404 {
		t.Fatal("expired request approved", code)
	}
}

// TestNodeDownloads (node-install 1.2): the install script names Core's address for nodes and the packages are
// served without a credential; other names are not.
func TestNodeDownloads(t *testing.T) {
	c, _, _ := nodeCore(t)
	dir := t.TempDir()
	c.NodeDownloads = dir
	if err := os.WriteFile(filepath.Join(dir, "factory-node-darwin-arm64.tar.gz"), []byte("pkg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte("x  factory-node-darwin-arm64.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	get := func(path string, header map[string]string) (int, string) {
		r := httptest.NewRequest("GET", path, nil)
		r.Host = "core.example:8080"
		for k, v := range header {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	if code, body := get("/install.sh", nil); code != 200 || !strings.Contains(body, `CORE_URL="http://core.example:8080"`) || strings.Contains(body, "{{{") {
		t.Fatal(code, body[:min(len(body), 300)])
	}
	if _, body := get("/install.sh", map[string]string{"X-Forwarded-Proto": "https"}); !strings.Contains(body, `CORE_URL="https://core.example:8080"`) {
		t.Fatal("forwarded scheme ignored")
	}
	c.NodeURL = "https://nodes.example/"
	if _, body := get("/install.sh", nil); !strings.Contains(body, `CORE_URL="https://nodes.example"`) {
		t.Fatal("FACTORY_CORE_URL ignored")
	}
	if code, body := get("/downloads/node/factory-node-darwin-arm64.tar.gz", nil); code != 200 || body != "pkg" {
		t.Fatal(code, body)
	}
	if code, _ := get("/downloads/node/SHA256SUMS", nil); code != 200 {
		t.Fatal(code)
	}
	for _, p := range []string{"/downloads/node/factory-node-linux-amd64.tar.gz", "/downloads/node/../../etc/passwd", "/downloads/node/node.yaml"} {
		if code, _ := get(p, nil); code != 404 && code != 301 {
			t.Fatal(p, code)
		}
	}
	if code, out := call(t, c, "GET", "/node-install", "owner", nil); code != 200 || !strings.Contains(out, `"darwin-arm64"`) || !strings.Contains(out, "https://nodes.example/install.sh | sh") {
		t.Fatal(code, out)
	}
}

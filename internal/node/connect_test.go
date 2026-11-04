package node

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"appfactory/internal/nodesig"
)

// fakeConnectCore checks the request's proof and the signed polls, and answers statuses from a script.
type fakeConnectCore struct {
	mu       sync.Mutex
	t        *testing.T
	statuses []string
	pub      ed25519.PublicKey
	node     string
	name     string
}

func (f *fakeConnectCore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodPost && r.URL.Path == "/node-connects" {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		sign, box, err := nodesig.DecodePublicKeys(in["sign_pub"], in["box_pub"])
		if err != nil {
			f.t.Error(err)
		}
		proof, _ := hex.DecodeString(in["proof"])
		if !ed25519.Verify(sign, nodesig.ConnectMessage(in["node_id"], in["name"], in["sign_pub"], in["box_pub"]), proof) || in["os"] == "" || in["arch"] == "" {
			f.t.Error("bad request", in)
		}
		f.pub, f.node, f.name = sign, in["node_id"], in["name"]
		_ = json.NewEncoder(w).Encode(ConnectAnswer{ID: "c1", Code: "ABCD-EFGH", URL: "https://app.example/connect/c1", ExpiresAt: time.Now().Add(15 * time.Minute).Unix(), Fingerprint: nodesig.Fingerprint(sign, box)})
		return
	}
	if v, err := nodesig.Verify(r, f.pub, nil, time.Now()); err != nil || v.Node != f.node || r.URL.Path != "/node-connects/c1/status" {
		f.t.Error("status request:", r.URL.Path, err)
	}
	s := f.statuses[0]
	if len(f.statuses) > 1 {
		f.statuses = f.statuses[1:]
	}
	_ = json.NewEncoder(w).Encode(ConnectStatus{Status: s, Company: "acme", Name: f.name})
}

// TestConnect (node-install 2.3): the node asks with proof of its new keys, shows the page and the code, opens the
// browser, waits for approval and stores its company; a node with a company is refused; a cancelled request ends.
func TestConnect(t *testing.T) {
	f := &fakeConnectCore{t: t, statuses: []string{"pending", "pending", "approved"}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	dir := t.TempDir()
	var out bytes.Buffer
	var opened string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := Connect{URL: srv.URL, Name: "mini", StateDir: dir, Out: &out, Poll: time.Millisecond, Open: func(u string) error { opened = u; return nil }}
	got, err := c.Run(ctx)
	if err != nil || got.Status != "approved" {
		t.Fatal(got, err)
	}
	if opened != "https://app.example/connect/c1" || !strings.Contains(out.String(), "ABCD-EFGH") || f.name != "mini" {
		t.Fatal(opened, out.String())
	}
	if company, err := LoadCompany(dir); err != nil || company != "acme" {
		t.Fatal(company, err)
	}
	if _, err := c.Run(ctx); err == nil || !strings.Contains(err.Error(), "already belongs") {
		t.Fatal("connected twice", err)
	}

	f2 := &fakeConnectCore{t: t, statuses: []string{"rejected"}}
	srv2 := httptest.NewServer(f2)
	defer srv2.Close()
	if _, err := (Connect{URL: srv2.URL, Name: "x", StateDir: t.TempDir(), Poll: time.Millisecond}).Run(ctx); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatal(err)
	}
}

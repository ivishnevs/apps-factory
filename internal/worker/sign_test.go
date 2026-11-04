package worker

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"appfactory/internal/nodesig"
)

// TestNodeClientsSign (node-identity 2.1): every node client signs what it really sends, also through a reverse
// proxy and for a log chunk at its largest size, and a retried request signs anew.
func TestNodeClientsSign(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const node = "0123456789abcdef0123456789abcdef"
	var mu sync.Mutex
	nonces := map[string]bool{}
	fail := 1
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		v, err := nodesig.Verify(r, pub, body, time.Now())
		mu.Lock()
		defer mu.Unlock()
		if err != nil || v.Node != node {
			t.Errorf("%s %s: %v", r.Method, r.URL, err)
		}
		if nonces[v.Nonce] {
			t.Errorf("%s %s: nonce repeated", r.Method, r.URL)
		}
		nonces[v.Nonce] = true
		if r.URL.Path == "/flaky" && fail > 0 {
			fail--
			http.Error(w, "busy", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer core.Close()
	target, _ := url.Parse(core.URL)
	proxy := httptest.NewServer(httputil.NewSingleHostReverseProxy(target))
	defer proxy.Close()

	for _, base := range []string{core.URL, proxy.URL} {
		api := API{URL: base, Node: node, Key: key}
		ctx := context.Background()
		if err = api.Do(ctx, "POST", "/runs/r1/claim", map[string]string{"node_id": node}, nil); err != nil {
			t.Fatal(err)
		}
		if err = api.Do(ctx, "GET", "/runs/r1/x?a=1&b=two%20words", nil, nil); err != nil {
			t.Fatal(err)
		}
		if err = api.putLog(ctx, "r1", "lease", 1, false, bytes.Repeat([]byte("line\n"), LogChunk/5)); err != nil {
			t.Fatal(err)
		}
		if err = api.Fetch(ctx, "/runs/r1/blobs/abc", "lease", io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	api := API{URL: core.URL, Node: node, Key: key}
	if err = api.Retry(context.Background(), 3, "/flaky", map[string]string{"a": "b"}, nil); err != nil {
		t.Fatal(err)
	}
	if fail != 0 {
		t.Fatal("no retry happened")
	}
}

// TestAPIWithoutKeys: the owner CLI shares this client and sends only its token.
func TestAPIWithoutKeys(t *testing.T) {
	var auth, input string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, input = r.Header.Get("Authorization"), r.Header.Get("Signature-Input")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	if err := (API{URL: srv.URL, Token: "fot_x"}).Do(context.Background(), "GET", "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer fot_x" || input != "" || strings.Contains(auth, "sig") {
		t.Fatal(auth, input)
	}
}

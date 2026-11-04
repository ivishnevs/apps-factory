package node_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "appfactory/internal/node"
	"appfactory/internal/nodesig"
)

// TestRemoteChecksSkippedWithoutEnv: no FACTORY_URL means no network check at all.
func TestRemoteChecksSkippedWithoutEnv(t *testing.T) {
	m := byCheck(doctor(healthy(), "darwin").Run(context.Background()))
	if _, ok := m["nats"]; m["core"].Status != Skip || ok {
		t.Fatal(m["core"], m["nats"])
	}
}

// TestCoreCheck: a GET /health signed with the node's keys, read-only; no bearer token is sent; without keys the
// check fails before any request.
func TestCoreCheck(t *testing.T) {
	state := t.TempDir()
	keys, err := GenerateKeys(state)
	if err != nil {
		t.Fatal(err)
	}
	id, err := NodeID(state)
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	status := http.StatusOK
	accept := keys.SignPublic()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		v, err := nodesig.Verify(r, accept, nil, time.Now())
		if r.Header.Get("Authorization") != "" || err != nil || v.Node != id {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	other, err := GenerateKeys(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name, url, state string
		refuse           bool
		status           int
		want             Status
		detail           string
	}{
		{"healthy", srv.URL + "/", state, false, 200, Pass, "healthy"},
		{"refused signature", srv.URL, state, true, 200, Fail, "signature was refused"},
		{"no keys", srv.URL, t.TempDir(), false, 200, Fail, "no usable node keys"},
		{"unhealthy", srv.URL, state, false, 503, Fail, "503"},
		{"down", closed.URL, state, false, 200, Fail, "unreachable"},
		{"not a url", "core:8080", state, false, 200, Fail, "not an http(s) URL"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := healthy()
			f.env["FACTORY_URL"] = c.url
			status, accept = c.status, keys.SignPublic()
			if c.refuse {
				accept = other.SignPublic()
			}
			d := doctor(f, "darwin")
			d.StateDir = c.state
			before := len(methods)
			r := byCheck(d.Run(context.Background()))["core"]
			if r.Status != c.want || !strings.Contains(r.Detail, c.detail) {
				t.Fatalf("%+v", r)
			}
			if c.state != state && len(methods) != before {
				t.Fatal("a request without keys was sent")
			}
		})
	}
	if len(methods) == 0 {
		t.Fatal("no request reached Core")
	}
	for _, m := range methods {
		if m != "GET /health" {
			t.Fatalf("unexpected request %s", m)
		}
	}
}

package node_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	. "appfactory/internal/node"
)

// TestRemoteChecksSkippedWithoutEnv: no FACTORY_URL or NATS_URL means no network check at all.
func TestRemoteChecksSkippedWithoutEnv(t *testing.T) {
	m := byCheck(doctor(healthy(), "darwin").Run(context.Background()))
	if m["core"].Status != Skip || m["nats"].Status != Skip {
		t.Fatal(m["core"], m["nats"])
	}
}

// TestCoreCheck: an authenticated GET /health, read-only; the token is sent but never shown.
func TestCoreCheck(t *testing.T) {
	var methods []string
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer good-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	for _, c := range []struct {
		name, url, token string
		status           int
		want             Status
		detail           string
	}{
		{"healthy", srv.URL + "/", "good-token", 200, Pass, "healthy"},
		{"bad token", srv.URL, "bad-token", 200, Fail, "FACTORY_TOKEN rejected"},
		{"no token", srv.URL, "", 200, Fail, "FACTORY_TOKEN not set"},
		{"unhealthy", srv.URL, "good-token", 503, Fail, "503"},
		{"down", closed.URL, "good-token", 200, Fail, "unreachable"},
		{"not a url", "core:8080", "good-token", 200, Fail, "not an http(s) URL"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := healthy()
			f.env["FACTORY_URL"], f.env["FACTORY_TOKEN"] = c.url, c.token
			status = c.status
			r := byCheck(doctor(f, "darwin").Run(context.Background()))["core"]
			if r.Status != c.want || !strings.Contains(r.Detail, c.detail) || c.token != "" && strings.Contains(r.Detail, c.token) {
				t.Fatalf("%+v", r)
			}
		})
	}
	for _, m := range methods {
		if m != "GET /health" {
			t.Fatalf("unexpected request %s", m)
		}
	}
}

// TestNATSCheckUnreachable: a refused connection fails fast with a hint.
func TestNATSCheckUnreachable(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	f := healthy()
	f.env["NATS_URL"] = "nats://user:secret@" + addr
	r := byCheck(doctor(f, "darwin").Run(context.Background()))["nats"]
	if r.Status != Fail || !strings.Contains(r.Detail, "unreachable") || strings.Contains(r.Detail, "secret") || r.Hint == "" {
		t.Fatalf("%+v", r)
	}
}

// TestNATSCheckJetStream connects to a real JetStream server (NATS_TEST_URL) and reads its account info.
func TestNATSCheckJetStream(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to a dedicated JetStream instance")
	}
	f := healthy()
	f.env["NATS_URL"] = url
	r := byCheck(doctor(f, "darwin").Run(context.Background()))["nats"]
	if r.Status != Pass || !strings.Contains(r.Detail, "JetStream:") {
		t.Fatalf("%+v", r)
	}
}

package node_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"appfactory/internal/bus"
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
		name, url, token, nodeToken string
		status                      int
		want                        Status
		detail                      string
	}{
		{"healthy", srv.URL + "/", "good-token", "", 200, Pass, "healthy"},
		{"bad token", srv.URL, "bad-token", "", 200, Fail, "FACTORY_TOKEN rejected"},
		{"no token", srv.URL, "", "", 200, Fail, "no node token"},
		{"node token wins", srv.URL, "bad-token", "good-token", 200, Pass, "healthy"},
		{"bad node token", srv.URL, "good-token", "bad-token", 200, Fail, "FACTORY_NODE_TOKEN rejected"},
		{"unhealthy", srv.URL, "good-token", "", 503, Fail, "503"},
		{"down", closed.URL, "good-token", "", 200, Fail, "unreachable"},
		{"not a url", "core:8080", "good-token", "", 200, Fail, "not an http(s) URL"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := healthy()
			f.env["FACTORY_URL"], f.env["FACTORY_TOKEN"] = c.url, c.token
			status = c.status
			d := doctor(f, "darwin")
			if c.nodeToken != "" {
				d.Token, d.TokenSource = c.nodeToken, TokenEnv
			}
			r := byCheck(d.Run(context.Background()))["core"]
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

// TestNATSCheckNodeConfig: nats.url overrides NATS_URL; a group-readable credentials file is a warning, a
// missing one fails before dialing, naming the file only.
func TestNATSCheckNodeConfig(t *testing.T) {
	f := healthy()
	f.env["NATS_URL"] = "nats://user:secret@127.0.0.1:1"
	f.files["/etc/factory/node.creds"] = true // the fake host reports mode 0755
	d := doctor(f, "darwin")
	d.NATS = NATS{URL: "tls://127.0.0.1:1", Options: bus.Options{CredsFile: "/etc/factory/node.creds"}}
	m := byCheck(d.Run(context.Background()))
	if r := m["nats_credentials"]; r.Status != Warn || !strings.Contains(r.Detail, "/etc/factory/node.creds") {
		t.Fatalf("%+v", r)
	}
	if r := m["nats"]; r.Status != Fail || !strings.Contains(r.Detail, "tls://127.0.0.1:1") || !strings.Contains(r.Detail, "creds_file") || strings.Contains(r.Detail, "secret") {
		t.Fatalf("%+v", r)
	}
}

// TestNATSCheckSecure runs the check over secure WebSocket with the node's nkey user and private CA against the
// server of scripts/nats-secure-test.sh: the node permission set is enough for the JetStream account info.
func TestNATSCheckSecure(t *testing.T) {
	url, ca, seed := os.Getenv("NATS_TEST_SECURE_WSS_URL"), os.Getenv("NATS_TEST_SECURE_CA"), os.Getenv("NATS_TEST_NODE_SEED")
	if url == "" || ca == "" || seed == "" {
		t.Skip("run scripts/nats-secure-test.sh up and export its NATS_TEST_* variables")
	}
	f := healthy()
	d := doctor(f, "darwin")
	d.Host.Stat = os.Stat
	d.NATS = NATS{URL: url, Options: bus.Options{NkeySeedFile: seed, TLS: bus.TLS{CAFile: ca, ServerName: os.Getenv("NATS_TEST_SECURE_SERVER_NAME")}}}
	m := byCheck(d.Run(context.Background()))
	if r := m["nats"]; r.Status != Pass || !strings.Contains(r.Detail, "JetStream:") {
		t.Fatalf("%+v", r)
	}
	if r, ok := m["nats_credentials"]; ok {
		t.Fatalf("seed is 0600: %+v", r)
	}
	d.NATS.TLS.CAFile = ""
	if r := byCheck(d.Run(context.Background()))["nats"]; r.Status != Fail || !strings.Contains(r.Detail, "certificate") {
		t.Fatalf("%+v", r)
	}
}

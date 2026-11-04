package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appfactory/internal/core"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
)

// TestWebAPIInCore (saas-web 2.1): Core serves the web API in its own process. Without sign-in it refuses unless
// FACTORY_WEB_LOCAL is set; locally it reads the board, refuses an action without X-Factory-Web, reaches Core's owner
// routes through /api/core and streams the feed through the in-memory transport.
func TestWebAPIInCore(t *testing.T) {
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	c := &core.Core{Store: s, Token: "owner-token"}
	if err = s.EnsureCompany(context.Background(), storage.DefaultCompany); err != nil {
		t.Fatal(err)
	}
	closed := httptest.NewServer(withWebAPI(c, false))
	defer closed.Close()
	if res, err := http.Get(closed.URL + "/api/meta"); err != nil || res.StatusCode != 401 {
		t.Fatal("web API without sign-in or local flag", res, err)
	}
	if res, err := http.Get(closed.URL + "/health"); err != nil || res.StatusCode != 401 {
		t.Fatal("Core's own routes keep their auth", res, err)
	}

	srv := httptest.NewServer(withWebAPI(c, true))
	defer srv.Close()
	get := func(path string) (int, string) {
		t.Helper()
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if code, out := get("/api/overview"); code != 200 || !strings.Contains(out, `"health":{"status":"ok"`) {
		t.Fatal(code, out)
	}
	if code, out := get("/api/core/secrets"); code != 200 {
		t.Fatal(code, out)
	}
	res, err := http.Post(srv.URL+"/api/action", "application/json", strings.NewReader(`{"action":"pause"}`))
	if err != nil || res.StatusCode != 400 {
		t.Fatal("action without X-Factory-Web", res, err)
	}
	// The feed streams through the pipe: the first event arrives while the stream stays open.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/stream/feed", nil)
	stream, err := http.DefaultClient.Do(req)
	if err != nil || stream.StatusCode != 200 {
		t.Fatal(stream, err)
	}
	defer stream.Body.Close()
	if err = s.SetPause(context.Background(), storage.DefaultCompany, storage.Pause{Scope: "company", Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stream.Body).ReadString('\n')
	if err != nil || line == "" {
		t.Fatal("no streamed line", line, err)
	}
}

// TestWebPagesThroughCore: with FACTORY_WEB_UPSTREAM, Core serves the web UI's pages from the Next.js server and
// keeps /api, /auth and its own routes; without it, page paths are Core's as before.
func TestWebPagesThroughCore(t *testing.T) {
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	c := &core.Core{Store: s, Token: "owner-token"}
	if err = s.EnsureCompany(context.Background(), storage.DefaultCompany); err != nil {
		t.Fatal(err)
	}
	next := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "page "+r.URL.Path)
	}))
	defer next.Close()
	t.Setenv("FACTORY_WEB_UPSTREAM", next.URL)
	srv := httptest.NewServer(withWebAPI(c, true))
	defer srv.Close()
	get := func(path string) (int, string) {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	for _, p := range []string{"/", "/signin", "/c/default/tasks/x", "/_next/static/a.js", "/connect/abc", "/favicon.ico"} {
		if code, body := get(p); code != 200 || body != "page "+p {
			t.Fatal(p, code, body)
		}
	}
	for _, p := range []string{"/api/meta", "/health", "/install.sh", "/tasks", "/auth/login"} {
		if _, body := get(p); strings.HasPrefix(body, "page ") {
			t.Fatal("Core route went to the web UI:", p)
		}
	}
	res, err := http.Post(srv.URL+"/c/default", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.HasPrefix(string(b), "page ") {
		t.Fatal("a POST went to the web UI")
	}
}

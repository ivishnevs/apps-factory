package core

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	l := rateLimiter{perAddress: 3, total: 5}
	now := time.Unix(1_800_000_000, 0)
	for i := 0; i < 3; i++ {
		if !l.allow("a", now) {
			t.Fatal(i)
		}
	}
	if l.allow("a", now) {
		t.Fatal("address limit not applied")
	}
	if !l.allow("b", now) || !l.allow("c", now) {
		t.Fatal("another address limited")
	}
	if l.allow("d", now) {
		t.Fatal("overall limit not applied")
	}
	if !l.allow("a", now.Add(time.Minute)) {
		t.Fatal("limit not reset")
	}
}

func TestPublicRateClass(t *testing.T) {
	for _, c := range []struct{ method, path, want string }{
		{"GET", "/auth/providers", "auth"},
		{"GET", "/auth/login", "auth"},
		{"GET", "/auth/callback", "auth"},
		{"POST", "/auth/logout", "auth"},
		{"GET", "/auth/invitations/abc", "invitation"},
		{"POST", "/node-connects", "enroll"},
		{"POST", "/nodes/enroll", "enroll"},
		{"GET", "/node-connects/nc1/status", "node-status"},
		{"GET", "/nodes/n1/enrollment", "node-status"},
		{"GET", "/install.sh", "download"},
		{"GET", "/downloads/node/factory-node-darwin-arm64.tar.gz", "download"},
		// Routes behind a credential are not limited here.
		{"GET", "/node-connects/nc1", ""},
		{"POST", "/node-connects/nc1/approve", ""},
		{"GET", "/health", ""},
		{"GET", "/tasks", ""},
		{"POST", "/nodes/register", ""},
	} {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := publicRateClass(r); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.method, c.path, got, c.want)
		}
	}
}

// TestPublicRateLimits (saas-release-readiness 2.3): every public route answers 429 with Retry-After once one
// address used its minute's share; another address still gets through; the next minute starts over; routes
// behind a credential are not counted.
func TestPublicRateLimits(t *testing.T) {
	c, _, fake := nodeCore(t)
	h := c.Handler()
	do := func(method, path, addr string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader("{}"))
		r.Header.Set("X-Forwarded-For", addr)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, c := range []struct {
		method, path, class string
	}{
		{"GET", "/auth/providers", "auth"},
		{"GET", "/auth/invitations/some-token", "invitation"},
		{"POST", "/node-connects", "enroll"},
		{"GET", "/node-connects/nc1/status", "node-status"},
		{"GET", "/install.sh", "download"},
		{"GET", "/downloads/node/x.tar.gz", "download"},
	} {
		addr := "198.51.100." + fmt.Sprint(len(c.path))
		limit := rateClasses[c.class].perAddress
		if c.path == "/downloads/node/x.tar.gz" {
			limit = 0 // the installer used the class's share from this address above
			addr = "198.51.100." + fmt.Sprint(len("/install.sh"))
		}
		for i := 0; i < limit; i++ {
			if w := do(c.method, c.path, addr); w.Code == http.StatusTooManyRequests {
				t.Fatalf("%s %s refused at %d of %d", c.method, c.path, i, limit)
			}
		}
		w := do(c.method, c.path, addr)
		if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "60" || !strings.Contains(w.Body.String(), "rate_limited") {
			t.Fatalf("%s %s over the limit: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
		if w := do(c.method, c.path, "203.0.113.9"); w.Code == http.StatusTooManyRequests {
			t.Fatalf("%s %s: another address refused", c.method, c.path)
		}
	}
	// A credentialed route is never counted.
	for i := 0; i < 200; i++ {
		r := httptest.NewRequest("GET", "/agents", nil)
		r.Header.Set("Authorization", "Bearer owner")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == http.StatusTooManyRequests {
			t.Fatal("owner route limited")
		}
	}
	// The next minute starts over.
	fake.Advance(time.Minute)
	if w := do("GET", "/auth/providers", "198.51.100.15"); w.Code != 200 {
		t.Fatal("next minute", w.Code, w.Body.String())
	}
}

// TestPublicRateLimitOverall: the overall limit holds when every request forges a new address.
func TestPublicRateLimitOverall(t *testing.T) {
	c, _, _ := nodeCore(t)
	h := c.Handler()
	limited := 0
	for i := 0; i < rateClasses["download"].total+10; i++ {
		r := httptest.NewRequest("GET", "/install.sh", nil)
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("10.%d.%d.1", i/250, i%250))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited != 10 {
		t.Fatal("refused", limited)
	}
}

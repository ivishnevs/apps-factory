package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"appfactory/internal/tui"
	"appfactory/internal/worker"
)

// Who the web UI acts as (user-accounts D5, D6). With a token (FACTORY_TOKEN, an owner token) every browser acts
// as that token's owner, as before. In session mode there is no token: each person signs in on the website,
// and the person's session cookie, CSRF header and chosen company travel with every call to Core, which
// decides. Clients and caches are kept per company and session, so nothing one person read reaches another.

const (
	sessionCookie = "factory_session"
	csrfCookie    = "factory_csrf"
	// companyCookie is the company of the page, for requests the browser makes without our headers (images,
	// frames, event streams); the X-Factory-Company header wins when present.
	companyCookie = "factory_company"
	companyHeader = "X-Factory-Company"
	csrfHeader    = "X-Factory-CSRF"
)

// forwardKey carries the headers of the browser's request to the client's transport.
type forwardKey struct{}

type forwarded struct {
	company string
	session string
	csrf    string
	csrfTok string
}

// state is one company's client and caches for one session (or for the token).
type state struct {
	client tui.Client
	tail   feedTail
	lib    library
	used   time.Time
}

// stateIdle drops the state of a session nobody used for this long.
const stateIdle = 15 * time.Minute

func company(r *http.Request) string {
	if c := strings.TrimSpace(r.Header.Get(companyHeader)); c != "" {
		return c
	}
	if c := strings.TrimSpace(r.URL.Query().Get("company")); c != "" {
		return c
	}
	if ck, err := r.Cookie(companyCookie); err == nil {
		return ck.Value
	}
	return ""
}

func cookie(r *http.Request, name string) string {
	if ck, err := r.Cookie(name); err == nil {
		return ck.Value
	}
	return ""
}

// withForward puts what the browser sent into r's context, for the transport.
func withForward(r *http.Request) *http.Request {
	f := forwarded{company: company(r), session: cookie(r, sessionCookie), csrf: cookie(r, csrfCookie), csrfTok: r.Header.Get(csrfHeader)}
	return r.WithContext(context.WithValue(r.Context(), forwardKey{}, f))
}

// forward sets the browser's company, session and CSRF on Core requests; without a token it sends none.
type forward struct{ next http.RoundTripper }

func (t forward) RoundTrip(r *http.Request) (*http.Response, error) {
	f, _ := r.Context().Value(forwardKey{}).(forwarded)
	r = r.Clone(r.Context())
	if a := r.Header.Get("Authorization"); a == "Bearer " || a == "Bearer" {
		r.Header.Del("Authorization")
	}
	if f.company != "" {
		r.Header.Set(companyHeader, f.company)
	}
	if r.Header.Get("Authorization") == "" && f.session != "" {
		cookies := []string{sessionCookie + "=" + f.session}
		if f.csrf != "" {
			cookies = append(cookies, csrfCookie+"="+f.csrf)
		}
		r.Header.Set("Cookie", strings.Join(cookies, "; "))
		if f.csrfTok != "" {
			r.Header.Set(csrfHeader, f.csrfTok)
		}
	}
	next := t.next
	if next == nil {
		next = http.DefaultTransport
	}
	return next.RoundTrip(r)
}

// state is the client and caches of r's company and session.
func (s *Server) state(r *http.Request) *state {
	if s.Core == "" { // a fixture (demo): one state for every company
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.demo == nil {
			s.demo = &state{client: s.Client}
		}
		return s.demo
	}
	key := company(r)
	if s.Token == "" {
		h := sha256.Sum256([]byte(cookie(r, sessionCookie)))
		key += "\x00" + hex.EncodeToString(h[:8])
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = map[string]*state{}
	}
	for k, st := range s.states {
		if now.Sub(st.used) > stateIdle {
			delete(s.states, k)
		}
	}
	st := s.states[key]
	if st == nil {
		st = &state{client: &tui.HTTPClient{
			API:    worker.API{URL: s.Core, Token: s.Token, Client: &http.Client{Transport: forward{next: s.Transport}, Timeout: 60 * time.Second}},
			Stream: &http.Client{Transport: forward{next: s.Transport}},
		}}
		s.states[key] = st
	}
	st.used = now
	return st
}

// coreProxy forwards a browser request to Core unchanged but for the credentials: the token in token mode,
// the session cookies otherwise. It serves /auth/* (sign-in: Core's redirects and cookies reach the browser)
// and /api/core/* (owner routes the web UI calls directly: /me, /company, secrets, nodes, skills, teams).
func (s *Server) coreProxy(strip string) http.Handler {
	target, _ := url.Parse(s.Core)
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, strip)
			pr.Out.URL.RawPath = ""
			pr.Out.Host = target.Host
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del(companyHeader)
			if c := company(pr.In); c != "" && strip != "" {
				pr.Out.Header.Set(companyHeader, c)
			}
			if s.Token != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+s.Token)
				return
			}
			var cookies []string
			for _, name := range []string{sessionCookie, csrfCookie, "factory_login"} {
				if v := cookie(pr.In, name); v != "" {
					cookies = append(cookies, name+"="+v)
				}
			}
			if len(cookies) > 0 {
				pr.Out.Header.Set("Cookie", strings.Join(cookies, "; "))
			}
		},
		FlushInterval: -1,
		Transport:     s.Transport,
	}
}

// corePaths are the Core routes /api/core/* reaches.
var corePaths = []string{"/me", "/company", "/companies", "/catalog", "/templates", "/secrets", "/nodes", "/node-enrollments", "/node-keys", "/node-install", "/node-connects",
	"/skills", "/presets", "/setup", "/owner-tokens", "/agents", "/projects", "/memberships", "/invitations", "/settings", "/telegram"}

func corePath(p string) bool {
	for _, prefix := range corePaths {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return !strings.Contains(p, "..")
		}
	}
	return false
}

// coreRoute serves /api/core/{path...}: GET freely, changes only with the X-Factory-Web header (a plain
// cross-site form cannot set it); in session mode Core also checks the CSRF header and the role.
func (s *Server) coreRoute(proxy http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/api/core")
		if s.Core == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = io.WriteString(w, `{"error":"this needs a live Core"}`+"\n")
			return
		}
		if !corePath(p) {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Factory-Web") != "1" {
			reply(w, nil, errInputf("X-Factory-Web header required"))
			return
		}
		proxy.ServeHTTP(w, r)
	}
}

type stateMap struct {
	mu     sync.Mutex
	states map[string]*state
	demo   *state
}

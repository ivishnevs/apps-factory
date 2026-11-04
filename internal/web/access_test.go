package web_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"appfactory/internal/core"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
	"appfactory/internal/web"
)

// TestSessionMode (user-accounts 6.1, 6.2): without a token the web API forwards each person's session and
// company to Core; a person reaches only companies of their memberships, caches are per session, changes need
// the CSRF header, and /auth/* and /api/core/* reach Core.
func TestSessionMode(t *testing.T) {
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	ctx := context.Background()
	c := &core.Core{Store: s, Root: "../..", Token: "operator"}
	if err = c.LoadCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	coreSrv := httptest.NewServer(c.Handler())
	defer coreSrv.Close()
	session := map[string]string{}
	for _, who := range []string{"ann", "bob"} {
		res, err := s.SignIn(ctx, storage.Identity{Provider: "google", Subject: who, Email: who + "@example.com", EmailVerified: true}, storage.NewCompany{ID: who + "-co", Name: who})
		if err != nil {
			t.Fatal(err)
		}
		if session[who], _, err = s.CreateSession(ctx, res.User.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.CreateProject(ctx, "ann-co", tasks.ProjectCreate{ID: "ann-secret", Name: "Ann's", Workspace: "/tmp/ann-secret"}); err != nil {
		t.Fatal(err)
	}
	h := (&web.Server{Mode: "live", Core: coreSrv.URL}).Handler()
	get := func(who, company, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if who != "" {
			r.AddCookie(&http.Cookie{Name: "factory_session", Value: session[who]})
		}
		if company != "" {
			r.Header.Set("X-Factory-Company", company)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := get("ann", "ann-co", "/api/feed"); w.Code != 200 || !strings.Contains(w.Body.String(), "ann-secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := get("ann", "ann-co", "/api/feed?before=0"); w.Code != 200 || !strings.Contains(w.Body.String(), "ann-secret") {
		t.Fatal(w.Code, w.Body.String())
	}
	// Bob asks for Ann's company: Core says not found; nothing cached for Ann reaches him.
	for _, path := range []string{"/api/feed", "/api/feed?before=0", "/api/materials"} {
		if w := get("bob", "ann-co", path); w.Code == 200 || strings.Contains(w.Body.String(), "ann-secret") {
			t.Fatal("foreign company", path, w.Code, w.Body.String())
		}
	}
	if w := get("", "ann-co", "/api/feed"); w.Code == 200 {
		t.Fatal("no session", w.Code)
	}
	var me core.Me
	if w := get("ann", "", "/api/core/me"); json.Unmarshal(w.Body.Bytes(), &me) != nil || me.Kind != "session" || me.Companies[0].Company != "ann-co" {
		t.Fatal(w.Code, w.Body.String())
	}
	// The company also comes from the page's cookie (images, frames, event streams send no header).
	r := httptest.NewRequest("GET", "/api/core/company", nil)
	r.AddCookie(&http.Cookie{Name: "factory_session", Value: session["ann"]})
	r.AddCookie(&http.Cookie{Name: "factory_company", Value: "ann-co"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"ann-co"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// A change: X-Factory-Web for this API, the CSRF header for Core.
	rename := func(csrf string) int {
		r := httptest.NewRequest("PATCH", "/api/core/company", strings.NewReader(`{"name":"Ann Games"}`))
		r.AddCookie(&http.Cookie{Name: "factory_session", Value: session["ann"]})
		r.AddCookie(&http.Cookie{Name: "factory_csrf", Value: "t0ken"})
		r.Header.Set("X-Factory-Company", "ann-co")
		r.Header.Set("X-Factory-Web", "1")
		r.Header.Set("X-Factory-CSRF", csrf)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := rename("wrong"); code != 403 {
		t.Fatal("without CSRF", code)
	}
	if code := rename("t0ken"); code != 200 {
		t.Fatal("with CSRF", code)
	}
	if w := get("ann", "ann-co", "/api/core/../health"); w.Code == 200 {
		t.Fatal("path outside the allowed routes", w.Code)
	}
	// Sign-in routes reach Core (not configured here: providers is empty).
	if w := get("", "", "/auth/providers"); w.Code != 200 || !strings.Contains(w.Body.String(), `"providers":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// The run stream relays with the session.
	r = httptest.NewRequest("GET", "/api/meta", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), `"access":"session"`) {
		t.Fatal(string(body))
	}
}

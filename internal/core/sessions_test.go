package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"appfactory/internal/auth"
	"appfactory/internal/auth/authtest"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
)

// browser is a person's browser against Core: it keeps cookies and sends the CSRF header like the web UI.
type browser struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]string
	company string
}

func (b *browser) do(method, path string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, rd)
	for k, v := range b.cookies {
		r.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	if b.company != "" {
		r.Header.Set(CompanyHeader, b.company)
	}
	if method != "GET" {
		r.Header.Set(CSRFHeader, b.cookies[CSRFCookie])
	}
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, r)
	for _, ck := range w.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(b.cookies, ck.Name)
		} else {
			b.cookies[ck.Name] = ck.Value
		}
	}
	return w
}

// signIn runs the whole sign-in with provider and returns where the callback redirected.
func (b *browser) signIn(is *authtest.Issuer, provider string, p authtest.Person) string {
	b.t.Helper()
	w := b.do("GET", "/auth/login?provider="+provider+"&return=/c/x/tasks", nil)
	if w.Code != 302 {
		b.t.Fatal(w.Code, w.Body.String())
	}
	q := is.Approve(b.t, w.Header().Get("Location"), p)
	w = b.do("GET", "/auth/callback?"+q.Encode(), nil)
	if w.Code != 302 {
		b.t.Fatal(w.Code, w.Body.String())
	}
	return w.Header().Get("Location")
}

func sessionCore(t *testing.T) (*Core, *authtest.Issuer) {
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	is := authtest.New(t, "factory-core")
	a, err := auth.New(auth.Config{Issuer: is.URL, ClientID: "factory-core", ClientSecret: "x", RedirectURL: "http://web/auth/callback",
		Providers: []string{"google", "github"}, CookieKey: []byte(strings.Repeat("c", 32))})
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Root: "../..", Token: "operator", Auth: a}
	if err = c.LoadCatalogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c, is
}

// TestSessionSignIn (user-accounts 4.3, 5.1, 5.2, 7.1): the first sign-in creates the user's company with its
// catalog and opens its setup; the session reaches that company only (404 for another), state changes need
// the CSRF header, the same person through GitHub with a verified email is the same user, sign-out revokes.
func TestSessionSignIn(t *testing.T) {
	c, is := sessionCore(t)
	h := c.Handler()
	ann := &browser{t: t, h: h, cookies: map[string]string{}}
	loc := ann.signIn(is, "google", authtest.Person{UserID: "g-ann", Email: "ann@example.com", EmailVerified: true, Name: "Ann Lee"})
	company, ok := strings.CutSuffix(strings.TrimPrefix(loc, "/c/"), "/setup")
	if !ok || !strings.HasPrefix(company, "ann-lee-") {
		t.Fatal("redirect", loc)
	}
	if ann.cookies[SessionCookie] == "" || ann.cookies[CSRFCookie] == "" {
		t.Fatal("cookies", ann.cookies)
	}
	var me Me
	if w := ann.do("GET", "/me", nil); json.Unmarshal(w.Body.Bytes(), &me) != nil || me.Kind != "session" || len(me.Companies) != 1 ||
		me.Companies[0] != (storage.Membership{Company: company, Name: "Ann Lee's company", Role: storage.RoleOwner}) || me.LastCompany != company || me.MultiCompany {
		t.Fatal(w.Code, w.Body.String())
	}
	// The company got the sign-up template's catalog, authored by the user.
	ann.company = company
	var view CatalogView
	if w := ann.do("GET", "/catalog", nil); json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Version != 1 || view.Author != "user:"+me.User.ID || view.Company != company {
		t.Fatal(w.Code, w.Body.String())
	}
	// No company named: refused; another company: not found, even an existing one.
	ann.company = ""
	if w := ann.do("GET", "/tasks", nil); w.Code != 400 {
		t.Fatal(w.Code)
	}
	ann.company = storage.DefaultCompany
	if w := ann.do("GET", "/tasks", nil); w.Code != 404 {
		t.Fatal("foreign company", w.Code)
	}
	ann.company = "nobody"
	if w := ann.do("GET", "/tasks", nil); w.Code != 404 {
		t.Fatal("unknown company", w.Code)
	}
	ann.company = company
	// CSRF: a state change without the header is refused.
	csrf := ann.cookies[CSRFCookie]
	ann.cookies[CSRFCookie] = "other"
	if w := ann.do("PATCH", "/company", map[string]string{"name": "Games"}); w.Code != 200 {
		t.Fatal("matching header and cookie", w.Code, w.Body.String())
	}
	r := httptest.NewRequest("PATCH", "/company", strings.NewReader(`{"name":"Evil"}`))
	r.AddCookie(&http.Cookie{Name: SessionCookie, Value: ann.cookies[SessionCookie]})
	r.AddCookie(&http.Cookie{Name: CSRFCookie, Value: csrf})
	r.Header.Set(CompanyHeader, company)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("no CSRF header", w.Code)
	}
	if name, _ := c.Store.CompanyName(context.Background(), company); name != "Games" {
		t.Fatal(name)
	}
	// Node and operator routes are not for people.
	if w := ann.do("POST", "/nodes/register", map[string]any{}); w.Code != 401 {
		t.Fatal("node route", w.Code)
	}
	if w := ann.do("GET", "/companies", nil); w.Code != 403 {
		t.Fatal("operator route", w.Code)
	}
	// One company per user while the setting is off.
	if w := ann.do("POST", "/companies", map[string]string{"name": "Second"}); w.Code != 403 || !strings.Contains(w.Body.String(), "multi_company_off") {
		t.Fatal("second company", w.Code, w.Body.String())
	}
	// Same person through GitHub, verified email: the same user, no second company, back to the start page.
	other := &browser{t: t, h: h, cookies: map[string]string{}}
	if loc := other.signIn(is, "github", authtest.Person{UserID: "42", Email: "ann@example.com", EmailVerified: true}); loc != "/c/x/tasks" {
		t.Fatal("linked sign-in", loc)
	}
	var me2 Me
	if w := other.do("GET", "/me", nil); json.Unmarshal(w.Body.Bytes(), &me2) != nil || me2.User.ID != me.User.ID || len(me2.Companies) != 1 {
		t.Fatal(w.Body.String())
	}
	// Unverified email of an existing user: refused, no session.
	eve := &browser{t: t, h: h, cookies: map[string]string{}}
	if loc := eve.signIn(is, "github", authtest.Person{UserID: "666", Email: "ann@example.com"}); loc != "/signin?error=email_unverified" || eve.cookies[SessionCookie] != "" {
		t.Fatal(loc, eve.cookies)
	}
	// Sign-out revokes the session.
	session := ann.cookies[SessionCookie]
	if w := ann.do("POST", "/auth/logout", nil); w.Code != 200 || ann.cookies[SessionCookie] != "" {
		t.Fatal(w.Code, ann.cookies)
	}
	ann.cookies[SessionCookie] = session
	if w := ann.do("GET", "/me", nil); w.Code != 401 || !strings.Contains(w.Body.String(), "session_expired") {
		t.Fatal("revoked session", w.Code)
	}
}

// TestForgedCallback (user-accounts 7.1): a callback whose state is not the browser's creates no session.
func TestForgedCallback(t *testing.T) {
	c, is := sessionCore(t)
	h := c.Handler()
	b := &browser{t: t, h: h, cookies: map[string]string{}}
	w := b.do("GET", "/auth/login?provider=google", nil)
	q := is.Approve(t, w.Header().Get("Location"), authtest.Person{UserID: "x", Email: "x@example.com", EmailVerified: true})
	q.Set("state", "forged")
	w = b.do("GET", "/auth/callback?"+q.Encode(), nil)
	if w.Code != 302 || w.Header().Get("Location") != "/signin?error=state" || b.cookies[SessionCookie] != "" {
		t.Fatal(w.Code, w.Header(), b.cookies)
	}
	var n int
	if err := c.Store.DB.QueryRow("SELECT count(*) FROM sessions").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	// A callback from another browser (no login cookie) is refused the same way.
	other := &browser{t: t, h: h, cookies: map[string]string{}}
	w = b.do("GET", "/auth/login?provider=google", nil)
	q = is.Approve(t, w.Header().Get("Location"), authtest.Person{UserID: "x", Email: "x@example.com", EmailVerified: true})
	if w = other.do("GET", "/auth/callback?"+q.Encode(), nil); w.Header().Get("Location") != "/signin?error=state" {
		t.Fatal(w.Header())
	}
	if w = b.do("GET", "/auth/login?provider=okta", nil); w.Code != 400 {
		t.Fatal("unknown provider", w.Code)
	}
	var providers struct {
		Providers []string `json:"providers"`
	}
	if w = b.do("GET", "/auth/providers", nil); json.Unmarshal(w.Body.Bytes(), &providers) != nil || strings.Join(providers.Providers, ",") != "google,github" {
		t.Fatal(w.Body.String())
	}
}

// TestRoles (user-accounts 5.3, 7.1): members work with tasks and read the catalog but cannot save it, issue
// owner tokens or rename the company; admins can, except renaming; members see secret names only. An owner
// token sent with another company's header is refused; owner tokens record who issued them.
func TestRoles(t *testing.T) {
	c, is := sessionCore(t)
	h := c.Handler()
	ctx := context.Background()
	owner := &browser{t: t, h: h, cookies: map[string]string{}}
	company := strings.TrimSuffix(strings.TrimPrefix(owner.signIn(is, "google", authtest.Person{UserID: "o", Email: "o@example.com", EmailVerified: true, Name: "Olga"}), "/c/"), "/setup")
	owner.company = company
	var me Me
	_ = json.Unmarshal(owner.do("GET", "/me", nil).Body.Bytes(), &me)
	people := map[string]*browser{}
	for _, role := range []string{storage.RoleAdmin, storage.RoleMember} {
		// Invitations are out of scope: the user and the membership are made in the store, in the owner's account.
		if err := c.Store.CreateUser(ctx, me.User.Account, role, role+"@example.com"); err != nil {
			t.Fatal(err)
		}
		if err := c.Store.AddMember(ctx, company, role, role); err != nil {
			t.Fatal(err)
		}
		id, _, err := c.Store.CreateSession(ctx, role)
		if err != nil {
			t.Fatal(err)
		}
		people[role] = &browser{t: t, h: h, cookies: map[string]string{SessionCookie: id, CSRFCookie: "csrf"}, company: company}
	}
	var view CatalogView
	_ = json.Unmarshal(owner.do("GET", "/catalog", nil).Body.Bytes(), &view)
	change := CatalogChange{Base: view.Version, DryRun: true}
	for _, tc := range []struct {
		who, method, path string
		body              any
		code              int
	}{
		{storage.RoleMember, "GET", "/catalog", nil, 200},
		{storage.RoleMember, "GET", "/tasks", nil, 200},
		{storage.RoleMember, "POST", "/catalog", change, 403},
		{storage.RoleMember, "POST", "/owner-tokens", map[string]string{"name": "ci"}, 403},
		{storage.RoleMember, "GET", "/owner-tokens", nil, 403},
		{storage.RoleMember, "PATCH", "/company", map[string]string{"name": "x"}, 403},
		{storage.RoleMember, "PUT", "/settings/company", map[string]string{"rules": "x"}, 403},
		{storage.RoleMember, "PUT", "/secrets/API_KEY", map[string]string{"value": "12345678"}, 403},
		{storage.RoleMember, "POST", "/projects", map[string]string{"id": "p"}, 403},
		{storage.RoleAdmin, "POST", "/catalog", change, 200},
		{storage.RoleAdmin, "PATCH", "/company", map[string]string{"name": "x"}, 403},
		{storage.RoleAdmin, "POST", "/owner-tokens", map[string]string{"name": "ci"}, 200},
		{storage.RoleOwner, "POST", "/catalog", change, 200},
	} {
		b := owner
		if tc.who != storage.RoleOwner {
			b = people[tc.who]
		}
		if w := b.do(tc.method, tc.path, tc.body); w.Code != tc.code {
			t.Errorf("%s %s %s: %d %s", tc.who, tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	tokens, _ := c.Store.OwnerTokens(ctx, company)
	if len(tokens) != 1 || tokens[0].CreatedBy != "user:"+storage.RoleAdmin {
		t.Fatalf("%+v", tokens)
	}
	// An owner token of this company with another company's header.
	tok, err := c.Store.CreateOwnerToken(ctx, company, "cli", "owner")
	if err != nil {
		t.Fatal(err)
	}
	for header, code := range map[string]int{"": 200, company: 200, storage.DefaultCompany: 403} {
		r := httptest.NewRequest("GET", "/tasks", nil)
		r.Header.Set("Authorization", "Bearer "+tok.Token)
		if header != "" {
			r.Header.Set(CompanyHeader, header)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != code {
			t.Errorf("owner token with header %q: %d", header, w.Code)
		}
	}
	// The operator may name a company for support; an unknown one is not found.
	for header, code := range map[string]int{company: 200, "nobody": 404} {
		r := httptest.NewRequest("GET", "/company", nil)
		r.Header.Set("Authorization", "Bearer operator")
		r.Header.Set(CompanyHeader, header)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != code || code == 200 && !strings.Contains(w.Body.String(), company) {
			t.Errorf("operator naming %q: %d %s", header, w.Code, w.Body.String())
		}
	}
}

// TestMultiCompany (user-accounts 5.2): with the setting on a user creates another company with its own
// catalog and both are listed.
func TestMultiCompany(t *testing.T) {
	c, is := sessionCore(t)
	c.MultiCompany = true
	b := &browser{t: t, h: c.Handler(), cookies: map[string]string{}}
	first := strings.TrimSuffix(strings.TrimPrefix(b.signIn(is, "google", authtest.Person{UserID: "m", Email: "m@example.com", EmailVerified: true}), "/c/"), "/setup")
	w := b.do("POST", "/companies", map[string]string{"name": "Side project"})
	var created storage.Membership
	if json.Unmarshal(w.Body.Bytes(), &created); w.Code != 201 || created.Role != storage.RoleOwner || created.Company == first || !strings.HasPrefix(created.Company, "side-project-") {
		t.Fatal(w.Code, w.Body.String())
	}
	var me Me
	if json.Unmarshal(b.do("GET", "/me", nil).Body.Bytes(), &me); len(me.Companies) != 2 || !me.MultiCompany {
		t.Fatalf("%+v", me)
	}
	b.company = created.Company
	var view CatalogView
	if w = b.do("GET", "/catalog", nil); json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Version != 1 || view.Company != created.Company {
		t.Fatal(w.Body.String())
	}
	if w = b.do("GET", "/tasks", nil); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Code, w.Body.String())
	}
}

// TestSecretNamesOnly (user-accounts 5.3, core-secret-store 1.2): a member's view of the secrets routes keeps
// names only, whatever the route answers.
func TestSecretNamesOnly(t *testing.T) {
	w := httptest.NewRecorder()
	n := &namesOnly{ResponseWriter: w}
	n.Header().Set("Content-Length", "999")
	_, _ = n.Write([]byte(`{"secrets":[{"name":"API_KEY","kind":"string","version":3,"used_by":["dev"],"created_by":"user:x"}],"count":1,"master":"leak"}`))
	n.finish()
	if got := strings.TrimSpace(w.Body.String()); got != `{"count":1,"secrets":[{"name":"API_KEY"}]}` {
		t.Fatal(got)
	}
	w = httptest.NewRecorder()
	n = &namesOnly{ResponseWriter: w}
	_, _ = n.Write([]byte("value=hunter2"))
	n.finish()
	if strings.Contains(w.Body.String(), "hunter2") {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	n = &namesOnly{ResponseWriter: w}
	n.WriteHeader(404)
	_, _ = n.Write([]byte("no secret"))
	n.finish()
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

// TestNodeKeyRoutesNeedAdmin (node-identity D3, D5): issuing codes, seeing and confirming keys and revoking a node are
// for owners and admins.
func TestNodeKeyRoutesNeedAdmin(t *testing.T) {
	for _, c := range []struct{ method, path string }{
		{"POST", "/node-enrollments"}, {"GET", "/node-keys"}, {"POST", "/node-keys/n/approve"}, {"POST", "/node-keys/n/reject"}, {"POST", "/nodes/n/revoke"}, {"DELETE", "/nodes/n"},
	} {
		if got := requiredRole(c.method, c.path); got != storage.RoleAdmin {
			t.Errorf("%s %s needs %q", c.method, c.path, got)
		}
	}
}

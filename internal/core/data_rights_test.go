package core

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"appfactory/internal/auth/authtest"
	"appfactory/internal/storage"
)

func zipFiles(t *testing.T, w *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment; filename=") {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	b := w.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		r, _ := f.Open()
		v, _ := io.ReadAll(r)
		r.Close()
		out[f.Name] = string(v)
	}
	return out
}

// TestCompanyDataRights (saas-release-readiness 4.2): only a signed-in owner exports or deletes the company (not
// an admin, not an owner token); deleting needs the CSRF header and the company's id typed as confirmation, and
// afterwards nobody reaches the company; Core's own company is never deleted.
func TestCompanyDataRights(t *testing.T) {
	c, is := sessionCore(t)
	h := c.Handler()
	ctx := context.Background()
	ann := &browser{t: t, h: h, cookies: map[string]string{}}
	loc := ann.signIn(is, "google", authtest.Person{UserID: "g-ann", Email: "ann@example.com", EmailVerified: true, Name: "Ann Lee"})
	ann.company, _ = strings.CutSuffix(strings.TrimPrefix(loc, "/c/"), "/setup")
	bob := &browser{t: t, h: h, cookies: map[string]string{}}
	bob.signIn(is, "github", authtest.Person{UserID: "gh-bob", Email: "bob@example.com", EmailVerified: true, Name: "Bob"})
	var me Me
	if w := bob.do("GET", "/me", nil); json.Unmarshal(w.Body.Bytes(), &me) != nil || me.User == nil {
		t.Fatal(w.Body.String())
	}
	account, err := c.Store.AccountOf(ctx, ann.company)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Store.DB.Exec("INSERT INTO memberships(account_id,company_id,user_id,role,created_at) VALUES(?,?,?,'admin',0)", account, ann.company, me.User.ID); err != nil {
		t.Fatal(err)
	}
	bob.company = ann.company
	if w := bob.do("GET", "/company/export", nil); w.Code != 403 {
		t.Fatal("admin exports", w.Code)
	}
	if w := bob.do("DELETE", "/company", DeleteConfirm{Confirm: ann.company}); w.Code != 403 {
		t.Fatal("admin deletes", w.Code)
	}
	tok, err := c.Store.CreateOwnerToken(ctx, ann.company, "cli", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, c, "DELETE", "/company", tok.Token, DeleteConfirm{Confirm: ann.company}); code != 403 {
		t.Fatal("owner token deletes", code)
	}
	if code, _ := call(t, c, "GET", "/company/export", tok.Token, nil); code != 403 {
		t.Fatal("owner token exports", code)
	}

	files := zipFiles(t, ann.do("GET", "/company/export", nil))
	var m storage.ExportManifest
	if json.Unmarshal([]byte(files["manifest.json"]), &m) != nil || m.Company != ann.company || m.Tables["memberships"] != 2 || m.Tables["catalog_versions"] == 0 {
		t.Fatal(files["manifest.json"])
	}
	if strings.Contains(files["tables/owner_tokens.json"], tok.Token) || strings.Contains(files["tables/owner_tokens.json"], `"hash"`) {
		t.Fatal("owner token exported", files["tables/owner_tokens.json"])
	}

	// No CSRF header: refused by the session gateway.
	r := httptest.NewRequest("DELETE", "/company", strings.NewReader(`{"confirm":"`+ann.company+`"}`))
	for k, v := range ann.cookies {
		r.Header.Add("Cookie", k+"="+v)
	}
	r.Header.Set(CompanyHeader, ann.company)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("no CSRF", w.Code)
	}
	if w := ann.do("DELETE", "/company", DeleteConfirm{Confirm: "yes"}); w.Code != 400 || !strings.Contains(w.Body.String(), `"confirm"`) {
		t.Fatal("wrong confirmation", w.Code, w.Body.String())
	}
	if w := ann.do("DELETE", "/company", DeleteConfirm{Confirm: ann.company}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := ann.do("GET", "/tasks", nil); w.Code != 404 {
		t.Fatal("deleted company reachable", w.Code)
	}
	me = Me{}
	if w := ann.do("GET", "/me", nil); json.Unmarshal(w.Body.Bytes(), &me) != nil || len(me.Companies) != 0 || me.LastCompany != "" {
		t.Fatal(w.Body.String())
	}
	if _, err = c.Store.AuthenticateOwnerToken(ctx, tok.Token); err == nil {
		t.Fatal("the company's token still works")
	}
	// Core's own company stays.
	if code, out := call(t, c, "DELETE", "/company", "operator", DeleteConfirm{Confirm: storage.DefaultCompany}); code != 400 {
		t.Fatal("Core's company", code, out)
	}
}

// TestAccountDataRights (saas-release-readiness 4.2): a person exports their data and deletes their account by
// typing their email; a renewing subscription is refused first; afterwards the session is gone and the account's
// company too.
func TestAccountDataRights(t *testing.T) {
	c, is := sessionCore(t)
	h := c.Handler()
	ann := &browser{t: t, h: h, cookies: map[string]string{}}
	loc := ann.signIn(is, "google", authtest.Person{UserID: "g-ann", Email: "ann@example.com", EmailVerified: true, Name: "Ann Lee"})
	company, _ := strings.CutSuffix(strings.TrimPrefix(loc, "/c/"), "/setup")

	files := zipFiles(t, ann.do("GET", "/me/export", nil))
	var acc storage.AccountExport
	if json.Unmarshal([]byte(files["account.json"]), &acc) != nil || acc.User.Email != "ann@example.com" || len(acc.Companies) != 1 || acc.Companies[0] != company ||
		files["companies/"+company+"/manifest.json"] == "" {
		t.Fatal(files["account.json"])
	}
	if code, _ := call(t, c, "GET", "/me/export", "operator", nil); code == 200 {
		t.Fatal("operator exports a person's account")
	}

	if w := ann.do("DELETE", "/me", DeleteConfirm{Confirm: "someone@example.com"}); w.Code != 400 {
		t.Fatal("wrong confirmation", w.Code)
	}
	account, _ := c.Store.AccountOf(context.Background(), company)
	if _, err := c.Store.DB.Exec("INSERT INTO account_subscriptions(account_id,plan,status,provider,subscription_id,updated_at) VALUES(?,'pro','active','paddle','sub_1',0)", account); err != nil {
		t.Fatal(err)
	}
	if w := ann.do("DELETE", "/me", DeleteConfirm{Confirm: "Ann@Example.com"}); w.Code != 409 || !strings.Contains(w.Body.String(), "subscription_active") {
		t.Fatal("renewing subscription", w.Code, w.Body.String())
	}
	if _, err := c.Store.DB.Exec("UPDATE account_subscriptions SET status='canceled' WHERE account_id=?", account); err != nil {
		t.Fatal(err)
	}
	var out storage.AccountDeletion
	if w := ann.do("DELETE", "/me", DeleteConfirm{Confirm: "Ann@Example.com"}); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Companies) != 1 || out.Companies[0] != company {
		t.Fatal(w.Code, w.Body.String())
	}
	if ann.cookies[SessionCookie] != "" {
		t.Fatal("session cookie kept")
	}
	if _, err := c.Store.CompanyName(context.Background(), company); err == nil {
		t.Fatal("company kept")
	}
	// Signing in again starts a new account.
	ann = &browser{t: t, h: h, cookies: map[string]string{}}
	if loc := ann.signIn(is, "google", authtest.Person{UserID: "g-ann", Email: "ann@example.com", EmailVerified: true, Name: "Ann Lee"}); !strings.HasSuffix(loc, "/setup") {
		t.Fatal("new sign-up", loc)
	}
}

// TestStatusNoteAndSupport (saas-release-readiness 2.4, 4.3): the operator's status note and the support address
// reach the public /auth/providers and /me; unset, neither appears; only the operator sets the note.
func TestStatusNoteAndSupport(t *testing.T) {
	c, is := sessionCore(t)
	h := c.Handler()
	anon := &browser{t: t, h: h, cookies: map[string]string{}}
	providers := func() map[string]any {
		var v map[string]any
		if w := anon.do("GET", "/auth/providers", nil); json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal(w.Body.String())
		}
		return v
	}
	if v := providers(); v["support_email"] != nil || v["status_note"] != nil {
		t.Fatal("shown while unset", v)
	}
	c.SupportEmail = "help@example.com"
	if code, out := call(t, c, "PUT", "/status-note", "operator", map[string]string{"note": "  Sign-in is slow;\n we are on it  "}); code != 200 || !strings.Contains(out, `"Sign-in is slow; we are on it"`) {
		t.Fatal(code, out)
	}
	if v := providers(); v["support_email"] != "help@example.com" || v["status_note"] != "Sign-in is slow; we are on it" {
		t.Fatal(v)
	}
	ann := &browser{t: t, h: h, cookies: map[string]string{}}
	loc := ann.signIn(is, "google", authtest.Person{UserID: "g-ann", Email: "ann@example.com", EmailVerified: true, Name: "Ann Lee"})
	var me Me
	if w := ann.do("GET", "/me", nil); json.Unmarshal(w.Body.Bytes(), &me) != nil || me.SupportEmail != "help@example.com" || me.StatusNote == "" {
		t.Fatal(w.Body.String())
	}
	ann.company, _ = strings.CutSuffix(strings.TrimPrefix(loc, "/c/"), "/setup")
	if w := ann.do("PUT", "/status-note", map[string]string{"note": "hacked"}); w.Code != 403 {
		t.Fatal("owner sets the note", w.Code)
	}
	if code, _ := call(t, c, "PUT", "/status-note", "operator", map[string]string{"note": strings.Repeat("x", 301)}); code != 400 {
		t.Fatal("long note", code)
	}
	if code, _ := call(t, c, "PUT", "/status-note", "operator", map[string]string{"note": ""}); code != 200 {
		t.Fatal(code)
	}
	if v := providers(); v["status_note"] != nil {
		t.Fatal("cleared note shown", v)
	}
}

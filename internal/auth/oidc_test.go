package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"appfactory/internal/auth"
	"appfactory/internal/auth/authtest"
)

func client(t *testing.T, is *authtest.Issuer) *auth.Client {
	c, err := auth.New(auth.Config{Issuer: is.URL, ClientID: is.ClientID, ClientSecret: "s", RedirectURL: "http://web/auth/callback",
		Providers: []string{"google", "github"}, CookieKey: []byte(strings.Repeat("k", 32))})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// signIn runs Begin, the issuer's approval and Finish; mutate may change the callback before Finish.
func signIn(t *testing.T, c *auth.Client, is *authtest.Issuer, p authtest.Person, mutate func(q url.Values, r *http.Request)) (auth.Claims, error) {
	ctx := context.Background()
	w := httptest.NewRecorder()
	to, err := c.Begin(ctx, w, "google", "/c/acme/tasks")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(to, "connector_id=google") {
		t.Fatalf("no connector_id: %s", to)
	}
	q := is.Approve(t, to, p)
	r := httptest.NewRequest("GET", "/auth/callback", nil)
	for _, ck := range w.Result().Cookies() {
		r.AddCookie(ck)
	}
	if mutate != nil {
		mutate(q, r)
	}
	r.URL.RawQuery = q.Encode()
	return c.Finish(ctx, httptest.NewRecorder(), r)
}

// TestSignIn (user-accounts 4.2): PKCE login against the issuer, the identity from federated_claims, the local
// return path; a callback with another state, without the login cookie or with a wrong nonce is refused.
func TestSignIn(t *testing.T) {
	is := authtest.New(t, "factory-core")
	c := client(t, is)
	got, err := signIn(t, c, is, authtest.Person{UserID: "g-1", Email: "ann@example.com", EmailVerified: true, Name: "Ann"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "google" || got.Subject != "g-1" || got.Email != "ann@example.com" || !got.EmailVerified || got.Name != "Ann" || got.Return != "/c/acme/tasks" {
		t.Fatalf("%+v", got)
	}
	if _, err = signIn(t, c, is, authtest.Person{UserID: "g-1"}, func(q url.Values, r *http.Request) { q.Set("state", "forged") }); !errors.Is(err, auth.ErrState) {
		t.Fatal("forged state", err)
	}
	if _, err = signIn(t, c, is, authtest.Person{UserID: "g-1"}, func(q url.Values, r *http.Request) { r.Header.Del("Cookie") }); !errors.Is(err, auth.ErrState) {
		t.Fatal("no login cookie", err)
	}
	if _, err = signIn(t, c, is, authtest.Person{UserID: "g-1"}, func(q url.Values, r *http.Request) {
		r.Header.Set("Cookie", auth.LoginCookie+"=tampered.sig")
	}); !errors.Is(err, auth.ErrState) {
		t.Fatal("tampered cookie", err)
	}
	is.Nonce = "replayed"
	if _, err = signIn(t, c, is, authtest.Person{UserID: "g-1"}, nil); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatal("nonce mismatch", err)
	}
	is.Nonce = ""
	if _, err = c.Begin(context.Background(), httptest.NewRecorder(), "okta", ""); !errors.Is(err, auth.ErrUnknownProvider) {
		t.Fatal(err)
	}
}

// TestInternalURL: Core reaches the issuer through another address than the browser (Dex in compose).
func TestInternalURL(t *testing.T) {
	is := authtest.New(t, "factory-core")
	// The issuer's public name is unreachable; the internal URL is the real server.
	c, err := auth.New(auth.Config{Issuer: is.URL, InternalURL: is.URL, ClientID: is.ClientID, RedirectURL: "http://web/auth/callback",
		Providers: []string{"google"}, CookieKey: []byte(strings.Repeat("k", 32))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = signIn(t, c, is, authtest.Person{UserID: "x", Email: "x@example.com"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestLocalPath(t *testing.T) {
	for in, want := range map[string]string{"/c/a": "/c/a", "//evil.com": "", "https://evil.com": "", "/\\evil": "", "": "", "/a\r\nb": ""} {
		if got := auth.LocalPath(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

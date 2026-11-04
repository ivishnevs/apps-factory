package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"appfactory/internal/auth"
)

// TestDexMockConnector (user-accounts 4.1, 4.2) signs in through a real Dex (deploy/dex/config.yaml with
// DEX_MOCK_CONNECTOR=1): DEX_TEST_ISSUER is its issuer, DEX_TEST_SECRET the client secret. Skipped without them.
func TestDexMockConnector(t *testing.T) {
	issuer := os.Getenv("DEX_TEST_ISSUER")
	if issuer == "" {
		t.Skip("DEX_TEST_ISSUER not set (a Dex with deploy/dex/config.yaml and DEX_MOCK_CONNECTOR=1)")
	}
	c, err := auth.New(auth.Config{Issuer: issuer, ClientID: "factory-core", ClientSecret: os.Getenv("DEX_TEST_SECRET"),
		RedirectURL: "http://127.0.0.1:3000/auth/callback", Providers: []string{"mock"}, CookieKey: []byte(strings.Repeat("k", 32))})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	w := httptest.NewRecorder()
	to, err := c.Begin(ctx, w, "mock", "/")
	if err != nil {
		t.Fatal(err)
	}
	// The browser: follow Dex's redirects until it sends us back to the callback.
	browser := &http.Client{CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if strings.HasPrefix(r.URL.String(), "http://127.0.0.1:3000/") {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	res, err := browser.Get(to)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	back, err := url.Parse(res.Header.Get("Location"))
	if err != nil || back.Query().Get("code") == "" {
		t.Fatalf("no code: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	r := httptest.NewRequest("GET", "/auth/callback?"+back.RawQuery, nil)
	for _, ck := range w.Result().Cookies() {
		r.AddCookie(ck)
	}
	claims, err := c.Finish(ctx, httptest.NewRecorder(), r)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Provider != "mock" || claims.Subject == "" || claims.Email != "kilgore@kilgore.trout" {
		t.Fatalf("%+v", claims)
	}
}

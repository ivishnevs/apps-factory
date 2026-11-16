package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestAPINamesItsCompany: an API with Company sends it as X-Factory-Company on every kind of request; without one,
// none (a node's company comes from its keys).
func TestAPINamesItsCompany(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get(CompanyHeader))
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	ctx := context.Background()
	for _, company := range []string{"", "acme"} {
		got = nil
		api := API{URL: srv.URL, Token: "t", Company: company}
		var v map[string]any
		if err := api.Do(ctx, "GET", "/x", nil, &v); err != nil {
			t.Fatal(err)
		}
		if err := api.PostRaw(ctx, "/x", "application/octet-stream", []byte("b")); err != nil {
			t.Fatal(err)
		}
		if err := api.Upload(ctx, "/x", "image/png", []byte("b"), &v); err != nil {
			t.Fatal(err)
		}
		body, err := api.Get(ctx, "/x")
		if err != nil {
			t.Fatal(err)
		}
		body.Close()
		for _, h := range got {
			if h != company {
				t.Fatalf("Company %q sent %q", company, got)
			}
		}
		if len(got) != 4 {
			t.Fatalf("requests %d", len(got))
		}
	}
}

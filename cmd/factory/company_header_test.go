package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// TestCLINamesItsCompany: the API commands send the company named by --company or FACTORY_COMPANY as
// X-Factory-Company (an operator token otherwise acts in Core's own company), and none when nothing names one.
func TestCLINamesItsCompany(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("X-Factory-Company"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer srv.Close()
	t.Setenv("FACTORY_URL", srv.URL)
	t.Setenv("FACTORY_TOKEN", "operator")
	args := os.Args
	defer func() { os.Args = args }()
	for _, c := range []struct {
		env  string
		args []string
		want string
	}{
		{"", []string{"nodes", "list"}, ""},
		{"acme", []string{"nodes", "list"}, "acme"},
		{"acme", []string{"--company", "beta", "nodes", "list"}, "beta"},
		{"", []string{"nodes", "list", "--company", "beta"}, "beta"},
	} {
		t.Setenv("FACTORY_COMPANY", c.env)
		got = nil
		os.Args = append([]string{"factory"}, c.args...)
		if err := run(); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		if len(got) != 1 || got[0] != c.want {
			t.Fatalf("FACTORY_COMPANY=%q %v sent company %q, want %q", c.env, c.args, got, c.want)
		}
	}
}

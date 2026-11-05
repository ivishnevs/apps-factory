package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"appfactory/internal/core"
	"appfactory/internal/tui"
	"appfactory/internal/web"
)

const webUsage = "web --demo | --fixture PATH [--listen ADDR]"

// webCommand serves the web UI's API over fixtures (--demo, --fixture) on --listen (default 127.0.0.1:8090), for
// UI development without Core. The live web API is Core's own (saas-web D1): point the Next.js app at Core.
func webCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen", "127.0.0.1:8090", "")
	demo := fs.Bool("demo", false, "")
	fixture := fs.String("fixture", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || *demo == (*fixture != "") {
		return fmt.Errorf("usage: %s (the live web API is served by Core: factory serve)", webUsage)
	}
	f := tui.DemoFixture()
	if *fixture != "" {
		var err error
		if f, err = tui.LoadFixture(*fixture); err != nil {
			return err
		}
	}
	s := &web.Server{Mode: "demo", Client: &tui.FixtureClient{F: f}}
	l, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	fmt.Fprintf(os.Stderr, "factory web (%s) on http://%s\n", s.Mode, l.Addr())
	if err = srv.Serve(l); errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// withWebAPI is Core's handler serving the web UI's API too (saas-web D1): /api/* runs internal/web in this process,
// its calls to Core going to Core's handler in memory with the person's session, CSRF and company. Without sign-in
// the web API acts as the owner only with local (FACTORY_WEB_LOCAL=1, Core on loopback); otherwise it answers 401.
func withWebAPI(c *core.Core, local bool) http.Handler {
	coreHandler := c.Handler()
	s := &web.Server{Mode: "live", Core: "http://core.internal", Transport: web.HandlerTransport{H: coreHandler}}
	webHandler := s.Handler()
	if c.Auth == nil {
		if !local {
			webHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "the web API needs sign-in (FACTORY_OIDC_ISSUER) or FACTORY_WEB_LOCAL=1 on a loopback Core", http.StatusUnauthorized)
			})
		} else {
			s.Token = c.Token
		}
	}
	pages := webPages(os.Getenv("FACTORY_WEB_UPSTREAM"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			webHandler.ServeHTTP(w, r)
			return
		}
		if pages != nil && webPage(r) {
			pages.ServeHTTP(w, r)
			return
		}
		coreHandler.ServeHTTP(w, r)
	})
}

// webPages proxies the web UI's pages to the Next.js server at upstream (FACTORY_WEB_UPSTREAM, e.g. on a private
// network): Core is then the one public address, the browser calls /api and /auth on it directly and the session
// cookie stays first-party. nil without upstream.
func webPages(upstream string) http.Handler {
	if upstream == "" {
		return nil
	}
	u, err := url.Parse(upstream)
	if err != nil || u.Host == "" {
		slog.Error("FACTORY_WEB_UPSTREAM is not a URL; the web UI is not served", "value", upstream)
		return nil
	}
	p := httputil.NewSingleHostReverseProxy(u)
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		slog.Warn("web UI unreachable", "error", err)
		http.Error(w, "the web UI is starting, try again in a moment", http.StatusBadGateway)
	}
	return p
}

// webPage tells the web UI's pages and assets from Core's own routes, which never use these paths.
func webPage(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	p := r.URL.Path
	if p == "/" || p == "/favicon.ico" || p == "/signin" || p == "/welcome" {
		return true
	}
	for _, prefix := range []string{"/c/", "/_next/", "/connect/", "/invite/", "/signin/"} {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	// A browser opening any other address (a link, a typo) gets the web's page or its "not found", not Core's 401;
	// API clients, nodes and the sign-in never ask for HTML on these.
	if strings.Contains(r.Header.Get("Accept"), "text/html") && r.Header.Get("Signature-Input") == "" && r.Header.Get("Authorization") == "" {
		for _, prefix := range []string{"/api/", "/auth/", "/downloads/", "/agent/"} {
			if strings.HasPrefix(p, prefix) {
				return false
			}
		}
		return p != "/install.sh" && p != "/health"
	}
	return false
}

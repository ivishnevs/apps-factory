package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"appfactory/internal/tui"
	"appfactory/internal/web"
)

const webUsage = "web [--listen ADDR] [--demo | --fixture PATH | --sessions]"

// webCommand serves the web UI's API on --listen (default 127.0.0.1:8090) against Core (FACTORY_URL,
// FACTORY_TOKEN), or against a fixture (--demo, --fixture); the Next.js app in web/ proxies to it. The token
// stays in this process; keep it on localhost: whoever reaches it acts as the owner. With --sessions there is no
// token: people sign in on the website and act with their own session and role.
func webCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	listen := fs.String("listen", "127.0.0.1:8090", "")
	demo := fs.Bool("demo", false, "")
	fixture := fs.String("fixture", "", "")
	sessions := fs.Bool("sessions", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 || *demo && *fixture != "" || *sessions && (*demo || *fixture != "") {
		return fmt.Errorf("usage: %s", webUsage)
	}
	s := &web.Server{Mode: "live"}
	switch {
	case *demo || *fixture != "":
		f := tui.DemoFixture()
		if *fixture != "" {
			var err error
			if f, err = tui.LoadFixture(*fixture); err != nil {
				return err
			}
		}
		s.Client, s.Mode = &tui.FixtureClient{F: f}, "demo"
	case *sessions:
		// People sign in on the website (user-accounts): no token here, Core checks each person's session.
		s.Core = env("FACTORY_URL", "http://127.0.0.1:8080")
	default:
		if os.Getenv("FACTORY_TOKEN") == "" {
			return fmt.Errorf("FACTORY_TOKEN required (or factory web --sessions | --demo)")
		}
		s.Core, s.Token = env("FACTORY_URL", "http://127.0.0.1:8080"), os.Getenv("FACTORY_TOKEN")
	}
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

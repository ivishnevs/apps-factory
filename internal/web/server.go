// Package web is the JSON API of the owner's web UI (owner-web) over the same owner client as the terminal UI
// (tui.Client). The Next.js app in web/ proxies /api/* here; the owner token stays in this process and the
// browser never sees it. Bind it to localhost: whoever reaches it acts as the owner.
package web

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"appfactory/internal/messaging"
	"appfactory/internal/tui"
)

// Server serves the API under /api/.
type Server struct {
	// Client is the fixture client of demo mode (Core empty).
	Client tui.Client
	// Mode is "live" or "demo" (shown by the page).
	Mode string
	// Core is Core's URL in live mode; Token the owner token, empty in session mode (access.go).
	Core  string
	Token string

	stateMap
}

// Handler is the whole site.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/meta", func(w http.ResponseWriter, r *http.Request) {
		access := "token"
		switch {
		case s.Core == "":
			access = "demo"
		case s.Token == "":
			access = "session"
		}
		reply(w, map[string]any{"describe": s.state(r).client.Describe(), "mode": s.Mode, "access": access}, nil)
	})
	mux.HandleFunc("GET /api/overview", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.state(r).client.Overview(r.Context())
		reply(w, v, err)
	})
	mux.HandleFunc("GET /api/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		o, err := s.state(r).client.Overview(r.Context())
		if err != nil {
			reply(w, nil, err)
			return
		}
		v, err := s.state(r).client.TaskDetail(r.Context(), r.PathValue("id"), o.Tasks)
		reply(w, v, err)
	})
	mux.HandleFunc("GET /api/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.state(r).client.Run(r.Context(), r.PathValue("id"))
		reply(w, v, err)
	})
	mux.HandleFunc("GET /api/runs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.state(r).client.RunEvents(r.Context(), r.PathValue("id"))
		reply(w, v, err)
	})
	mux.HandleFunc("GET /api/runs/{id}/toolcalls", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.state(r).client.ToolCalls(r.Context(), r.PathValue("id"))
		reply(w, v, err)
	})
	// ?after=N pages forward (Core's GET /feed); ?before=N reads backwards: the newest rows below N, or the
	// newest of all with before=0 (see feedTail).
	mux.HandleFunc("GET /api/feed", func(w http.ResponseWriter, r *http.Request) {
		limit := int(num(r, "limit", 100))
		if !r.URL.Query().Has("before") {
			v, err := s.state(r).client.Feed(r.Context(), feedQuery(r), num(r, "after", 0), limit)
			reply(w, v, err)
			return
		}
		before := num(r, "before", -1)
		if before < 0 || limit < 1 || limit > 500 {
			reply(w, nil, fmt.Errorf("%w: before >= 0 and limit 1..500 required", errInput))
			return
		}
		v, err := s.state(r).tail.read(r.Context(), s.state(r).client, feedQuery(r), before, limit)
		reply(w, v, err)
	})
	mux.HandleFunc("GET /api/messages", func(w http.ResponseWriter, r *http.Request) {
		v, err := s.state(r).client.Messages(r.Context(), r.URL.Query().Get("conversation"))
		if v == nil {
			v = []messaging.Message{} // a conversation that does not exist yet has no messages
		}
		reply(w, v, err)
	})
	mux.HandleFunc("GET /api/costs", func(w http.ResponseWriter, r *http.Request) {
		d, err := time.ParseDuration(cmp.Or(r.URL.Query().Get("since"), "24h"))
		if err != nil {
			reply(w, nil, fmt.Errorf("%w: since", errInput))
			return
		}
		v, err := s.state(r).client.Costs(r.Context(), time.Now().Add(-d))
		reply(w, v, err)
	})
	mux.HandleFunc("GET /api/artifacts/{project}/{artifact}/{version}", func(w http.ResponseWriter, r *http.Request) {
		b, err := s.state(r).client.ArtifactContent(r.Context(), r.PathValue("project"), r.PathValue("artifact"), r.PathValue("version"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		sendContent(w, r, b, cmp.Or(r.URL.Query().Get("name"), r.PathValue("artifact")))
	})
	// Task materials (materials.go): what a task produced, the Library across the company, and the blobs
	// behind report attachments (raw, typed by the file name, sandboxed).
	mux.HandleFunc("GET /api/tasks/{id}/materials", func(w http.ResponseWriter, r *http.Request) {
		d, err := s.state(r).client.TaskDetail(r.Context(), r.PathValue("id"), nil)
		if err != nil {
			reply(w, nil, err)
			return
		}
		v, err := taskMaterials(r.Context(), s.state(r).client, d)
		reply(w, v, err)
	})
	mux.HandleFunc("GET /api/materials", func(w http.ResponseWriter, r *http.Request) {
		limit, before := int(num(r, "limit", 50)), num(r, "before", 0)
		if limit < 1 || limit > 200 || before < 0 {
			reply(w, nil, fmt.Errorf("%w: limit 1..200 and before >= 0 required", errInput))
			return
		}
		all, err := s.state(r).lib.read(r.Context(), s.state(r).client)
		if err != nil {
			reply(w, nil, err)
			return
		}
		q := r.URL.Query()
		reply(w, page(all, q.Get("project"), q.Get("kind"), q.Get("q"), before, limit), nil)
	})
	mux.HandleFunc("GET /api/blobs/{sha256}", func(w http.ResponseWriter, r *http.Request) {
		if !tui.ValidSHA256(r.PathValue("sha256")) {
			reply(w, nil, fmt.Errorf("%w: a SHA-256 digest required", errInput))
			return
		}
		b, err := s.state(r).client.Blob(r.Context(), r.PathValue("sha256"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		sendContent(w, r, b, r.URL.Query().Get("name"))
	})
	mux.HandleFunc("POST /api/action", func(w http.ResponseWriter, r *http.Request) {
		// A plain cross-site form cannot set this header, so a page elsewhere cannot act as the owner.
		if r.Header.Get("X-Factory-Web") != "1" {
			reply(w, nil, fmt.Errorf("%w: X-Factory-Web header required", errInput))
			return
		}
		var a tui.Action
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&a); err != nil || a.Kind == "" {
			reply(w, nil, fmt.Errorf("%w: action JSON with Kind required", errInput))
			return
		}
		out, err := s.state(r).client.Do(r.Context(), a)
		reply(w, map[string]string{"result": out}, err)
	})
	// The company's catalog (company-self-service 6.1): reads and changes pass through to Core's catalog,
	// template and company routes; a demo client has none.
	catalog := func(w http.ResponseWriter, r *http.Request) (tui.CatalogClient, bool) {
		cc, ok := s.state(r).client.(tui.CatalogClient)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotImplemented)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "the catalog needs a live Core"})
		}
		return cc, ok
	}
	for _, p := range []string{"/catalog", "/catalog/teams", "/catalog/history", "/catalog/document", "/templates", "/companies"} {
		mux.HandleFunc("GET /api"+p, func(w http.ResponseWriter, r *http.Request) {
			cc, ok := catalog(w, r)
			if !ok {
				return
			}
			path := p
			if r.URL.RawQuery != "" {
				path += "?" + r.URL.RawQuery
			}
			v, err := cc.CatalogGet(r.Context(), path)
			reply(w, v, err)
		})
	}
	for _, p := range []string{"/catalog", "/catalog/rollback", "/companies"} {
		mux.HandleFunc("POST /api"+p, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Factory-Web") != "1" {
				reply(w, nil, fmt.Errorf("%w: X-Factory-Web header required", errInput))
				return
			}
			cc, ok := catalog(w, r)
			if !ok {
				return
			}
			var body json.RawMessage
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&body); err != nil {
				reply(w, nil, fmt.Errorf("%w: JSON body required", errInput))
				return
			}
			v, err := cc.CatalogPost(r.Context(), p, body)
			reply(w, v, err)
		})
	}
	mux.HandleFunc("GET /api/stream/feed", func(w http.ResponseWriter, r *http.Request) {
		stream(w, r, func(ctx context.Context, out func(tui.StreamEvent)) {
			s.state(r).client.StreamFeed(ctx, feedQuery(r), num(r, "after", 0), out)
		})
	})
	mux.HandleFunc("GET /api/stream/run/{id}", func(w http.ResponseWriter, r *http.Request) {
		stream(w, r, func(ctx context.Context, out func(tui.StreamEvent)) {
			s.state(r).client.StreamRun(ctx, r.PathValue("id"), num(r, "after", 0), int(num(r, "tail", 0)), out)
		})
	})
	// Sign-in and the owner routes the web UI calls as they are (access.go).
	if s.Core != "" {
		mux.Handle("/auth/", s.coreProxy(""))
		mux.Handle("/api/core/", s.coreRoute(s.coreProxy("/api/core")))
	} else {
		mux.Handle("/api/core/", s.coreRoute(nil))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mux.ServeHTTP(w, withForward(r)) })
}

var errInput = errors.New("bad request")

func errInputf(message string) error { return fmt.Errorf("%w: %s", errInput, message) }

// reply writes v as JSON, or the error as {"error": ...} (400 for bad input, 502 for Core's refusals).
func reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, errInput) {
			code = http.StatusBadRequest
		}
		w.WriteHeader(code)
		v = map[string]string{"error": err.Error()}
	}
	_ = json.NewEncoder(w).Encode(v)
}

// stream relays a client stream as server-sent events: "feed" (a FeedItem), "run" (a RunEvent), "end"
// ({"outcome"}) and "state" ({"connected","err"}).
func stream(w http.ResponseWriter, r *http.Request, run func(context.Context, func(tui.StreamEvent))) {
	f, ok := w.(http.Flusher)
	if !ok {
		reply(w, nil, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	// no-transform: a proxy (Next's rewrites) must not compress the stream, which would hold events back.
	w.Header().Set("Cache-Control", "no-store, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	events := make(chan tui.StreamEvent, 64)
	go func() {
		defer close(events)
		run(ctx, func(e tui.StreamEvent) {
			select {
			case events <- e:
			case <-ctx.Done():
			}
		})
	}()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	send := func(kind string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, b)
		f.Flush()
	}
	w.WriteHeader(http.StatusOK)
	f.Flush() // the client's own state event says when Core's stream is connected
	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			f.Flush()
		case e, ok := <-events:
			switch {
			case !ok:
				return
			case e.State:
				send("state", map[string]any{"connected": e.Connected, "err": e.Err})
			case e.Feed != nil:
				send("feed", e.Feed)
			case e.Run != nil:
				send("run", e.Run)
			case e.End:
				send("end", map[string]string{"outcome": e.Outcome})
				return
			}
		}
	}
}

func feedQuery(r *http.Request) tui.FeedQuery {
	q := r.URL.Query()
	return tui.FeedQuery{Project: q.Get("project"), Task: q.Get("task"), Subtree: q.Get("subtree"), Attention: q.Get("attention") == "1"}
}

func num(r *http.Request, k string, d int64) int64 {
	if n, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get(k)), 10, 64); err == nil {
		return n
	}
	return d
}

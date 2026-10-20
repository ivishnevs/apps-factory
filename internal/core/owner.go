package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"appfactory/internal/storage"
)

// killSwitch pauses the company (nodes stop claiming) and revokes every live run; seals complete on the nodes.
func (c *Core) killSwitch(ctx context.Context, company, reason string) (int, error) {
	if err := c.Store.SetPause(ctx, company, storage.Pause{Scope: "company", Reason: "stop: " + reason, Actor: "owner"}); err != nil {
		return 0, err
	}
	return c.Store.RevokeLive(ctx, company, "owner")
}

// Owner controls (waits-events-cost-cli 5.2 and 2.2): pauses, kill switch, pending count and a feed
// stream. Owner token only; agents have no route to them.
func (c *Core) ownerRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /pauses", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Pauses(r.Context(), c.companyFrom(r))
		reply(w, v, err)
	})
	m.HandleFunc("POST /pauses", func(w http.ResponseWriter, r *http.Request) {
		var p storage.Pause
		if !decode(w, r, &p) {
			return
		}
		p.Actor, p.CreatedAt = "owner", 0
		reply(w, map[string]bool{"ok": true}, c.Store.SetPause(r.Context(), c.companyFrom(r), p))
	})
	m.HandleFunc("POST /pauses/resume", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Scope  string `json:"scope"`
			Target string `json:"target,omitempty"`
		}
		if !decode(w, r, &p) {
			return
		}
		reply(w, map[string]bool{"ok": true}, c.Store.ClearPause(r.Context(), c.companyFrom(r), p.Scope, p.Target, "owner"))
	})
	// Kill switch: pause the company, then revoke every live run; tasks return to READY.
	m.HandleFunc("POST /stop", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Reason string `json:"reason,omitempty"`
		}
		if !decode(w, r, &p) {
			return
		}
		n, err := c.killSwitch(r.Context(), c.companyFrom(r), p.Reason)
		reply(w, map[string]int{"revoked": n}, err)
	})
	m.HandleFunc("GET /escalations", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Escalations(r.Context(), c.companyFrom(r), r.URL.Query().Get("all") == "")
		reply(w, v, err)
	})
	m.HandleFunc("GET /pending/count", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.PendingCounts(r.Context(), c.companyFrom(r))
		reply(w, v, err)
	})
	// Server-sent events over the feed table: `id` is the feed sequence, so a client reconnecting with
	// Last-Event-ID (or ?after=) receives every later row once.
	m.HandleFunc("GET /feed/stream", func(w http.ResponseWriter, r *http.Request) {
		f := c.feedFilter(r.URL.Query())
		after, ok := sseCursor(w, r)
		if !ok {
			return
		}
		company := c.companyFrom(r)
		serveSSE(w, r, func(ctx context.Context) ([]sseEvent, bool, bool, error) {
			page, err := c.Store.Feed(ctx, company, f, after, 100)
			if err != nil {
				return nil, false, false, err
			}
			out := make([]sseEvent, 0, len(page.Items))
			for _, item := range page.Items {
				b, _ := json.Marshal(item)
				out = append(out, sseEvent{ID: item.Sequence, Name: "feed", Data: b})
			}
			after = page.NextAfter
			return out, page.HasMore, false, nil
		})
	})
	m.HandleFunc("GET /runs/{id}/events/stream", c.runEventStream)
}

// runEventStream serves a run's events as server-sent events (the owner UI's live run view): `id` is the
// event sequence, resumed with Last-Event-ID or ?after=; ?tail=N (1..1000) starts at the last N events. After
// the ended run's last event the stream sends `event: end` with the run's status and outcome, and closes.
func (c *Core) runEventStream(w http.ResponseWriter, r *http.Request) {
	company, id := c.companyFrom(r), r.PathValue("id")
	if _, err := c.Store.Get(r.Context(), company, id); err != nil {
		reply(w, nil, err)
		return
	}
	after, ok := sseCursor(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if tail := q.Get("tail"); tail != "" && r.Header.Get("Last-Event-ID") == "" && q.Get("after") == "" {
		var n int
		if _, err := fmt.Sscan(tail, &n); err != nil || n < 1 || n > 1000 {
			http.Error(w, "tail 1..1000", 400)
			return
		}
		var err error
		if after, err = c.Store.RunEventTail(r.Context(), company, id, n); err != nil {
			reply(w, nil, err)
			return
		}
	}
	const batch = 100
	serveSSE(w, r, func(ctx context.Context) ([]sseEvent, bool, bool, error) {
		// The phase is read before the events: once a run is ENDED all its events are stored.
		run, err := c.Store.Get(ctx, company, id)
		if err != nil {
			return nil, false, false, err
		}
		events, err := c.Store.RunEventsAfter(ctx, company, id, after, batch)
		if err != nil {
			return nil, false, false, err
		}
		out := make([]sseEvent, 0, len(events)+1)
		for _, e := range events {
			// The worker's lease is a credential: the stream leaves it out.
			var p map[string]json.RawMessage
			if json.Unmarshal(e.Envelope.Payload, &p) == nil {
				delete(p, "lease")
				e.Envelope.Payload, _ = json.Marshal(p)
			}
			b, _ := json.Marshal(e.Envelope)
			out = append(out, sseEvent{ID: e.Seq, Name: "run", Data: b})
			after = e.Seq
		}
		more := len(events) == batch
		if !more && run.Status == "ENDED" {
			b, _ := json.Marshal(map[string]string{"status": run.Status, "outcome": run.Outcome})
			return append(out, sseEvent{Name: "end", Data: b}), false, true, nil
		}
		return out, more, false, nil
	})
}

// sseCursor reads a stream's resume cursor: Last-Event-ID, else ?after=, else 0.
func sseCursor(w http.ResponseWriter, r *http.Request) (int64, bool) {
	var after int64
	cursor := r.Header.Get("Last-Event-ID")
	if cursor == "" {
		cursor = r.URL.Query().Get("after")
	}
	if cursor != "" {
		if _, err := fmt.Sscan(cursor, &after); err != nil || after < 0 {
			http.Error(w, "invalid cursor", 400)
			return 0, false
		}
	}
	return after, true
}

// sseEvent is one server-sent event; ID 0 writes no id line (the cursor stays).
type sseEvent struct {
	ID   int64
	Name string
	Data []byte
}

// serveSSE writes what next returns as server-sent events until the request ends, next fails or reports
// done. Without more to read it polls next every second and sends a keepalive comment every 15 s of silence.
func serveSSE(w http.ResponseWriter, r *http.Request, next func(context.Context) (events []sseEvent, more, done bool, err error)) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // the stream outlives the server's write timeout
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	lastWrite := time.Time{}
	for {
		events, more, done, err := next(r.Context())
		if err != nil {
			return
		}
		for _, e := range events {
			id := ""
			if e.ID > 0 {
				id = fmt.Sprintf("id: %d\n", e.ID)
			}
			if _, err = fmt.Fprintf(w, "%sevent: %s\ndata: %s\n\n", id, e.Name, e.Data); err != nil {
				return
			}
		}
		if len(events) > 0 {
			lastWrite = time.Now()
		} else if time.Since(lastWrite) >= 15*time.Second {
			if _, err = fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			lastWrite = time.Now()
		}
		if err = rc.Flush(); err != nil || done {
			return
		}
		if more {
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(time.Second):
		}
	}
}

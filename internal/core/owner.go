package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"appfactory/internal/storage"
)

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
		company := c.companyFrom(r)
		if err := c.Store.SetPause(r.Context(), company, storage.Pause{Scope: "company", Reason: "stop: " + p.Reason, Actor: "owner"}); err != nil {
			reply(w, nil, err)
			return
		}
		n, err := c.Store.RevokeLive(r.Context(), company, "owner")
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
		q := r.URL.Query()
		f := storage.FeedFilter{Project: q.Get("project"), Task: q.Get("task"), Attention: q.Get("attention") == "1" || q.Get("attention") == "true"}
		var after int64
		cursor := r.Header.Get("Last-Event-ID")
		if cursor == "" {
			cursor = q.Get("after")
		}
		if cursor != "" {
			if _, err := fmt.Sscan(cursor, &after); err != nil || after < 0 {
				http.Error(w, "invalid cursor", 400)
				return
			}
		}
		rc := http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Time{}) // the stream outlives the server's write timeout
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		company := c.companyFrom(r)
		lastWrite := time.Time{}
		for {
			page, err := c.Store.Feed(r.Context(), company, f, after, 100)
			if err != nil {
				return
			}
			for _, item := range page.Items {
				b, _ := json.Marshal(item)
				if _, err = fmt.Fprintf(w, "id: %d\nevent: feed\ndata: %s\n\n", item.Sequence, b); err != nil {
					return
				}
			}
			after = page.NextAfter
			if len(page.Items) > 0 {
				lastWrite = time.Now()
			} else if time.Since(lastWrite) >= 15*time.Second {
				if _, err = fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return
				}
				lastWrite = time.Now()
			}
			if err = rc.Flush(); err != nil {
				return
			}
			if page.HasMore {
				continue
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Second):
			}
		}
	})
}

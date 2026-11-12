package core

import (
	"log/slog"
	"net/http"
)

// The status note and the support address (saas-release-readiness 2.4, 4.3) reach everyone: the public
// /auth/providers answer (the landing and the sign-in read it) and GET /me (the app) carry them. Only the operator
// sets the note: PUT /status-note {"note": "..."}, an empty note clears it (docs/incident-runbook.md).

func (c *Core) statusNoteRoutes(m *http.ServeMux) {
	m.HandleFunc("PUT /status-note", func(w http.ResponseWriter, r *http.Request) {
		if !operator(r) {
			jsonStatus(w, 403, map[string]string{"error": "the operator sets the status note", "reason": "forbidden"})
			return
		}
		var p struct {
			Note string `json:"note"`
		}
		if !decode(w, r, &p) {
			return
		}
		note, err := c.Store.SetStatusNote(r.Context(), p.Note)
		if err == nil {
			slog.Info("status note", "note", note)
		}
		reply(w, map[string]string{"note": note}, err)
	})
}

// statusNote is the current note, "" when none is set or it cannot be read.
func (c *Core) statusNote(r *http.Request) string {
	if c.Store == nil {
		return ""
	}
	note, err := c.Store.StatusNote(r.Context())
	if err != nil {
		slog.Warn("status note", "error", err)
	}
	return note
}

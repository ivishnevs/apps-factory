package core

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"appfactory/internal/storage"
)

// Team invitations (team-invitations): owners create single-use links for the admin or member role, list the open
// ones and revoke them; the link /invite/<token> shows what it invites to (GET /auth/invitations/<token>, public)
// and a signed-in person accepts it (POST /me/invitations/<token>). A person who signs up from a link joins that
// company instead of getting their own.

// invitePrefix is the web page of an invitation link.
const invitePrefix = "/invite/"

func (c *Core) invitationRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /invitations", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Invitations(r.Context(), c.companyFrom(r))
		reply(w, v, err)
	})
	m.HandleFunc("POST /invitations", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Role string `json:"role"`
		}
		if !decode(w, r, &p) {
			return
		}
		if p.Role == "" {
			p.Role = storage.RoleMember
		}
		inv, token, err := c.Store.CreateInvitation(r.Context(), c.companyFrom(r), p.Role, author(r))
		if err != nil {
			reply(w, nil, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
		write(w, map[string]any{"invitation": inv, "token": token, "url": strings.TrimRight(c.PublicURL, "/") + invitePrefix + token})
	})
	m.HandleFunc("DELETE /invitations/{id}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, c.Store.RevokeInvitation(r.Context(), c.companyFrom(r), r.PathValue("id")))
	})
}

// previewInvitation is GET /auth/invitations/<token>: the company, role and inviter of a link, open or not.
func (c *Core) previewInvitation(w http.ResponseWriter, r *http.Request, token string) {
	p, err := c.Store.PreviewInvitation(r.Context(), token)
	reply(w, p, err)
}

// acceptInvitation is POST /me/invitations/<token> with a session.
func (c *Core) acceptInvitation(w http.ResponseWriter, r *http.Request, user, token string) {
	m, err := c.Store.AcceptInvitation(r.Context(), token, user)
	reply(w, m, err)
}

// inviteToken is the token of an invitation page path, "" for any other path.
func inviteToken(path string) string {
	t, ok := strings.CutPrefix(path, invitePrefix)
	if !ok || t == "" || strings.ContainsAny(t, "/?#") {
		return ""
	}
	return t
}

// joinOnSignUp finishes the sign-up of a person who came from an open invitation: they join that company (their
// first company, so the setup wizard is not theirs to see). It returns where to go, "" when the link could not be
// used (the caller then gives the person a company of their own).
func (c *Core) joinOnSignUp(ctx context.Context, user, token string) string {
	m, err := c.Store.AcceptInvitation(ctx, token, user)
	if err != nil {
		if !errors.Is(err, storage.ErrInvitationClosed) && !errors.Is(err, storage.ErrNotFound) {
			slog.Error("invitation at sign-up", "error", err)
		}
		return ""
	}
	if err = c.Store.MarkWelcomed(ctx, user); err != nil {
		slog.Warn("welcomed", "error", err)
	}
	return "/c/" + m.Company
}

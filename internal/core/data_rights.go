package core

import (
	"archive/zip"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"appfactory/internal/notify/telegram"
	"appfactory/internal/storage"
)

// Data rights (saas-release-readiness 4.2): a company's owner exports or deletes the company with all its data; a
// signed-in person exports or deletes their account (the companies of their account with it). Deleting needs the
// explicit confirmation the page asks for: the company's id, or the person's email. Only a person who signed in
// (or the operator) does either: an owner token acts as an admin and never exports or deletes a company.

// DeleteConfirm is the body of DELETE /company and DELETE /me.
type DeleteConfirm struct {
	Confirm string `json:"confirm"`
}

// dataOwner reports whether the request may export or delete its company: a signed-in owner, or the operator.
func dataOwner(r *http.Request) bool {
	p := principalOf(r)
	return operator(r) || (p.Kind == "session" && p.Role == storage.RoleOwner)
}

func (c *Core) dataRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /company/export", func(w http.ResponseWriter, r *http.Request) {
		if !dataOwner(r) {
			jsonStatus(w, 403, map[string]string{"error": "only an owner who signed in exports the company", "reason": "forbidden"})
			return
		}
		company := c.companyFrom(r)
		c.streamExport(w, r, company+"-export", func(zw *zip.Writer) error { return c.Store.ExportCompany(r.Context(), company, zw, "") })
	})
	m.HandleFunc("DELETE /company", func(w http.ResponseWriter, r *http.Request) {
		if !dataOwner(r) {
			jsonStatus(w, 403, map[string]string{"error": "only an owner who signed in deletes the company", "reason": "forbidden"})
			return
		}
		var p DeleteConfirm
		if !decode(w, r, &p) {
			return
		}
		company := c.companyFrom(r)
		if strings.TrimSpace(p.Confirm) != company {
			jsonStatus(w, 400, map[string]string{"error": "type the company's id (" + company + ") to confirm", "reason": "confirm"})
			return
		}
		if company == c.company() {
			jsonStatus(w, 400, map[string]string{"error": "this Core serves " + company + " itself; it is not deleted", "reason": "core_company"})
			return
		}
		bot, botErr := c.Store.TelegramBotOf(r.Context(), company)
		if err := c.Store.DeleteCompany(r.Context(), company); err != nil {
			reply(w, nil, err)
			return
		}
		c.companyDeleted(r, company, bot, botErr == nil)
		slog.Info("company deleted", "company", company, "by", author(r))
		write(w, map[string]any{"deleted": company})
	})
}

// companyDeleted forgets what Core keeps of a deleted company in memory and stops its Telegram bot.
func (c *Core) companyDeleted(r *http.Request, company string, bot storage.TelegramBot, hasBot bool) {
	c.catalogMu.Lock()
	delete(c.catalogs, company)
	c.catalogMu.Unlock()
	if hasBot {
		if err := (telegram.API{BaseURL: c.Telegram.APIBase, Token: bot.Token}).DeleteWebhook(r.Context()); err != nil {
			slog.Warn("telegram webhook removal", "company", company, "error", err)
		}
		c.kickTelegram()
	}
}

// streamExport sends the zip archive fill writes as a download named name-<date>.zip.
func (c *Core) streamExport(w http.ResponseWriter, r *http.Request, name string, fill func(*zip.Writer) error) {
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.zip"`, name, c.Store.Now().UTC().Format("20060102")))
	h.Set("Cache-Control", "no-store")
	ew := &exportWriter{w: w}
	zw := zip.NewWriter(ew)
	err := fill(zw)
	if err == nil {
		err = zw.Close()
	}
	if err == nil {
		return
	}
	if !ew.wrote {
		h.Del("Content-Disposition")
		h.Del("Content-Type")
		reply(w, nil, err)
		return
	}
	// The archive is cut short; the person sees a broken download and tries again.
	slog.Error("export", "name", name, "error", err)
}

// exportWriter notes whether anything reached the response.
type exportWriter struct {
	w     http.ResponseWriter
	wrote bool
}

func (e *exportWriter) Write(b []byte) (int, error) {
	if len(b) > 0 {
		e.wrote = true
	}
	return e.w.Write(b)
}

// exportAccount is GET /me/export for a signed-in person.
func (c *Core) exportAccount(w http.ResponseWriter, r *http.Request, user string) {
	c.streamExport(w, r, "app-factory-account", func(zw *zip.Writer) error { return c.Store.ExportAccount(r.Context(), user, zw) })
}

// deleteAccount is DELETE /me: the person's account, the companies of the account with all their data, and the
// person's memberships elsewhere; the session ends with it.
func (c *Core) deleteAccount(w http.ResponseWriter, r *http.Request, user string) {
	var p DeleteConfirm
	if !decode(w, r, &p) {
		return
	}
	u, err := c.Store.User(r.Context(), user)
	if err != nil {
		reply(w, nil, err)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(p.Confirm), u.Email) {
		jsonStatus(w, 400, map[string]string{"error": "type your email (" + u.Email + ") to confirm", "reason": "confirm"})
		return
	}
	ms, err := c.Store.Memberships(r.Context(), user)
	if err != nil {
		reply(w, nil, err)
		return
	}
	// The account's companies go with it: Core's own company never does, and their bots are stopped after.
	bots := map[string]storage.TelegramBot{}
	for _, m := range ms {
		account, err := c.Store.AccountOf(r.Context(), m.Company)
		if err != nil || account != u.Account || u.Account == storage.DefaultAccount {
			continue
		}
		if m.Company == c.company() {
			jsonStatus(w, 400, map[string]string{"error": "this Core serves " + m.Company + " itself; the operator removes this account", "reason": "core_company"})
			return
		}
		if b, err := c.Store.TelegramBotOf(r.Context(), m.Company); err == nil {
			bots[m.Company] = b
		}
	}
	out, err := c.Store.DeleteAccount(r.Context(), user)
	if errors.Is(err, storage.ErrSubscriptionActive) {
		jsonStatus(w, 409, map[string]string{"error": err.Error(), "reason": "subscription_active"})
		return
	}
	if err != nil {
		reply(w, nil, err)
		return
	}
	for _, company := range out.Companies {
		b, ok := bots[company]
		c.companyDeleted(r, company, b, ok)
	}
	slog.Info("account deleted", "user", user, "companies", len(out.Companies))
	clearSessionCookies(w, c.secureCookies())
	write(w, out)
}

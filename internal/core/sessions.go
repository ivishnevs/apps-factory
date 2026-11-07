package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"appfactory/internal/auth"
	"appfactory/internal/storage"
)

// People sign in on the website (user-accounts D3–D7): /auth/* runs the OIDC sign-in through Auth, a web
// session is a cookie Core keeps server-side, and every request of a session names its company
// (X-Factory-Company) and is served with the user's role there.

const (
	// SessionCookie carries the web session id (HttpOnly).
	SessionCookie = "factory_session"
	// CSRFCookie is the readable twin of the X-Factory-CSRF header every state-changing session request sends.
	CSRFCookie = "factory_csrf"
	// CompanyHeader names the request's company (D6).
	CompanyHeader = "X-Factory-Company"
	// CSRFHeader must equal the CSRF cookie on state-changing session requests.
	CSRFHeader = "X-Factory-CSRF"
)

// principal is who a request acts as.
type principal struct {
	// Kind is "operator", "token" (company owner token), "session" or "" (runs and nodes).
	Kind string
	// User is the signed-in user (sessions).
	User string
	// Role is the company role the request has: sessions their membership's, owner tokens admin (issued by
	// owners and admins), the operator owner.
	Role string
	// Token is the owner token's id.
	Token string
}

type principalKey struct{}

func principalOf(r *http.Request) principal {
	p, _ := r.Context().Value(principalKey{}).(principal)
	return p
}

// author names who did something in records (catalog versions, owner tokens): `user:<id>` for a signed-in
// user, `owner` for the owner's CLI token and the operator's CLI as before.
func author(r *http.Request) string {
	if p := principalOf(r); p.Kind == "session" {
		return "user:" + p.User
	}
	if operator(r) {
		return "operator"
	}
	return "owner"
}

// roleRank orders the roles; a route's required role is met by an equal or higher one.
func roleRank(role string) int {
	switch role {
	case storage.RoleOwner:
		return 3
	case storage.RoleAdmin:
		return 2
	case storage.RoleMember:
		return 1
	}
	return 0
}

// requiredRole is the company role an owner route needs (D7). Owners: renaming the company and roles. Owners and
// admins: the catalog (teams, agents, profiles, policies, prompts), skills, secrets, nodes, projects (creating
// and changing them), owner tokens, company settings and the kill switch. Members: everything else — tasks,
// chats, approvals and the projects' work — and every read except owner tokens.
func requiredRole(method, path string) string {
	read := method == http.MethodGet || method == http.MethodHead
	under := func(prefix string) bool { return path == prefix || strings.HasPrefix(path, prefix+"/") }
	switch {
	case path == "/company" && !read, under("/memberships") && !read, under("/invitations"):
		return storage.RoleOwner
	case under("/owner-tokens"), under("/node-enrollments"), under("/node-keys"), under("/node-connects"):
		return storage.RoleAdmin
	case read:
		return storage.RoleMember
	case under("/catalog"), under("/secrets"), under("/skills"), under("/nodes"), under("/node-keys"), under("/node-enrollments"), under("/settings"),
		under("/presets"), under("/setup"), under("/telegram/bot"), path == "/telegram/bind", path == "/stop", path == "/projects", path == "/tasks/review-fill",
		method == http.MethodPatch && projectPath.MatchString(path):
		return storage.RoleAdmin
	}
	return storage.RoleMember
}

var projectPath = regexp.MustCompile(`^/projects/[^/]+$`)

// nodeProtocol are the routes of nodes and runs' executors; a person's session never reaches them.
func nodeProtocol(method, path string) bool {
	if method == http.MethodGet {
		return false
	}
	switch {
	case path == "/nodes/register", path == "/nodes/enroll", strings.HasPrefix(path, "/nodes/") && (strings.HasSuffix(path, "/heartbeat") || strings.HasSuffix(path, "/keys")):
		return true
	case strings.HasPrefix(path, "/runs/"):
		for _, s := range []string{"/claim", "/start", "/credential", "/heartbeat", "/workspace", "/project-setup", "/logs"} {
			if strings.HasSuffix(path, s) {
				return true
			}
		}
	}
	return false
}

func sessionCookie(r *http.Request) string {
	if ck, err := r.Cookie(SessionCookie); err == nil {
		return ck.Value
	}
	return ""
}

// sessionGateway serves a request carrying a session cookie: /me and creating a company need no company; every
// other route needs X-Factory-Company naming a company of the user's memberships (404 otherwise, D6), the role
// the route requires (403) and, when it changes state, the CSRF header (D5).
func (c *Core) sessionGateway(w http.ResponseWriter, r *http.Request, m *http.ServeMux, id string) {
	sess, err := c.Store.AuthenticateSession(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		clearSessionCookies(w, c.secureCookies())
		jsonStatus(w, 401, map[string]string{"error": "session expired", "reason": "session_expired"})
		return
	}
	if err != nil {
		reply(w, nil, err)
		return
	}
	if !safeMethod(r.Method) && !csrfOK(r) {
		jsonStatus(w, 403, map[string]string{"error": "CSRF header required", "reason": "csrf"})
		return
	}
	p := principal{Kind: "session", User: sess.User}
	ctx := context.WithValue(r.Context(), principalKey{}, p)
	switch {
	case r.URL.Path == "/me":
		c.me(w, r.WithContext(ctx))
		return
	case r.URL.Path == "/me/welcomed" && r.Method == http.MethodPost:
		reply(w, map[string]bool{"ok": true}, c.Store.MarkWelcomed(r.Context(), sess.User))
		return
	case r.URL.Path == "/companies" && r.Method == http.MethodPost:
		c.createUserCompany(w, r.WithContext(ctx), sess.User)
		return
	case strings.HasPrefix(r.URL.Path, "/me/invitations/") && r.Method == http.MethodPost:
		c.acceptInvitation(w, r.WithContext(ctx), sess.User, strings.TrimPrefix(r.URL.Path, "/me/invitations/"))
		return
	case operatorOnly(r.URL.Path), nodeProtocol(r.Method, r.URL.Path):
		http.Error(w, "not available to a signed-in user", 403)
		return
	}
	company := r.Header.Get(CompanyHeader)
	if company == "" {
		http.Error(w, CompanyHeader+" required", 400)
		return
	}
	role, err := c.Store.Role(r.Context(), sess.User, company)
	if errors.Is(err, storage.ErrNotFound) {
		http.Error(w, "not found", 404)
		return
	}
	if err != nil {
		reply(w, nil, err)
		return
	}
	if need := requiredRole(r.Method, r.URL.Path); roleRank(role) < roleRank(need) {
		jsonStatus(w, 403, map[string]string{"error": "this needs the " + need + " role in " + company, "reason": "forbidden", "role": role})
		return
	}
	if err = c.Store.SetLastCompany(r.Context(), sess.User, company); err != nil {
		slog.Warn("last company", "error", err)
	}
	p.Role = role
	ctx = context.WithValue(context.WithValue(ctx, principalKey{}, p), companyKey{}, company)
	r = r.WithContext(ctx)
	if role == storage.RoleMember && secretsPath(r.URL.Path) {
		n := &namesOnly{ResponseWriter: w}
		m.ServeHTTP(n, r)
		n.finish()
		return
	}
	m.ServeHTTP(w, r)
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func csrfOK(r *http.Request) bool {
	ck, err := r.Cookie(CSRFCookie)
	h := r.Header.Get(CSRFHeader)
	return err == nil && ck.Value != "" && subtle.ConstantTimeCompare([]byte(ck.Value), []byte(h)) == 1
}

func secretsPath(p string) bool { return p == "/secrets" || strings.HasPrefix(p, "/secrets/") }

// namesOnly is a member's view of the secrets routes (D7, core-secret-store): only names reach the browser,
// whatever else the route answers (kinds, versions, authors, used_by).
type namesOnly struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
	done   bool
}

func (n *namesOnly) WriteHeader(status int) { n.status = status }
func (n *namesOnly) Write(b []byte) (int, error) {
	if n.status == 0 {
		n.status = 200
	}
	return n.buf.Write(b)
}

func (n *namesOnly) finish() {
	if n.done {
		return
	}
	n.done = true
	body := n.buf.Bytes()
	if n.status == 200 {
		var v any
		if json.Unmarshal(body, &v) == nil {
			body, _ = json.Marshal(secretNames(v))
			body = append(body, '\n')
			n.ResponseWriter.Header().Set("Content-Type", "application/json")
		} else {
			body = []byte("{}\n") // nothing but names may pass
		}
		n.ResponseWriter.Header().Del("Content-Length")
	}
	n.ResponseWriter.WriteHeader(n.status)
	_, _ = n.ResponseWriter.Write(body)
}

// secretNames keeps the names of secrets in v: objects with a name become {"name"}, arrays are filtered,
// other objects keep only their arrays (lists of secrets) and plain counts.
func secretNames(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, 0, len(t))
		for _, e := range t {
			out = append(out, secretNames(e))
		}
		return out
	case map[string]any:
		if name, ok := t["name"]; ok {
			return map[string]any{"name": name}
		}
		out := map[string]any{}
		for k, e := range t {
			switch e.(type) {
			case []any, map[string]any:
				out[k] = secretNames(e)
			case float64, bool:
				out[k] = e
			}
		}
		return out
	case float64, bool, nil:
		return t
	}
	return nil
}

// Me is GET /me: who is signed in and the companies they may open.
type Me struct {
	Kind         string               `json:"kind"`
	User         *storage.User        `json:"user,omitempty"`
	Companies    []storage.Membership `json:"companies"`
	LastCompany  string               `json:"last_company,omitempty"`
	MultiCompany bool                 `json:"multi_company"`
}

func (c *Core) me(w http.ResponseWriter, r *http.Request) {
	p := principalOf(r)
	out := Me{Kind: p.Kind, MultiCompany: c.MultiCompany, Companies: []storage.Membership{}}
	if out.Kind == "" && operator(r) {
		out.Kind = "operator"
	}
	if p.Kind == "session" {
		u, err := c.Store.User(r.Context(), p.User)
		if err != nil {
			reply(w, nil, err)
			return
		}
		out.User, out.LastCompany = &u, u.LastCompany
		if out.Companies, err = c.Store.Memberships(r.Context(), p.User); err != nil {
			reply(w, nil, err)
			return
		}
		write(w, out)
		return
	}
	company := c.companyFrom(r)
	name, err := c.Store.CompanyName(r.Context(), company)
	if err != nil {
		reply(w, nil, err)
		return
	}
	role := p.Role
	if role == "" {
		role = storage.RoleOwner
	}
	out.Companies, out.LastCompany = []storage.Membership{{Company: company, Name: name, Role: role}}, company
	write(w, out)
}

// secureCookies reports whether session cookies are Secure (D5); off only for a plain-http development setup.
func (c *Core) secureCookies() bool { return c.Auth == nil || c.Auth.Secure() }

func clearSessionCookies(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: CSRFCookie, Value: "", Path: "/", MaxAge: -1, Secure: secure, SameSite: http.SameSiteLaxMode})
}

func setSessionCookies(w http.ResponseWriter, id string, expires time.Time, secure bool) {
	csrf := make([]byte, 16)
	_, _ = rand.Read(csrf)
	maxAge := int(storage.SessionAbsolute / time.Second)
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: id, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: CSRFCookie, Value: hex.EncodeToString(csrf), Path: "/", MaxAge: maxAge, Secure: secure, SameSite: http.SameSiteLaxMode})
}

// authRoutes are the public sign-in routes (no credential): providers, login, callback and logout.
func (c *Core) authRoutes(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/auth/providers" && r.Method == http.MethodGet:
		providers := []string{}
		if c.Auth != nil {
			providers = c.Auth.Providers()
		}
		write(w, map[string]any{"providers": providers, "multi_company": c.MultiCompany})
	case r.URL.Path == "/auth/login" && r.Method == http.MethodGet:
		if c.Auth == nil {
			http.Error(w, "sign-in is not configured (FACTORY_OIDC_ISSUER)", 404)
			return
		}
		to, err := c.Auth.Begin(r.Context(), w, r.URL.Query().Get("provider"), r.URL.Query().Get("return"))
		if errors.Is(err, auth.ErrUnknownProvider) {
			http.Error(w, err.Error(), 400)
			return
		}
		if err != nil {
			slog.Error("sign-in start", "error", err)
			http.Redirect(w, r, "/signin?error=unavailable", http.StatusFound)
			return
		}
		http.Redirect(w, r, to, http.StatusFound)
	case strings.HasPrefix(r.URL.Path, "/auth/invitations/") && r.Method == http.MethodGet:
		c.previewInvitation(w, r, strings.TrimPrefix(r.URL.Path, "/auth/invitations/"))
	case r.URL.Path == "/auth/callback" && r.Method == http.MethodGet:
		c.callback(w, r)
	case r.URL.Path == "/auth/logout" && r.Method == http.MethodPost:
		if id := sessionCookie(r); id != "" {
			if !csrfOK(r) {
				http.Error(w, "CSRF header required", 403)
				return
			}
			if err := c.Store.RevokeSession(r.Context(), id); err != nil {
				reply(w, nil, err)
				return
			}
		}
		clearSessionCookies(w, c.secureCookies())
		write(w, map[string]bool{"signed_out": true})
	default:
		http.NotFound(w, r)
	}
}

// callback finishes a sign-in: the user (created on first sign-in with their company, D1), a new session and
// a redirect to the page the sign-in started from, else the user's company (a new company opens its setup).
func (c *Core) callback(w http.ResponseWriter, r *http.Request) {
	if c.Auth == nil {
		http.NotFound(w, r)
		return
	}
	fail := func(reason string, err error) {
		slog.Warn("sign-in refused", "reason", reason, "error", err)
		http.Redirect(w, r, "/signin?error="+reason, http.StatusFound)
	}
	claims, err := c.Auth.Finish(r.Context(), w, r)
	if errors.Is(err, auth.ErrState) {
		fail("state", err)
		return
	}
	if err != nil {
		fail("provider", err)
		return
	}
	in := storage.Identity{Provider: claims.Provider, Subject: claims.Subject, Email: claims.Email, EmailVerified: claims.EmailVerified, Name: claims.Name}
	// Signed up from an open invitation link: no company of their own, they join the invitation's.
	token := inviteToken(claims.Return)
	if token != "" {
		if p, err := c.Store.PreviewInvitation(r.Context(), token); err != nil || !p.Open {
			token = ""
		}
	}
	var res storage.SignInResult
	for range 3 { // a company id taken by someone else: another suffix
		nc := newCompany(claims.Name, claims.Email)
		if token != "" {
			nc = storage.NewCompany{}
		}
		res, err = c.Store.SignIn(r.Context(), in, nc)
		if !errors.Is(err, storage.ErrConflict) || !strings.Contains(err.Error(), "companies") {
			break
		}
	}
	if errors.Is(err, storage.ErrConflict) {
		fail("email_unverified", err)
		return
	}
	if err != nil {
		fail("account", err)
		return
	}
	target := claims.Return
	if res.Created && res.Company == "" {
		// Joined through the invitation; a link used up meanwhile leaves them a company of their own.
		if target = c.joinOnSignUp(r.Context(), res.User.ID, token); target == "" {
			if res.Company, err = c.ownCompany(r.Context(), res.User.ID, claims.Name, claims.Email); err != nil {
				fail("account", err)
				return
			}
		}
	}
	if res.Created && res.Company != "" {
		if err = c.provisionCompany(r.Context(), res.Company, "user:"+res.User.ID); err != nil {
			slog.Error("company at sign-up", "company", res.Company, "error", err)
		}
		target = "/c/" + res.Company + "/setup"
	} else if lc := res.User.LastCompany; lc != "" && c.catalogVersion(lc) == 0 {
		// A company whose catalog was not written at sign-up gets it now.
		if err = c.provisionCompany(r.Context(), lc, "user:"+res.User.ID); err != nil {
			slog.Error("company catalog", "company", lc, "error", err)
		}
	}
	id, sess, err := c.Store.CreateSession(r.Context(), res.User.ID)
	if err != nil {
		fail("session", err)
		return
	}
	setSessionCookies(w, id, time.Unix(sess.ExpiresAt, 0), c.secureCookies())
	if target == "" {
		target = "/"
		if res.User.LastCompany != "" {
			target = "/c/" + res.User.LastCompany
		}
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// ownCompany creates a company for user with them as owner (a sign-up whose invitation could not be used).
func (c *Core) ownCompany(ctx context.Context, user, name, email string) (string, error) {
	var err error
	for range 3 {
		nc := newCompany(name, email)
		if err = c.Store.CreateUserCompany(ctx, user, nc); err == nil {
			return nc.ID, c.Store.SetLastCompany(ctx, user, nc.ID)
		}
		if !errors.Is(err, storage.ErrConflict) {
			break
		}
	}
	return "", err
}

// provisionCompany gives a company created by a person its first catalog from the sign-up template (D1). A Core
// without stored catalogs (no Root) has nothing to write.
func (c *Core) provisionCompany(ctx context.Context, company, by string) error {
	if c.Root == "" {
		return nil
	}
	if _, err := c.Store.CurrentCatalog(ctx, company); err == nil {
		return c.reloadCatalog(ctx, company)
	} else if !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	template := c.SignupTemplate
	if template == "" {
		template = DefaultSignupTemplate
	}
	docs, err := c.templateDocuments(company, template)
	if err != nil {
		return err
	}
	_, err = c.SaveCatalog(ctx, company, storage.CatalogSave{Docs: docs, Author: by, Note: "company created from template " + template})
	return err
}

// DefaultSignupTemplate is the starter catalog of a company created at sign-up.
const DefaultSignupTemplate = "solo"

var slugRun = regexp.MustCompile(`[^a-z0-9]+`)

// newCompany names the company of a first sign-in after the person (D1): an id from the name or the email's
// local part with a random suffix, and "<name>'s company".
func newCompany(name, email string) storage.NewCompany {
	base := strings.TrimSpace(name)
	if base == "" {
		base, _, _ = strings.Cut(email, "@")
	}
	slug := strings.Trim(slugRun.ReplaceAllString(strings.ToLower(base), "-"), "-")
	if len(slug) > 30 {
		slug = strings.Trim(slug[:30], "-")
	}
	if slug == "" {
		slug = "company"
	}
	suffix := make([]byte, 3)
	_, _ = rand.Read(suffix)
	display := strings.TrimSpace(name)
	if display == "" {
		display, _, _ = strings.Cut(email, "@")
	}
	return storage.NewCompany{ID: slug + "-" + hex.EncodeToString(suffix), Name: display + "'s company"}
}

// createUserCompany is POST /companies with a session (D2): only with the multi-company setting on.
func (c *Core) createUserCompany(w http.ResponseWriter, r *http.Request, user string) {
	if !c.MultiCompany {
		jsonStatus(w, 403, map[string]string{"error": "one company per user (FACTORY_MULTI_COMPANY is off)", "reason": "multi_company_off"})
		return
	}
	var p struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &p) {
		return
	}
	if strings.TrimSpace(p.Name) == "" {
		http.Error(w, "name required", 400)
		return
	}
	nc := newCompany(p.Name, "")
	nc.Name = strings.TrimSpace(p.Name)
	if err := c.Store.CreateUserCompany(r.Context(), user, nc); err != nil {
		reply(w, nil, err)
		return
	}
	if err := c.provisionCompany(r.Context(), nc.ID, "user:"+user); err != nil {
		reply(w, nil, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	write(w, storage.Membership{Company: nc.ID, Name: nc.Name, Role: storage.RoleOwner})
}

// accountRoutes are the owner routes of the company itself: GET /me for tokens (sessions are answered by the
// gateway), GET /company and PATCH /company (rename, owners only).
func (c *Core) accountRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /me", c.me)
	// The company's people (saas-web-ui 2.3): everyone reads, owners change roles and remove (requiredRole).
	m.HandleFunc("GET /memberships", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Members(r.Context(), c.companyFrom(r))
		reply(w, v, err)
	})
	m.HandleFunc("PATCH /memberships/{user}", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Role string `json:"role"`
		}
		if !decode(w, r, &p) {
			return
		}
		reply(w, map[string]string{"user": r.PathValue("user"), "role": p.Role}, c.Store.SetMemberRole(r.Context(), c.companyFrom(r), r.PathValue("user"), p.Role))
	})
	m.HandleFunc("DELETE /memberships/{user}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, c.Store.RemoveMember(r.Context(), c.companyFrom(r), r.PathValue("user")))
	})
	m.HandleFunc("GET /company", func(w http.ResponseWriter, r *http.Request) {
		company := c.companyFrom(r)
		name, err := c.Store.CompanyName(r.Context(), company)
		role := principalOf(r).Role
		if role == "" {
			role = storage.RoleOwner
		}
		reply(w, map[string]any{"id": company, "name": name, "role": role, "multi_company": c.MultiCompany}, err)
	})
	m.HandleFunc("PATCH /company", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &p) {
			return
		}
		company := c.companyFrom(r)
		if err := c.Store.RenameCompany(r.Context(), company, p.Name); err != nil {
			reply(w, nil, err)
			return
		}
		write(w, map[string]string{"id": company, "name": strings.TrimSpace(p.Name)})
	})
}

// requestCompany checks the company a token request names (D6): absent or its own company is fine; an owner
// token naming another company is refused; the operator may name any existing company (support).
func (c *Core) requestCompany(r *http.Request, own string, operatorToken bool) (string, error) {
	named := r.Header.Get(CompanyHeader)
	if named == "" || named == own {
		return own, nil
	}
	if !operatorToken {
		return "", fmt.Errorf("%w: this token belongs to another company", storage.ErrForbidden)
	}
	if _, err := c.Store.CompanyName(r.Context(), named); err != nil {
		return "", err
	}
	return named, nil
}

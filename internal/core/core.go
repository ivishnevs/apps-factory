package core

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"appfactory/internal/auth"
	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/skills"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

type Core struct {
	approvalMu sync.Mutex
	// handlerMu guards hooked, the Store whose hooks Handler wired.
	handlerMu sync.Mutex
	hooked    *storage.Store
	Skills    *skills.Registry
	Store     *storage.Store
	Agents    map[string]config.Agent
	// NodeHooks are the catalog's NodeHooks documents, served to nodes in the register and heartbeat answers.
	NodeHooks map[string]protocol.NodeHooks
	Token     string
	// Company is the tenant this Core serves; empty means storage.DefaultCompany.
	Company string
	// ExplicitExits switches runs to factory_exit_* (docs/explicit-exits-contract.md, FACTORY_EXPLICIT_EXITS).
	ExplicitExits bool
	// Teams are the catalog's teams (company-teams D2) when Core serves its catalog from files: the owner's
	// unaddressed messages go to a team's lead, onboarding audits to its audit assignee.
	Teams map[string]config.Team
	// limits are the public routes' rate limits (ratelimit.go; node-identity D3, saas-release-readiness 2.3).
	limits publicLimits
	// signatures is the node gateway's memory (node-identity D4).
	signatures signatureState
	// lastStallSweep is touched only by the tick goroutine.
	lastStallSweep time.Time
	// lastUnmergedCheck is the daily unmerged-branch check (tick goroutine only).
	lastUnmergedCheck time.Time
	// Prices is the price table (configs/prices.yaml) for runs whose runtime reported no cost.
	Prices config.Prices
	// Telegram routes owner messages from the Telegram channel (waits-events-cost-cli D4).
	Telegram TelegramConfig
	// bots runs the companies' Telegram bots (telegram-per-company).
	bots telegramBots
	// wakeups wakes the waiting node polls of a company; pollers keeps one poll per node (http-node-transport).
	wakeups        notifier
	pollers        polls
	outboxKickOnce sync.Once
	// Root is the factory root on disk. Set, Core serves stored catalogs (company-self-service D1, D4): every
	// company's current catalog version from storage, seeded from the repository; Agents, Skills, NodeHooks,
	// and Teams are then unused. Empty: those fields are the catalog of Company.
	Root      string
	catalogMu sync.RWMutex
	catalogs  map[string]*storedCatalog
	// Auth signs people in on the website (user-accounts D3); nil: no sign-in, tokens only.
	Auth *auth.Client
	// PublicURL is the website's address (FACTORY_PUBLIC_URL): the page a node's connection request opens.
	PublicURL string
	// NodeURL is Core's address for nodes (FACTORY_CORE_URL), named in the install script; empty: the request's.
	NodeURL string
	// NodeDownloads holds the node packages (FACTORY_NODE_DOWNLOADS); empty: <Root>/dist/node.
	NodeDownloads string
	// MultiCompany lets a signed-in user create more companies (FACTORY_MULTI_COMPANY, user-accounts D2).
	MultiCompany bool
	// SignupTemplate is the starter catalog of a company created at sign-up (default DefaultSignupTemplate).
	SignupTemplate string
	// Billing is how this Core charges (saas-billing, FACTORY_BILLING); the zero value is off: no plan limits.
	Billing BillingConfig
	// SupportEmail is the support address shown on the landing and in the app (FACTORY_SUPPORT_EMAIL); empty: none.
	SupportEmail string
}

type companyKey struct{}

// runKey carries the authenticated run's spec from agentGateway to the routes it forwards to.
type runKey struct{}

func (c *Core) company() string {
	if c.Company == "" {
		return storage.DefaultCompany
	}
	return c.Company
}

// companyFrom resolves the company of an authenticated request once: the run's company for run credentials
// (set by agentGateway), the owner token's company, the company a session names (X-Factory-Company, checked
// against the user's memberships by sessionGateway) or the one the operator names; otherwise Core's company.
func (c *Core) companyFrom(r *http.Request) string {
	if company, ok := r.Context().Value(companyKey{}).(string); ok {
		return company
	}
	return c.company()
}

func (c *Core) Handler() http.Handler {
	if c.Store != nil {
		// The Store's hooks are wired once per Store and the flags written only when they change: Handler is built
		// again per request in tests while waiting polls and sweeps read them.
		c.handlerMu.Lock()
		if c.hooked != c.Store {
			c.hierarchyHooks()
			c.Store.Wake = c.wakeups.signal
			c.hooked = c.Store
		}
		c.handlerMu.Unlock()
		if c.Store.ExplicitExits != c.ExplicitExits {
			c.Store.ExplicitExits = c.ExplicitExits
		}
		if on := c.Billing.On(); c.Store.PlanLimits != on {
			c.Store.PlanLimits = on
		}
	}
	m := http.NewServeMux()
	c.configRoutes(m)
	c.nodeKeyRoutes(m)
	c.taskRoutes(m)
	c.messageRoutes(m)
	c.topicRoutes(m)
	c.approvalRoutes(m)
	c.artifactRoutes(m)
	c.journalRoutes(m)
	c.blobRoutes(m)
	c.consoleRoutes(m)
	c.ownerRoutes(m)
	c.nodeRoutes(m)
	c.pollRoutes(m)
	c.directiveRoutes(m)
	c.costRoutes(m)
	c.telegramRoutes(m)
	c.authorityRoutes(m)
	c.conversationRoutes(m)
	c.catalogRoutes(m)
	c.companyRoutes(m)
	c.accountRoutes(m)
	c.invitationRoutes(m)
	c.secretRoutes(m)
	c.agentSecretRoutes(m)
	c.skillRoutes(m)
	c.presetRoutes(m)
	c.billingRoutes(m)
	c.supportRoutes(m)
	c.dataRoutes(m)
	c.statusNoteRoutes(m)
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		database, err := c.Store.DatabaseVersion(r.Context())
		if err != nil {
			http.Error(w, "unavailable", 503)
			return
		}
		exits := "off"
		if c.ExplicitExits {
			exits = "on"
		}
		write(w, map[string]string{"status": "ok", "database": database, "company": c.company(), "explicit_exits": exits, "default_report": c.defaultReport(c.companyFrom(r))})
	})
	m.HandleFunc("GET /agents", func(w http.ResponseWriter, r *http.Request) { write(w, config.Names(c.cat(c.companyFrom(r)).Agents)) })
	m.HandleFunc("POST /runs", c.create)
	m.HandleFunc("GET /runs", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.List(r.Context(), c.companyFrom(r))
		reply(w, v, err)
	})
	m.HandleFunc("GET /runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Get(r.Context(), c.companyFrom(r), r.PathValue("id"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		// A QUEUED run says why no node claims it yet (owner-ui 6.1).
		q, err := c.Store.QueuedWhy(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, struct {
			storage.Run
			Queued *storage.QueuedReason `json:"queued,omitempty"`
		}{v, q}, err)
	})
	m.HandleFunc("GET /runs/{id}/tool-calls", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.ToolCalls(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("GET /runs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Events(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("POST /runs/{id}/credential", c.credential)
	// Workers report a task run's workspace (task-worktrees D1), authenticated by the run's lease.
	m.HandleFunc("POST /runs/{id}/workspace", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Lease string `json:"lease"`
			tasks.Workspace
		}
		if !decode(w, r, &p) {
			return
		}
		v, err := c.Store.ReportWorkspace(r.Context(), c.companyFrom(r), r.PathValue("id"), p.Lease, p.Workspace)
		reply(w, v, err)
	})
	m.HandleFunc("POST /runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, c.Store.Cancel(r.Context(), c.companyFrom(r), r.PathValue("id")))
	})
	m.HandleFunc("POST /runs/{id}/resolve", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Answer string `json:"answer"`
			Result string `json:"result"`
		}
		if !decode(w, r, &p) {
			return
		}
		if strings.TrimSpace(p.Result) == "" {
			http.Error(w, "workspace reconciliation evidence required", 400)
			return
		}
		reply(w, map[string]bool{"ok": true}, c.Store.Resolve(r.Context(), c.companyFrom(r), r.PathValue("id"), p.Answer, p.Result))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The routes served without a credential are rate limited (saas-release-readiness 2.3).
		if c.rateLimited(w, r) {
			return
		}
		if r.URL.Path == "/agent/tools" || r.URL.Path == "/agent/call" || r.URL.Path == "/agent/command" || exitPath(r.URL.Path) || strings.HasPrefix(r.URL.Path, "/agent/blobs") {
			c.agentGateway(w, r, m)
			return
		}
		// Nodes without a credential yet (node-identity D3).
		if c.enrollmentGateway(w, r) {
			return
		}
		// A company bot's updates from Telegram, checked by the webhook's secret (telegram-per-company).
		if strings.HasPrefix(r.URL.Path, "/telegram/hook/") && r.Method == http.MethodPost {
			c.telegramHook(w, r)
			return
		}
		// The payment provider's notifications, checked by their signature (saas-billing).
		if r.URL.Path == paddleHookPath && r.Method == http.MethodPost {
			c.paddleHook(w, r)
			return
		}
		// Sign-in (user-accounts D3) needs no credential.
		if strings.HasPrefix(r.URL.Path, "/auth/") {
			c.authRoutes(w, r)
			return
		}
		// A signed request is a node's, and only a signed request reaches a node route (node-identity D4).
		if r.Header.Get("Signature-Input") != "" {
			c.nodeGateway(w, r, m)
			return
		}
		if nodeRoute(r) {
			http.Error(w, "unauthorized", 401)
			return
		}
		auth := r.Header.Get("Authorization")
		if c.Token != "" && subtle.ConstantTimeCompare([]byte(auth), []byte("Bearer "+c.Token)) == 1 {
			ctx := context.WithValue(context.WithValue(r.Context(), operatorKey{}, true), principalKey{}, principal{Kind: "operator", Role: storage.RoleOwner})
			if company, err := c.requestCompany(r, c.company(), true); err != nil {
				reply(w, nil, err)
				return
			} else if company != c.company() {
				ctx = context.WithValue(ctx, companyKey{}, company)
			}
			m.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		// A company owner token (company-self-service 4.1) reaches its company's owner routes.
		if token, ok := strings.CutPrefix(auth, "Bearer "); ok && strings.HasPrefix(token, storage.OwnerTokenPrefix) && c.Store != nil {
			c.ownerGateway(w, r, m, token)
			return
		}
		// A person signed in on the website (user-accounts D5, D6).
		if id := sessionCookie(r); id != "" && auth == "" && c.Store != nil {
			c.sessionGateway(w, r, m, id)
			return
		}
		http.Error(w, "unauthorized", 401)
	})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool { return decodeMax(w, r, v, 1<<20) }

// decodeMax is decode of a body up to max bytes (documents and tool calls carrying them).
// maxToolCallBody bounds an agent tool call.
const maxToolCallBody = 8 << 20

func decodeMax(w http.ResponseWriter, r *http.Request, v any, max int64) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, max))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		http.Error(w, err.Error(), 400)
		return false
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		http.Error(w, "exactly one JSON object required", 400)
		return false
	}
	return true
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		status := 500
		if errors.Is(err, storage.ErrInput) {
			status = 400
		}
		if errors.Is(err, storage.ErrConflict) {
			status = 409
		}
		if errors.Is(err, storage.ErrNotFound) {
			status = 404
		}
		if errors.Is(err, storage.ErrForbidden) {
			status = 403
		}
		if planLimitReply(w, err) {
			return
		}
		var archived *storage.ArchivedError
		if errors.As(err, &archived) {
			// An archived artifact version (artifact-retention): its metadata stays, its content is gone.
			jsonStatus(w, http.StatusGone, map[string]any{"error": archived.Error(), "archived_at": archived.ArchivedAt, "reason": archived.Reason,
				"artifact_id": archived.ID, "version": archived.Version, "latest_version": archived.Latest})
			return
		}
		var brief *storage.BriefError
		if errors.As(err, &brief) {
			// people-topics D3: what reaches people needs the bringer's brief; nothing was sent.
			jsonStatus(w, http.StatusBadRequest, map[string]any{"reason": "brief_required", "error": brief.Error(), "missing": brief.Missing, "too_long": brief.TooLong, "joining": brief.Joining, "hint": brief.Hint()})
			return
		}
		var closed *storage.ClosedTaskError
		if errors.As(err, &closed) {
			// owner-comments-handled: a comment on a closed task is refused with the two ways on.
			jsonStatus(w, 409, map[string]any{"reason": "task_closed", "error": closed.Error(), "status": closed.Status, "lead": closed.Lead, "options": closed.Options()})
			return
		}
		var answered *storage.AnsweredError
		if errors.As(err, &answered) {
			// First answer wins (hierarchy D3): a later answer gets 409 with the recorded one.
			jsonStatus(w, 409, map[string]any{"reason": "already_answered", "answer": answered.Answer})
			return
		}
		http.Error(w, err.Error(), status)
		return
	}
	write(w, v)
}
func (c *Core) create(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Project   string `json:"project"`
		Agent     string `json:"agent"`
		Workspace string `json:"workspace"`
		Prompt    string `json:"prompt"`
		SessionID string `json:"session_id"`
	}
	if !decode(w, r, &p) {
		return
	}
	a, ok := c.cat(c.companyFrom(r)).Agents[p.Agent]
	if !ok || strings.TrimSpace(p.Project) == "" || strings.TrimSpace(p.Prompt) == "" || !filepath.IsAbs(p.Workspace) {
		http.Error(w, "valid agent, project, absolute macOS workspace and prompt required", 400)
		return
	}
	if project, err := c.Store.Project(r.Context(), c.companyFrom(r), p.Project); err == nil {
		if filepath.Clean(p.Workspace) != project.Workspace {
			http.Error(w, "workspace differs from registered project", 400)
			return
		}
	} else if !errors.Is(err, storage.ErrNotFound) {
		reply(w, nil, err)
		return
	}
	if missing := c.missingSecret(c.companyFrom(r), p.Agent, a); missing != "" {
		jsonStatus(w, http.StatusConflict, map[string]string{"reason": "secret_missing", "secret": missing})
		return
	}
	snapshot, hash, err := c.RunSnapshot(a)
	if err != nil {
		reply(w, nil, err)
		return
	}
	// Workspace is a host path: Core in Docker must not attempt to resolve it.
	spec := protocol.RunSpec{ID: protocol.ID(), Project: p.Project, Agent: p.Agent, Provider: protocol.RuntimeProvider(a.Spec.Runtime.Provider), Workspace: filepath.Clean(p.Workspace), Prompt: p.Prompt, Instructions: a.Instructions, TimeoutSeconds: int(a.Timeout.Seconds()), SessionID: p.SessionID, RuntimeOptions: a.RunOptions(false), ConfigSnapshot: snapshot, ConfigHash: hash, Skills: append([]string{}, a.AvailableSkills...)}
	if err := c.Store.Create(r.Context(), c.companyFrom(r), spec); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	persisted, err := c.Store.Get(r.Context(), c.companyFrom(r), spec.ID)
	if err != nil {
		reply(w, nil, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	write(w, persisted.Spec)
}

// Run consumes events and runs the Core tick until ctx ends; an error is fatal. Stopping ctx is a clean
// stop: an operation it interrupts (a tick's query, a publish) does not turn into an error.
func (c *Core) Run(ctx context.Context) error {
	err := c.run(ctx)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (c *Core) run(ctx context.Context) error {
	c.hierarchyHooks()
	if err := c.EnsureConversations(ctx); err != nil {
		return err
	}
	if c.Store.Continuity == nil {
		c.Store.Continuity = clock.NewContinuity(3 * tickInterval)
	}
	// Waiting node polls re-read Postgres on this beat too, for wake-ups another Core process committed
	// (http-node-transport D6).
	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	// Git skill sources sync once a day (skill-registry D7), checked hourly.
	sync := time.NewTicker(time.Hour)
	defer sync.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if err := c.maintain(ctx); err != nil {
				return err
			}
		case <-sync.C:
			go c.syncDueSources(ctx)
		}
	}
}

const tickInterval = time.Second

// maintain is one Core tick: observe continuity first, so leases are judged against a
// window that restarted if the host slept, then expire, schedule and dispatch.
func (c *Core) maintain(ctx context.Context) error {
	if c.Store.Continuity != nil {
		c.Store.Continuity.Observe(c.Store.Now())
	}
	for _, company := range c.companies() {
		if err := c.isolate(ctx, company, "expiry", c.maintainCompany(ctx, company)); err != nil {
			return err
		}
	}
	if err := c.ScheduleMessages(ctx); err != nil {
		return err
	}
	if err := c.Schedule(ctx); err != nil {
		return err
	}
	if err := c.sweepStalls(ctx); err != nil {
		return err
	}
	for _, company := range c.companies() {
		if _, err := c.Store.PriceRunCosts(ctx, company, c.Prices); c.isolate(ctx, company, "cost pricing", err) != nil {
			return err
		}
	}
	return c.checkUnmerged(ctx)
}

// maintainCompany expires what timed out in company before scheduling: leases, nodes, idle runs, pauses,
// waits, approvals and queued runs nobody claimed (the reconciler, trigger-log-scheduling D5).
func (c *Core) maintainCompany(ctx context.Context, company string) error {
	if err := c.Store.Expire(ctx, company); err != nil {
		return err
	}
	if err := c.Store.ExpireNodes(ctx, company); err != nil {
		return err
	}
	if _, err := c.Store.StopIdle(ctx, company); err != nil {
		return err
	}
	if err := c.Store.ExpirePauses(ctx, company); err != nil {
		return err
	}
	if err := c.Store.ExpireWaits(ctx, company); err != nil {
		return err
	}
	if err := c.Store.ExpireApprovals(ctx, company); err != nil {
		return err
	}
	_, err := c.Store.ExpireQueued(ctx, company)
	return err
}

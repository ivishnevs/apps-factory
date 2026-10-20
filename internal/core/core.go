package core

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/skills"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"

	"github.com/nats-io/nats.go"
)

type Core struct {
	approvalMu sync.Mutex
	Skills     *skills.Registry
	Store      *storage.Store
	Bus        *bus.Bus
	Agents     map[string]config.Agent
	// NodeHooks are the catalog's NodeHooks documents, served to nodes in the register and heartbeat answers.
	NodeHooks map[string]protocol.NodeHooks
	Token     string
	// Company is the tenant this Core serves; empty means storage.DefaultCompany.
	Company string
	// ExplicitExits switches runs to factory_exit_* (docs/explicit-exits-contract.md, FACTORY_EXPLICIT_EXITS).
	ExplicitExits bool
	// lastStallSweep is touched only by the tick goroutine.
	lastStallSweep time.Time
	// lastUnmergedCheck is the daily unmerged-branch check (tick goroutine only).
	lastUnmergedCheck time.Time
	// Prices is the price table (configs/prices.yaml) for runs whose runtime reported no cost.
	Prices config.Prices
	// Telegram routes owner messages from the Telegram channel (waits-events-cost-cli D4).
	Telegram TelegramConfig
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

// companyFrom resolves the company of an authenticated request once: the run's company for
// run credentials (set by agentGateway), otherwise the company of the owner token.
func (c *Core) companyFrom(r *http.Request) string {
	if company, ok := r.Context().Value(companyKey{}).(string); ok {
		return company
	}
	return c.company()
}

func (c *Core) Handler() http.Handler {
	if c.Store != nil {
		c.Store.EscalationTarget = c.escalationTarget
		c.Store.ManagerOf = c.escalationTarget
		c.Store.ExplicitExits = c.ExplicitExits
	}
	m := http.NewServeMux()
	c.configRoutes(m)
	c.taskRoutes(m)
	c.messageRoutes(m)
	c.approvalRoutes(m)
	c.artifactRoutes(m)
	c.journalRoutes(m)
	c.openSpecRoutes(m)
	c.consoleRoutes(m)
	c.ownerRoutes(m)
	c.nodeRoutes(m)
	c.directiveRoutes(m)
	c.costRoutes(m)
	c.telegramRoutes(m)
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := c.Store.DB.PingContext(r.Context()); err != nil || !c.Bus.NC.IsConnected() {
			http.Error(w, "unavailable", 503)
			return
		}
		write(w, map[string]string{"status": "ok"})
	})
	m.HandleFunc("GET /agents", func(w http.ResponseWriter, r *http.Request) { write(w, config.Names(c.Agents)) })
	m.HandleFunc("POST /runs", c.create)
	m.HandleFunc("GET /runs", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.List(r.Context(), c.companyFrom(r))
		reply(w, v, err)
	})
	m.HandleFunc("GET /runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Get(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
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
	m.HandleFunc("POST /runs/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Lease string `json:"lease"`
		}
		if !decode(w, r, &p) {
			return
		}
		cancel, err := c.Store.Heartbeat(r.Context(), c.companyFrom(r), r.PathValue("id"), p.Lease)
		reply(w, map[string]bool{"cancel": cancel}, err)
	})
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
		if r.URL.Path == "/agent/tools" || r.URL.Path == "/agent/call" || r.URL.Path == "/agent/command" || exitPath(r.URL.Path) {
			c.agentGateway(w, r, m)
			return
		}
		auth := r.Header.Get("Authorization")
		if c.Token != "" && subtle.ConstantTimeCompare([]byte(auth), []byte("Bearer "+c.Token)) == 1 {
			m.ServeHTTP(w, r)
			return
		}
		// A per-machine node token (node-agent 1.1) reaches only the node routes, for its own node.
		if token, ok := strings.CutPrefix(auth, "Bearer "); ok && strings.HasPrefix(token, storage.NodeTokenPrefix) && c.Store != nil {
			c.nodeGateway(w, r, m, token)
			return
		}
		http.Error(w, "unauthorized", 401)
	})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
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
	a, ok := c.Agents[p.Agent]
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
	snapshot, hash, err := c.RunSnapshot(a)
	if err != nil {
		reply(w, nil, err)
		return
	}
	// Workspace is a host path: Core in Docker must not attempt to resolve it.
	spec := protocol.RunSpec{ID: protocol.ID(), Project: p.Project, Agent: p.Agent, Provider: protocol.RuntimeProvider(a.Spec.Runtime.Provider), Workspace: filepath.Clean(p.Workspace), Prompt: p.Prompt, Instructions: a.Instructions, TimeoutSeconds: int(a.Timeout.Seconds()), SessionID: p.SessionID, RuntimeOptions: map[string]any{"model": a.Spec.Runtime.Model}, ConfigSnapshot: snapshot, ConfigHash: hash, Skills: append([]string{}, a.AvailableSkills...)}
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
	c.Store.EscalationTarget = c.escalationTarget
	c.Store.ManagerOf = c.escalationTarget
	c.Store.ExplicitExits = c.ExplicitExits
	sub, err := c.Bus.Subscribe("factory.events.>", "core-events-v1")
	if err != nil {
		return err
	}
	errs := make(chan error, 1)
	go func() { errs <- c.consume(ctx, sub) }()
	if c.Store.Continuity == nil {
		c.Store.Continuity = clock.NewContinuity(3 * tickInterval)
	}
	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errs:
			return err
		case <-tick.C:
			if err = c.maintain(ctx); err != nil {
				return err
			}
			if err = c.publishOutbox(ctx); err != nil {
				return err
			}
		}
	}
}

// publishOutbox publishes the pending outbox messages of every company.
func (c *Core) publishOutbox(ctx context.Context) error {
	out, err := c.Store.Pending(ctx)
	if err != nil {
		return err
	}
	for _, o := range out {
		pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = c.Bus.Publish(pubCtx, o.Subject, o.ID, o.Body)
		cancel()
		if err != nil {
			// One message that cannot be published (a broker outage, an invalid subject) is retried
			// with a backoff; the rest of the queue moves on.
			slog.Warn("outbox publish", "id", o.ID, "subject", o.Subject, "error", err)
			if err = c.Store.PublishFailed(ctx, o.ID, err); err != nil {
				return err
			}
			continue
		}
		if err = c.Store.Sent(ctx, o.ID); err != nil {
			return err
		}
	}
	return nil
}

const tickInterval = time.Second

// maintain is one Core tick: observe continuity first, so leases are judged against a
// window that restarted if the host slept, then expire, schedule and dispatch.
func (c *Core) maintain(ctx context.Context) error {
	if c.Store.Continuity != nil {
		c.Store.Continuity.Observe(c.Store.Now())
	}
	if err := c.Store.Expire(ctx, c.company()); err != nil {
		return err
	}
	if err := c.Store.ExpireNodes(ctx, c.company()); err != nil {
		return err
	}
	if _, err := c.Store.StopIdle(ctx, c.company()); err != nil {
		return err
	}
	if err := c.Store.ExpirePauses(ctx, c.company()); err != nil {
		return err
	}
	if err := c.Store.ExpireWaits(ctx, c.company()); err != nil {
		return err
	}
	if err := c.Store.ExpireApprovals(ctx, c.company()); err != nil {
		return err
	}
	if err := c.ScheduleMessages(ctx); err != nil {
		return err
	}
	// Reconciler (trigger-log-scheduling D5): a task run nobody claimed within the queue TTL ends unclaimed,
	// so Schedule dispatches its task again; younger queued runs get their command resent below.
	if _, err := c.Store.ExpireQueued(ctx, c.company()); err != nil {
		return err
	}
	if err := c.Schedule(ctx); err != nil {
		return err
	}
	if _, err := c.Store.RedeliverQueued(ctx, c.company()); err != nil {
		return err
	}
	if err := c.sweepStalls(ctx); err != nil {
		return err
	}
	if _, err := c.Store.PriceRunCosts(ctx, c.company(), c.Prices); err != nil {
		return err
	}
	return c.checkUnmerged(ctx)
}
func (c *Core) consume(ctx context.Context, sub *nats.Subscription) error {
	for ctx.Err() == nil {
		msgs, err := sub.Fetch(1, nats.MaxWait(time.Second))
		if errors.Is(err, nats.ErrTimeout) {
			continue
		}
		if err != nil {
			return err
		}
		for _, m := range msgs {
			var e protocol.Envelope
			if err = json.Unmarshal(m.Data, &e); err != nil || e.Version != 1 {
				_ = m.Term()
				slog.Error("invalid event", "error", err)
				continue
			}
			if !strings.HasPrefix(m.Subject, "factory.events.agent.") {
				if err = m.AckSync(); err != nil {
					return err
				}
				continue
			}
			if err = c.Store.Apply(ctx, c.company(), e); err != nil {
				if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrInvalid) {
					_ = m.Term()
				} else {
					_ = m.NakWithDelay(time.Second)
				}
				slog.Error("event apply", "event", e.ID, "error", err)
				continue
			}
			if err = m.AckSync(); err != nil {
				return fmt.Errorf("event ack: %w", err)
			}
		}
	}
	return nil
}

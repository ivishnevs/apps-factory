package core

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
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

	"github.com/nats-io/nats.go"
)

type Core struct {
	approvalMu sync.Mutex
	Skills     *skills.Registry
	Store      *storage.Store
	Bus        *bus.Bus
	Agents     map[string]config.Agent
	Token      string
	// Company is the tenant this Core serves; empty means storage.DefaultCompany.
	Company string
	// lastStallSweep is touched only by the tick goroutine.
	lastStallSweep time.Time
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
	m := http.NewServeMux()
	c.configRoutes(m)
	c.taskRoutes(m)
	c.messageRoutes(m)
	c.approvalRoutes(m)
	c.artifactRoutes(m)
	c.journalRoutes(m)
	c.openSpecRoutes(m)
	c.consoleRoutes(m)
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
	m.HandleFunc("POST /runs/{id}/claim", func(w http.ResponseWriter, r *http.Request) {
		token, err := c.Store.Claim(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, map[string]string{"lease": token}, err)
	})
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
	m.HandleFunc("POST /runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, c.Store.Cancel(r.Context(), c.companyFrom(r), r.PathValue("id")))
	})
	m.HandleFunc("POST /runs/{id}/resolve", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Status string `json:"status"`
			Result string `json:"result"`
		}
		if !decode(w, r, &p) {
			return
		}
		if strings.TrimSpace(p.Result) == "" {
			http.Error(w, "workspace reconciliation evidence required", 400)
			return
		}
		reply(w, map[string]bool{"ok": true}, c.Store.Resolve(r.Context(), c.companyFrom(r), r.PathValue("id"), p.Status, p.Result))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agent/tools" || r.URL.Path == "/agent/call" {
			c.agentGateway(w, r, m)
			return
		}
		if c.Token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+c.Token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		m.ServeHTTP(w, r)
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
	snapshot, err := json.Marshal(a)
	if err != nil {
		reply(w, nil, err)
		return
	}
	hash := sha256.Sum256(snapshot)
	// Workspace is a host path: Core in Docker must not attempt to resolve it.
	spec := protocol.RunSpec{ID: protocol.ID(), Project: p.Project, Agent: p.Agent, Provider: protocol.RuntimeProvider(a.Spec.Runtime.Provider), Workspace: filepath.Clean(p.Workspace), Prompt: p.Prompt, Instructions: a.Instructions, TimeoutSeconds: int(a.Timeout.Seconds()), SessionID: p.SessionID, RuntimeOptions: map[string]any{"model": a.Spec.Runtime.Model}, ConfigSnapshot: snapshot, ConfigHash: hex.EncodeToString(hash[:]), Skills: append([]string{}, a.AvailableSkills...)}
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
func (c *Core) Run(ctx context.Context) error {
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
			out, err := c.Store.Pending(ctx)
			if err != nil {
				return err
			}
			for _, o := range out {
				pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				err = c.Bus.Publish(pubCtx, o.Subject, o.ID, o.Body)
				cancel()
				if err != nil {
					slog.Warn("outbox publish", "error", err)
					break
				}
				if err = c.Store.Sent(ctx, o.ID); err != nil {
					return err
				}
			}
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
	if err := c.Store.Expire(ctx, c.company()); err != nil {
		return err
	}
	if err := c.Store.ExpireApprovals(ctx, c.company()); err != nil {
		return err
	}
	if err := c.ScheduleMessages(ctx); err != nil {
		return err
	}
	if err := c.Schedule(ctx); err != nil {
		return err
	}
	if _, err := c.Store.RedeliverQueued(ctx, c.company()); err != nil {
		return err
	}
	return c.sweepStalls(ctx)
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

package core

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"appfactory/internal/billing"
	"appfactory/internal/storage"
)

// SaaS billing (saas-billing). The account pays; its owners see Billing: the plan, usage against its limits,
// Upgrade (the provider's hosted checkout) and Manage (the provider's customer portal). The provider's signed
// webhooks change the account's plan; the operator can set a plan too (before a provider, or a complimentary one).
// Billing is off unless FACTORY_BILLING says otherwise: self-hosted and dev Cores have no plan limits.

// Billing modes (FACTORY_BILLING).
const (
	BillingOff    = "off"
	BillingOn     = "on"     // limits, plans set by the operator; no payments
	BillingPaddle = "paddle" // limits and payments through Paddle
)

// BillingConfig is how this Core charges.
type BillingConfig struct {
	Mode   string
	Paddle *billing.Paddle
}

// On reports whether plan limits apply.
func (b BillingConfig) On() bool { return b.Mode == BillingOn || b.Mode == BillingPaddle }

// paddleHookPath receives Paddle's notifications (a notification destination in Paddle's dashboard).
const paddleHookPath = "/hooks/paddle"

// BillingView is GET /billing: the account's plan, usage and what the owner can do.
type BillingView struct {
	Enabled bool `json:"enabled"`
	// Provider is the payment provider ("" when payments are not configured: the operator sets plans).
	Provider string `json:"provider,omitempty"`
	storage.Billing
	Plans []storage.Plan `json:"plans"`
	// Checkout: Upgrade opens the provider's checkout; Portal: Manage opens the provider's customer portal.
	Checkout bool `json:"checkout"`
	Portal   bool `json:"portal"`
}

func (c *Core) billingRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /billing", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.billingView(r)
		reply(w, v, err)
	})
	m.HandleFunc("POST /billing/checkout", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.billingView(r)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if !v.Checkout {
			http.Error(w, "upgrading is not available here: "+c.noCheckout(v), http.StatusConflict)
			return
		}
		url, err := c.Billing.Paddle.Checkout(r.Context(), storage.PlanPro, v.Account, c.companyFrom(r), v.CustomerID)
		if err != nil {
			slog.Error("billing checkout", "account", v.Account, "error", err)
			http.Error(w, "the payment provider did not open a checkout; try again later", http.StatusBadGateway)
			return
		}
		write(w, map[string]string{"url": url})
	})
	m.HandleFunc("POST /billing/portal", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.billingView(r)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if !v.Portal {
			http.Error(w, "no paid subscription to manage yet", http.StatusConflict)
			return
		}
		url, err := c.Billing.Paddle.Portal(r.Context(), v.CustomerID, v.SubscriptionID)
		if err != nil {
			slog.Error("billing portal", "account", v.Account, "error", err)
			http.Error(w, "the payment provider did not open its portal; try again later", http.StatusBadGateway)
			return
		}
		write(w, map[string]string{"url": url})
	})
	// The owner picks which own nodes take work when the account has more than its plan allows.
	m.HandleFunc("POST /billing/nodes/{id}/pick", func(w http.ResponseWriter, r *http.Request) {
		account, err := c.Store.AccountOf(r.Context(), c.companyFrom(r))
		if err != nil {
			reply(w, nil, err)
			return
		}
		k, err := c.Store.NodeKeyOf(r.Context(), r.PathValue("id"))
		if err == nil {
			var owner string
			if owner, err = c.Store.AccountOf(r.Context(), k.Company); err == nil && owner != account {
				err = storage.ErrNotFound
			}
		}
		if err == nil {
			err = c.Store.PickNode(r.Context(), k.Company, k.NodeID)
		}
		reply(w, map[string]bool{"ok": err == nil}, err)
	})
	// The operator's plan for a company's account (FACTORY_TOKEN only: /companies/ is operator-only).
	m.HandleFunc("PUT /companies/{id}/plan", func(w http.ResponseWriter, r *http.Request) {
		if !operator(r) {
			http.Error(w, "operator only", http.StatusForbidden)
			return
		}
		var p struct {
			Plan string `json:"plan"`
		}
		if !decode(w, r, &p) {
			return
		}
		account, err := c.Store.AccountOf(r.Context(), r.PathValue("id"))
		if err == nil {
			err = c.Store.SetAccountPlan(r.Context(), account, p.Plan)
		}
		if err != nil {
			reply(w, nil, err)
			return
		}
		sub, err := c.Store.AccountSubscription(r.Context(), account)
		reply(w, sub, err)
	})
}

func (c *Core) billingView(r *http.Request) (BillingView, error) {
	v := BillingView{Enabled: c.Billing.On(), Plans: []storage.Plan{storage.Plans[storage.PlanFree], storage.Plans[storage.PlanPro]}}
	var err error
	if v.Billing, err = c.Store.AccountBilling(r.Context(), c.companyFrom(r)); err != nil {
		return v, err
	}
	if c.Billing.Mode == BillingPaddle && c.Billing.Paddle != nil {
		v.Provider = BillingPaddle
		paid := v.Status == storage.SubActive || v.Status == storage.SubPastDue
		v.Checkout = v.Enabled && v.Plan != storage.PlanUnlimited && !(paid && v.Plan == storage.PlanPro)
		v.Portal = v.CustomerID != "" && v.Provider == v.Billing.Provider
	}
	return v, nil
}

func (c *Core) noCheckout(v BillingView) string {
	switch {
	case !v.Enabled:
		return "billing is off on this Core"
	case v.Provider == "":
		return "payments are not configured on this Core; the operator sets plans"
	case v.Plan == storage.PlanUnlimited:
		return "this account has no plan limits"
	}
	return "this account already pays for Pro: manage it in the portal"
}

// paddleHook applies a Paddle notification: only a valid signature is read; an event is applied once (the store
// answers a redelivery as a duplicate), so Paddle may retry freely. A failure to store answers 500: Paddle retries.
func (c *Core) paddleHook(w http.ResponseWriter, r *http.Request) {
	p := c.Billing.Paddle
	if c.Billing.Mode != BillingPaddle || p == nil {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	if err = p.Verify(r.Header.Get(billing.SignatureHeader), body); err != nil {
		slog.Warn("paddle webhook refused", "error", err)
		http.Error(w, "signature not valid", http.StatusUnauthorized)
		return
	}
	ev, err := billing.ParseEvent(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	in := storage.BillingEvent{Provider: BillingPaddle, EventID: ev.EventID, Type: ev.EventType, OccurredAt: ev.OccurredAt}
	if ev.IsSubscription() {
		s := p.Subscription(ev)
		in.Account, in.CustomerID, in.SubscriptionID, in.Plan, in.Status, in.PeriodEndsAt, in.CancelAt = s.Account, s.CustomerID, s.ID, s.Plan, s.Status, s.PeriodEndsAt, s.CancelAt
		if s.Plan == "" {
			slog.Warn("paddle subscription with a price no plan has", "event", ev.EventID, "subscription", s.ID)
		}
	}
	outcome, err := c.Store.ApplyBillingEvent(r.Context(), in)
	if err != nil {
		slog.Error("paddle webhook", "event", ev.EventID, "error", err)
		http.Error(w, "not stored", http.StatusInternalServerError)
		return
	}
	level := slog.LevelInfo
	if outcome == storage.BillingUnknown {
		level = slog.LevelError
	}
	slog.Log(r.Context(), level, "paddle webhook", "event", ev.EventID, "type", ev.EventType, "subscription", in.SubscriptionID, "outcome", outcome)
	write(w, map[string]string{"outcome": outcome})
}

// planLimitReply answers a refusal by the plan: 403 with reason plan_limit, which the web shows with a link to
// Billing. false when err is not one.
func planLimitReply(w http.ResponseWriter, err error) bool {
	var limit *storage.PlanLimitError
	if !errors.As(err, &limit) {
		return false
	}
	jsonStatus(w, http.StatusForbidden, map[string]string{"error": limit.Message, "reason": "plan_limit", "plan": limit.Plan})
	return true
}

// startTrial gives a new account its Pro trial when billing is on.
func (c *Core) startTrial(r *http.Request, account string) {
	if !c.Billing.On() || account == "" {
		return
	}
	if err := c.Store.StartTrial(r.Context(), account); err != nil {
		slog.Error("billing trial", "account", account, "error", fmt.Sprint(err))
	}
}

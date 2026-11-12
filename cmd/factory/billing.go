package main

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"appfactory/internal/billing"
	"appfactory/internal/core"
	"appfactory/internal/storage"
)

// billingConfig reads FACTORY_BILLING (saas-billing): unset or off, no plan limits (self-hosted and dev Cores);
// on, plan limits with plans set by the operator; paddle, plan limits and payments through Paddle, which needs
// FACTORY_PADDLE_API_KEY, FACTORY_PADDLE_WEBHOOK_SECRET and FACTORY_PADDLE_PRICE_PRO (FACTORY_PADDLE_ENV sandbox,
// the default, or production; FACTORY_PADDLE_API_URL overrides the address).
func billingConfig(getenv func(string) string) (core.BillingConfig, error) {
	mode := strings.ToLower(strings.TrimSpace(getenv("FACTORY_BILLING")))
	switch mode {
	case "", core.BillingOff:
		return core.BillingConfig{Mode: core.BillingOff}, nil
	case core.BillingOn:
		slog.Info("billing on: plan limits apply, plans set by the operator")
		return core.BillingConfig{Mode: core.BillingOn}, nil
	case core.BillingPaddle:
	default:
		return core.BillingConfig{}, fmt.Errorf("FACTORY_BILLING must be off, on or paddle, not %q", mode)
	}
	p := &billing.Paddle{APIKey: getenv("FACTORY_PADDLE_API_KEY"), WebhookSecret: getenv("FACTORY_PADDLE_WEBHOOK_SECRET"),
		Prices: map[string]string{}, APIURL: getenv("FACTORY_PADDLE_API_URL")}
	if price := strings.TrimSpace(getenv("FACTORY_PADDLE_PRICE_PRO")); price != "" {
		p.Prices[price] = storage.PlanPro
	}
	var missing []string
	for name, v := range map[string]string{"FACTORY_PADDLE_API_KEY": p.APIKey, "FACTORY_PADDLE_WEBHOOK_SECRET": p.WebhookSecret, "FACTORY_PADDLE_PRICE_PRO": p.PriceOf(storage.PlanPro)} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return core.BillingConfig{}, fmt.Errorf("FACTORY_BILLING=paddle needs %s", strings.Join(missing, ", "))
	}
	env := strings.ToLower(getenv("FACTORY_PADDLE_ENV"))
	if p.APIURL == "" {
		switch env {
		case "", "sandbox":
			p.APIURL, env = billing.SandboxAPI, "sandbox"
		case "production", "live":
			p.APIURL = billing.ProductionAPI
		default:
			return core.BillingConfig{}, fmt.Errorf("FACTORY_PADDLE_ENV must be sandbox or production, not %q", env)
		}
	}
	slog.Info("billing on: Paddle", "environment", env, "api", p.APIURL)
	return core.BillingConfig{Mode: core.BillingPaddle, Paddle: p}, nil
}

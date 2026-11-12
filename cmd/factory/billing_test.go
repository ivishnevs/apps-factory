package main

import (
	"strings"
	"testing"

	"appfactory/internal/billing"
	"appfactory/internal/core"
)

// TestBillingConfig (saas-billing): billing is off unless FACTORY_BILLING says on or paddle; paddle needs its key,
// webhook secret and Pro price and defaults to the sandbox.
func TestBillingConfig(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	if b, err := billingConfig(env(nil)); err != nil || b.On() || b.Paddle != nil {
		t.Fatal(b, err)
	}
	if b, err := billingConfig(env(map[string]string{"FACTORY_BILLING": "on"})); err != nil || !b.On() || b.Paddle != nil {
		t.Fatal(b, err)
	}
	if _, err := billingConfig(env(map[string]string{"FACTORY_BILLING": "stripe"})); err == nil {
		t.Fatal("unknown mode accepted")
	}
	_, err := billingConfig(env(map[string]string{"FACTORY_BILLING": "paddle", "FACTORY_PADDLE_API_KEY": "k"}))
	if err == nil || !strings.Contains(err.Error(), "FACTORY_PADDLE_PRICE_PRO, FACTORY_PADDLE_WEBHOOK_SECRET") {
		t.Fatal(err)
	}
	full := map[string]string{"FACTORY_BILLING": "paddle", "FACTORY_PADDLE_API_KEY": "k", "FACTORY_PADDLE_WEBHOOK_SECRET": "s", "FACTORY_PADDLE_PRICE_PRO": "pri_1"}
	b, err := billingConfig(env(full))
	if err != nil || b.Mode != core.BillingPaddle || b.Paddle.APIURL != billing.SandboxAPI || b.Paddle.PriceOf("pro") != "pri_1" {
		t.Fatal(b, err)
	}
	full["FACTORY_PADDLE_ENV"] = "production"
	if b, err = billingConfig(env(full)); err != nil || b.Paddle.APIURL != billing.ProductionAPI {
		t.Fatal(b, err)
	}
	full["FACTORY_PADDLE_ENV"] = "staging"
	if _, err = billingConfig(env(full)); err == nil {
		t.Fatal("unknown environment accepted")
	}
}

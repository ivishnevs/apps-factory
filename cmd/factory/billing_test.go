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

// TestBlobQuotaConfig (security-review-2026-11 F6): the hosted service (billing on) has a storage quota per company
// unless FACTORY_COMPANY_STORAGE_GB says otherwise; a self-hosted Core has none.
func TestBlobQuotaConfig(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	for _, tc := range []struct {
		env  map[string]string
		on   bool
		want int64
	}{
		{nil, false, 0},
		{nil, true, DefaultCompanyStorageGB << 30},
		{map[string]string{"FACTORY_COMPANY_STORAGE_GB": "5"}, false, 5 << 30},
		{map[string]string{"FACTORY_COMPANY_STORAGE_GB": "0"}, true, 0},
	} {
		if got, err := blobQuota(env(tc.env), tc.on); err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	if _, err := blobQuota(env(map[string]string{"FACTORY_COMPANY_STORAGE_GB": "lots"}), true); err == nil {
		t.Fatal("a non-number accepted")
	}
}

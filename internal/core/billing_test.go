package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appfactory/internal/auth/authtest"
	"appfactory/internal/billing"
	"appfactory/internal/clock"
	"appfactory/internal/storage"
)

// fakePaddle is Paddle's API for the checkout and portal links: it records the transactions it was asked for.
func fakePaddle(t *testing.T) (*billing.Paddle, *[]map[string]any) {
	t.Helper()
	var txns []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pdl_key" {
			w.WriteHeader(403)
			return
		}
		switch {
		case r.URL.Path == "/transactions":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			txns = append(txns, in)
			_, _ = io.WriteString(w, `{"data":{"id":"txn_1","checkout":{"url":"https://pay.example/?_ptxn=txn_1"}}}`)
		case strings.HasSuffix(r.URL.Path, "/portal-sessions"):
			_, _ = io.WriteString(w, `{"data":{"urls":{"general":{"overview":"https://portal.example`+strings.TrimSuffix(r.URL.Path, "/portal-sessions")+`"}}}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return &billing.Paddle{APIKey: "pdl_key", WebhookSecret: "whsec", APIURL: srv.URL, Prices: map[string]string{"pri_pro": storage.PlanPro}}, &txns
}

func paddleEvent(id, typ, account, sub, status string, at time.Time) []byte {
	data := map[string]any{"id": sub, "status": status, "customer_id": "ctm_ann", "items": []any{map[string]any{"price": map[string]any{"id": "pri_pro"}}},
		"current_billing_period": map[string]any{"starts_at": at.Format(time.RFC3339), "ends_at": at.Add(30 * 24 * time.Hour).Format(time.RFC3339)}}
	if account != "" {
		data["custom_data"] = map[string]any{"account_id": account}
	}
	b, _ := json.Marshal(map[string]any{"event_id": id, "event_type": typ, "occurred_at": at.Format(time.RFC3339Nano), "notification_id": "ntf_" + id, "data": data})
	return b
}

func hook(h http.Handler, header string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", paddleHookPath, strings.NewReader(string(body)))
	if header != "" {
		r.Header.Set(billing.SignatureHeader, header)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestBilling (saas-billing 1.2-1.4): a sign-up on a Core with Paddle billing starts a 14-day Pro trial; after it
// the account is on Free and inviting is refused with reason plan_limit; Upgrade opens Paddle's checkout for the
// account; Paddle's signed webhook moves the account to Pro (once; a forged one is refused); Manage opens the
// portal; members do not see Billing; the operator sets a plan.
func TestBilling(t *testing.T) {
	c, is := sessionCore(t)
	fake := clock.NewFake(time.Now())
	c.Store.Clock = fake
	p, txns := fakePaddle(t)
	c.Billing = BillingConfig{Mode: BillingPaddle, Paddle: p}
	h := c.Handler()
	ann := &browser{t: t, h: h, cookies: map[string]string{}}
	loc := ann.signIn(is, "google", authtest.Person{UserID: "g-ann", Email: "ann@example.com", EmailVerified: true, Name: "Ann Lee"})
	ann.company, _ = strings.CutSuffix(strings.TrimPrefix(loc, "/c/"), "/setup")
	view := func() BillingView {
		t.Helper()
		var v BillingView
		if w := ann.do("GET", "/billing", nil); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		return v
	}
	v := view()
	if !v.Enabled || v.Provider != BillingPaddle || v.Status != storage.SubTrialing || v.Effective.ID != storage.PlanPro || !v.Checkout || v.Portal || len(v.Plans) != 2 || v.Members != 1 {
		t.Fatalf("%+v", v)
	}
	if w := ann.do("POST", "/invitations", map[string]string{"role": "member"}); w.Code != 201 {
		t.Fatal("inviting during the trial", w.Code, w.Body.String())
	}

	// The trial ends: Free, no invitations.
	fake.Advance(storage.TrialDays*24*time.Hour + time.Minute)
	if v = view(); v.Effective.ID != storage.PlanFree || !v.Checkout {
		t.Fatalf("%+v", v)
	}
	w := ann.do("POST", "/invitations", map[string]string{"role": "member"})
	var refusal map[string]string
	if w.Code != 403 || json.Unmarshal(w.Body.Bytes(), &refusal) != nil || refusal["reason"] != "plan_limit" || !strings.Contains(refusal["error"], "Pro") {
		t.Fatal(w.Code, w.Body.String())
	}

	// Upgrade: a checkout for the account.
	var link map[string]string
	if w = ann.do("POST", "/billing/checkout", nil); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &link) != nil || link["url"] != "https://pay.example/?_ptxn=txn_1" {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(*txns) != 1 || (*txns)[0]["custom_data"].(map[string]any)["account_id"] != v.Account || v.Account == "" {
		t.Fatal(*txns, v.Account)
	}
	if w = ann.do("POST", "/billing/portal", nil); w.Code != 409 {
		t.Fatal("portal without a subscription", w.Code)
	}

	// Paddle's webhook: forged and unsigned ones are refused; the signed one applies once.
	body := paddleEvent("evt_1", "subscription.created", v.Account, "sub_1", "active", time.Now())
	if w = hook(h, "", body); w.Code != 401 {
		t.Fatal("unsigned", w.Code)
	}
	if w = hook(h, billing.SignatureFor("forged", time.Now(), body), body); w.Code != 401 {
		t.Fatal("forged", w.Code)
	}
	if v2 := view(); v2.Effective.ID != storage.PlanFree {
		t.Fatal("a refused webhook changed the plan")
	}
	sig := billing.SignatureFor("whsec", time.Now(), body)
	if w = hook(h, sig, body); w.Code != 200 || !strings.Contains(w.Body.String(), storage.BillingApplied) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = hook(h, sig, body); w.Code != 200 || !strings.Contains(w.Body.String(), storage.BillingDuplicate) {
		t.Fatal(w.Code, w.Body.String())
	}
	if v = view(); v.Effective.ID != storage.PlanPro || v.Status != storage.SubActive || v.Checkout || !v.Portal || v.SubscriptionID != "sub_1" {
		t.Fatalf("%+v", v)
	}
	if w = ann.do("POST", "/invitations", map[string]string{"role": "member"}); w.Code != 201 {
		t.Fatal("inviting on Pro", w.Code)
	}
	if w = ann.do("POST", "/billing/checkout", nil); w.Code != 409 {
		t.Fatal("checkout while paying", w.Code)
	}
	if w = ann.do("POST", "/billing/portal", nil); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &link) != nil || link["url"] != "https://portal.example/customers/ctm_ann" {
		t.Fatal(w.Code, w.Body.String())
	}

	// Members (and admins) do not see Billing.
	var inv struct {
		Token string `json:"token"`
	}
	w = ann.do("POST", "/invitations", map[string]string{"role": "admin"})
	_ = json.Unmarshal(w.Body.Bytes(), &inv)
	bob := &browser{t: t, h: h, cookies: map[string]string{}}
	bob.signInFrom(is, "github", authtest.Person{UserID: "gh-bob", Email: "bob@example.com", EmailVerified: true, Name: "Bob"}, "/invite/"+inv.Token)
	bob.company = ann.company
	if w = bob.do("GET", "/billing", nil); w.Code != 403 {
		t.Fatal("an admin sees Billing", w.Code)
	}

	// Cancelled in the portal: Paddle says so, the account is on Free again.
	cancel := paddleEvent("evt_2", "subscription.canceled", "", "sub_1", "canceled", time.Now().Add(time.Second))
	if w = hook(h, billing.SignatureFor("whsec", time.Now(), cancel), cancel); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if v = view(); v.Effective.ID != storage.PlanFree {
		t.Fatalf("%+v", v)
	}

	// The operator sets a plan (a complimentary one here); a company owner cannot.
	if code, out := call(t, c, "PUT", "/companies/"+ann.company+"/plan", "operator", map[string]string{"plan": "unlimited"}); code != 200 || !strings.Contains(out, `"unlimited"`) {
		t.Fatal(code, out)
	}
	if w = ann.do("PUT", "/companies/"+ann.company+"/plan", map[string]string{"plan": "pro"}); w.Code != 403 {
		t.Fatal("an owner set the plan", w.Code)
	}
	if v = view(); v.Effective.ID != storage.PlanUnlimited || v.Checkout {
		t.Fatalf("%+v", v)
	}
}

// TestBillingOff: without FACTORY_BILLING nothing is limited and nobody gets a trial; Billing says it is off.
func TestBillingOff(t *testing.T) {
	c, is := sessionCore(t)
	h := c.Handler()
	ann := &browser{t: t, h: h, cookies: map[string]string{}}
	loc := ann.signIn(is, "google", authtest.Person{UserID: "g-ann", Email: "ann@example.com", EmailVerified: true, Name: "Ann Lee"})
	ann.company, _ = strings.CutSuffix(strings.TrimPrefix(loc, "/c/"), "/setup")
	var v BillingView
	if w := ann.do("GET", "/billing", nil); json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Enabled || v.Status != "" || v.Effective.ID != storage.PlanUnlimited || v.Checkout {
		t.Fatalf("%+v", v)
	}
	if w := ann.do("POST", "/billing/checkout", nil); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := hook(h, "ts=1;h1=00", []byte("{}")); w.Code != 404 {
		t.Fatal("webhook served with billing off", w.Code)
	}
	// Even a Free plan set by the operator limits nothing while billing is off.
	if code, _ := call(t, c, "PUT", "/companies/"+ann.company+"/plan", "operator", map[string]string{"plan": "free"}); code != 200 {
		t.Fatal(code)
	}
	if w := ann.do("POST", "/invitations", map[string]string{"role": "member"}); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
}

package billing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// subscriptionCreated has the shape of Paddle Billing's subscription.created notification (developer.paddle.com,
// webhooks/subscriptions/subscription-created), trimmed to the fields around the ones Core reads.
const subscriptionCreated = `{
  "event_id": "evt_01hv8x2acma2gz1my1ewfqv7jt",
  "event_type": "subscription.created",
  "occurred_at": "2026-11-12T10:41:18.215812Z",
  "notification_id": "ntf_01hv8x2axnm1fgwthrb4ha8kr6",
  "data": {
    "id": "sub_01hv8x29kz0t586xy6zn1a62ny",
    "status": "active",
    "customer_id": "ctm_01hv6y1jedq4p1n0yqn5ba3ky4",
    "address_id": "add_01hv8gq3318ktkfengj2r75gfx",
    "currency_code": "USD",
    "collection_mode": "automatic",
    "custom_data": {"account_id": "u123", "company": "ann-co"},
    "items": [{"status": "active", "quantity": 1, "recurring": true,
      "price": {"id": "pri_pro", "product_id": "pro_01", "billing_cycle": {"interval": "month", "frequency": 1}}}],
    "current_billing_period": {"starts_at": "2026-11-12T10:41:18Z", "ends_at": "2026-12-12T10:41:18Z"},
    "scheduled_change": null,
    "next_billed_at": "2026-12-12T10:41:18Z"
  }
}`

func paddle(now time.Time) *Paddle {
	return &Paddle{APIKey: "pdl_test_key", WebhookSecret: "pdl_ntfset_secret", Prices: map[string]string{"pri_pro": "pro"}, Now: func() time.Time { return now }}
}

// TestVerify (saas-billing 1.3): Paddle-Signature is ts and h1 = HMAC-SHA256("ts:body"); a wrong secret, a changed
// body, an old timestamp or a malformed header are refused; any h1 of several (secret rotation) is enough.
func TestVerify(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	p := paddle(now)
	body := []byte(subscriptionCreated)
	good := SignatureFor(p.WebhookSecret, now, body)
	if err := p.Verify(good, body); err != nil {
		t.Fatal(err)
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	if err := p.Verify("ts="+ts+";h1=00ff;h1="+Sign(p.WebhookSecret, ts, body), body); err != nil {
		t.Fatal("a second h1 not accepted", err)
	}
	for name, c := range map[string]struct {
		header string
		body   []byte
	}{
		"other secret": {SignatureFor("other", now, body), body},
		"changed body": {good, append([]byte(" "), body...)},
		"old":          {SignatureFor(p.WebhookSecret, now.Add(-SignatureTolerance-time.Second), body), body},
		"future":       {SignatureFor(p.WebhookSecret, now.Add(SignatureTolerance+time.Second), body), body},
		"no h1":        {"ts=" + ts, body},
		"no ts":        {"h1=" + Sign(p.WebhookSecret, ts, body), body},
		"empty":        {"", body},
	} {
		if err := p.Verify(c.header, c.body); !errors.Is(err, ErrSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	p.WebhookSecret = ""
	if err := p.Verify(good, body); !errors.Is(err, ErrSignature) {
		t.Fatal("verified without a secret")
	}
}

// TestParseSubscription reads a subscription notification: the account from custom data, the plan of its price,
// the period end and a scheduled cancellation.
func TestParseSubscription(t *testing.T) {
	p := paddle(time.Now())
	e, err := ParseEvent([]byte(subscriptionCreated))
	if err != nil || !e.IsSubscription() || e.EventID != "evt_01hv8x2acma2gz1my1ewfqv7jt" || e.OccurredAt.IsZero() {
		t.Fatal(e, err)
	}
	s := p.Subscription(e)
	want := Subscription{ID: "sub_01hv8x29kz0t586xy6zn1a62ny", Status: "active", CustomerID: "ctm_01hv6y1jedq4p1n0yqn5ba3ky4", Account: "u123", Plan: "pro",
		PeriodEndsAt: time.Date(2026, 12, 12, 10, 41, 18, 0, time.UTC).Unix()}
	if s != want {
		t.Fatalf("%+v", s)
	}
	var raw map[string]any
	_ = json.Unmarshal([]byte(subscriptionCreated), &raw)
	data := raw["data"].(map[string]any)
	data["scheduled_change"] = map[string]any{"action": "cancel", "effective_at": "2026-12-12T10:41:18Z", "resume_at": nil}
	data["items"] = []any{map[string]any{"price": map[string]any{"id": "pri_unknown"}}}
	b, _ := json.Marshal(raw)
	e, _ = ParseEvent(b)
	if s = p.Subscription(e); s.CancelAt != want.PeriodEndsAt || s.Plan != "" {
		t.Fatalf("%+v", s)
	}
	if _, err = ParseEvent([]byte(`{"data":{}}`)); err == nil {
		t.Fatal("an event without id parsed")
	}
}

// TestCheckoutAndPortal against a fake Paddle API: the checkout is a transaction for the plan's price carrying the
// account; the portal is a customer portal session; refusals come back as APIError.
func TestCheckoutAndPortal(t *testing.T) {
	var got []map[string]any
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pdl_test_key" {
			w.WriteHeader(403)
			_, _ = io.WriteString(w, `{"error":{"type":"request_error","code":"forbidden","detail":"bad key"}}`)
			return
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		in["_path"] = r.Method + " " + r.URL.Path
		got = append(got, in)
		switch r.URL.Path {
		case "/transactions":
			_, _ = io.WriteString(w, `{"data":{"id":"txn_1","status":"ready","checkout":{"url":"https://pay.example/checkout?_ptxn=txn_1"}}}`)
		case "/customers/ctm_1/portal-sessions":
			_, _ = io.WriteString(w, `{"data":{"id":"cpls_1","customer_id":"ctm_1","urls":{"general":{"overview":"https://customer-portal.paddle.com/cpl_1"},"subscriptions":[]}}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer fake.Close()
	p := paddle(time.Now())
	p.APIURL = fake.URL
	ctx := context.Background()
	url, err := p.Checkout(ctx, "pro", "u123", "ann-co", "")
	if err != nil || url != "https://pay.example/checkout?_ptxn=txn_1" {
		t.Fatal(url, err)
	}
	b, _ := json.Marshal(got[0])
	if string(b) != `{"_path":"POST /transactions","custom_data":{"account_id":"u123","company":"ann-co"},"items":[{"price_id":"pri_pro","quantity":1}]}` {
		t.Fatal(string(b))
	}
	if _, err = p.Checkout(ctx, "free", "u123", "ann-co", ""); err == nil {
		t.Fatal("a checkout for a plan without a price")
	}
	if url, err = p.Portal(ctx, "ctm_1", "sub_1"); err != nil || url != "https://customer-portal.paddle.com/cpl_1" {
		t.Fatal(url, err)
	}
	if b, _ = json.Marshal(got[len(got)-1]); string(b) != `{"_path":"POST /customers/ctm_1/portal-sessions","subscription_ids":["sub_1"]}` {
		t.Fatal(string(b))
	}
	p.APIKey = "wrong"
	var apiErr *APIError
	if _, err = p.Portal(ctx, "ctm_1", ""); !errors.As(err, &apiErr) || apiErr.Status != 403 || apiErr.Code != "forbidden" {
		t.Fatal(err)
	}
}

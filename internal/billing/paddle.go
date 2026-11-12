// Package billing talks to the payment provider (saas-billing): Paddle Billing, a merchant of record. Core asks it
// for hosted checkout and customer portal links and verifies its signed webhooks; Core never sees card data.
//
// Paddle Billing API: https://developer.paddle.com/api-reference/overview (Bearer API key; sandbox
// https://sandbox-api.paddle.com, live https://api.paddle.com). Webhook signatures:
// https://developer.paddle.com/webhooks/signature-verification (header Paddle-Signature "ts=<unix>;h1=<hex>",
// HMAC-SHA256 of "<ts>:<raw body>" with the notification destination's secret key).
package billing

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Paddle API addresses.
const (
	SandboxAPI    = "https://sandbox-api.paddle.com"
	ProductionAPI = "https://api.paddle.com"
)

// SignatureHeader carries a webhook's signature.
const SignatureHeader = "Paddle-Signature"

// SignatureTolerance is how far a webhook's timestamp may be from now (replays older than this are refused; a
// redelivered event inside it is answered as a duplicate by the store).
const SignatureTolerance = 5 * time.Minute

// ErrSignature is a webhook whose signature does not verify.
var ErrSignature = errors.New("paddle webhook signature not valid")

// Paddle is the configured provider: the API key (server-side, never shown), the webhook destination's secret key
// and the price of each plan.
type Paddle struct {
	APIKey        string
	WebhookSecret string
	// APIURL is SandboxAPI or ProductionAPI (tests: a fake).
	APIURL string
	// Prices maps a Paddle price id (pri_...) to a plan id (pro).
	Prices map[string]string
	HTTP   *http.Client
	Now    func() time.Time
}

func (p *Paddle) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// PriceOf is the price id of plan, "" when none is configured.
func (p *Paddle) PriceOf(plan string) string {
	for price, pl := range p.Prices {
		if pl == plan {
			return price
		}
	}
	return ""
}

// Verify checks header (Paddle-Signature) against body: a timestamp within SignatureTolerance and an h1 that is
// the HMAC-SHA256 of "ts:body" (several h1 during a secret rotation: any one matching is enough).
func (p *Paddle) Verify(header string, body []byte) error {
	if p.WebhookSecret == "" {
		return fmt.Errorf("%w: no webhook secret configured", ErrSignature)
	}
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "ts":
			ts = v
		case "h1":
			sigs = append(sigs, v)
		}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(sigs) == 0 {
		return fmt.Errorf("%w: malformed header", ErrSignature)
	}
	if d := p.now().Sub(time.Unix(sec, 0)); d > SignatureTolerance || d < -SignatureTolerance {
		return fmt.Errorf("%w: timestamp outside the tolerance", ErrSignature)
	}
	want := Sign(p.WebhookSecret, ts, body)
	for _, s := range sigs {
		if hmac.Equal([]byte(s), []byte(want)) {
			return nil
		}
	}
	return ErrSignature
}

// Sign is the h1 signature of body at ts (a fake Paddle in tests signs with it too).
func Sign(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + ":"))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// SignatureFor is a whole Paddle-Signature header for body at t.
func SignatureFor(secret string, t time.Time, body []byte) string {
	ts := strconv.FormatInt(t.Unix(), 10)
	return "ts=" + ts + ";h1=" + Sign(secret, ts, body)
}

// Event is a webhook notification's envelope with the subscription it carries (subscription.* events).
type Event struct {
	EventID    string    `json:"event_id"`
	EventType  string    `json:"event_type"`
	OccurredAt time.Time `json:"occurred_at"`
	Data       Entity    `json:"data"`
}

// Entity is the part of a subscription (or transaction) entity Core reads.
type Entity struct {
	ID         string         `json:"id"`
	Status     string         `json:"status"`
	CustomerID string         `json:"customer_id"`
	CustomData map[string]any `json:"custom_data"`
	Items      []struct {
		Price struct {
			ID string `json:"id"`
		} `json:"price"`
	} `json:"items"`
	CurrentBillingPeriod *struct {
		EndsAt time.Time `json:"ends_at"`
	} `json:"current_billing_period"`
	ScheduledChange *struct {
		Action      string    `json:"action"`
		EffectiveAt time.Time `json:"effective_at"`
	} `json:"scheduled_change"`
}

// Subscription is a parsed subscription event: Plan "" for a price no plan has.
type Subscription struct {
	ID, Status, CustomerID, Account, Plan string
	PeriodEndsAt, CancelAt                int64
}

// ParseEvent reads a verified webhook body.
func ParseEvent(body []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(body, &e); err != nil {
		return e, err
	}
	if e.EventID == "" || e.EventType == "" {
		return e, errors.New("paddle event without event_id or event_type")
	}
	return e, nil
}

// IsSubscription reports whether the event carries a subscription entity.
func (e Event) IsSubscription() bool { return strings.HasPrefix(e.EventType, "subscription.") }

// Subscription reads the event's subscription with the plan of its first known price.
func (p *Paddle) Subscription(e Event) Subscription {
	d := e.Data
	s := Subscription{ID: d.ID, Status: d.Status, CustomerID: d.CustomerID}
	if a, ok := d.CustomData["account_id"].(string); ok {
		s.Account = a
	}
	for _, it := range d.Items {
		if plan, ok := p.Prices[it.Price.ID]; ok {
			s.Plan = plan
			break
		}
	}
	if d.CurrentBillingPeriod != nil {
		s.PeriodEndsAt = d.CurrentBillingPeriod.EndsAt.Unix()
	}
	if d.ScheduledChange != nil && d.ScheduledChange.Action == "cancel" {
		s.CancelAt = d.ScheduledChange.EffectiveAt.Unix()
	}
	return s
}

// APIError is a refusal by the Paddle API.
type APIError struct {
	Status int
	Code   string
	Detail string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("paddle: HTTP %d %s: %s", e.Status, e.Code, e.Detail)
}

func (p *Paddle) call(ctx context.Context, method, path string, in, out any) error {
	if p.APIKey == "" {
		return errors.New("paddle: no API key configured")
	}
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.APIURL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := p.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Code   string `json:"code"`
				Detail string `json:"detail"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		return &APIError{Status: res.StatusCode, Code: e.Error.Code, Detail: e.Error.Detail}
	}
	return json.Unmarshal(body, out)
}

// Checkout is a hosted checkout for plan: Paddle creates a transaction for the plan's price carrying the account
// (custom_data.account_id, copied to the subscription) and answers its checkout URL (the account's default payment
// link with the transaction). customerID continues an earlier customer ("" for a new one).
func (p *Paddle) Checkout(ctx context.Context, plan, account, company, customerID string) (string, error) {
	price := p.PriceOf(plan)
	if price == "" {
		return "", fmt.Errorf("paddle: no price configured for plan %s", plan)
	}
	in := map[string]any{
		"items":       []map[string]any{{"price_id": price, "quantity": 1}},
		"custom_data": map[string]string{"account_id": account, "company": company},
	}
	if customerID != "" {
		in["customer_id"] = customerID
	}
	var out struct {
		Data struct {
			ID       string `json:"id"`
			Checkout *struct {
				URL string `json:"url"`
			} `json:"checkout"`
		} `json:"data"`
	}
	if err := p.call(ctx, http.MethodPost, "/transactions", in, &out); err != nil {
		return "", err
	}
	if out.Data.Checkout == nil || out.Data.Checkout.URL == "" {
		return "", errors.New("paddle: the transaction has no checkout URL (set a default payment link in Paddle's checkout settings)")
	}
	return out.Data.Checkout.URL, nil
}

// Portal is a customer portal session's overview link (card, invoices, cancel) for the customer's subscription.
func (p *Paddle) Portal(ctx context.Context, customerID, subscriptionID string) (string, error) {
	in := map[string]any{}
	if subscriptionID != "" {
		in["subscription_ids"] = []string{subscriptionID}
	}
	var out struct {
		Data struct {
			URLs struct {
				General struct {
					Overview string `json:"overview"`
				} `json:"general"`
			} `json:"urls"`
		} `json:"data"`
	}
	if err := p.call(ctx, http.MethodPost, "/customers/"+url.PathEscape(customerID)+"/portal-sessions", in, &out); err != nil {
		return "", err
	}
	if out.Data.URLs.General.Overview == "" {
		return "", errors.New("paddle: the portal session has no overview URL")
	}
	return out.Data.URLs.General.Overview, nil
}

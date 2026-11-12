package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SaaS billing (saas-billing): every account has a plan whose limits Core enforces while billing is on
// (Store.PlanLimits, FACTORY_BILLING). The plan comes from the account's subscription row: a 14-day Pro trial
// started at sign-up, a plan the operator set, or the payment provider's signed webhooks. An account without a
// row has no limits (accounts from before billing, self-hosted and dev Cores). Over a limit nothing is deleted:
// connecting another node and inviting are refused, and own nodes over the plan take no work.

// Plan ids.
const (
	PlanFree = "free"
	PlanPro  = "pro"
	// PlanUnlimited is no plan limits: an account without a subscription row, or one the operator set.
	PlanUnlimited = "unlimited"
)

// Subscription statuses (the provider's: Paddle Billing's subscription statuses).
const (
	SubTrialing = "trialing"
	SubActive   = "active"
	SubPastDue  = "past_due"
	SubPaused   = "paused"
	SubCanceled = "canceled"
)

// TrialDays is the Pro trial of a new account (no card).
const TrialDays = 14

// Plan is what a plan allows. OwnNodes < 0 is unlimited; cloud nodes (hosted-connectors) never count.
type Plan struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	PriceUSD    int    `json:"price_usd"`
	OwnNodes    int    `json:"own_nodes"`
	Invitations bool   `json:"invitations"`
	// Credits are the cloud credits included each month (hosted-connectors; not spent anywhere yet).
	Credits int `json:"credits"`
}

// Plans are the launch plans (owner 2026-11-06): Free and Pro; On-prem is postponed.
var Plans = map[string]Plan{
	PlanFree:      {ID: PlanFree, Name: "Free", PriceUSD: 0, OwnNodes: 1, Invitations: false, Credits: 0},
	PlanPro:       {ID: PlanPro, Name: "Pro", PriceUSD: 29, OwnNodes: 5, Invitations: true, Credits: 2000},
	PlanUnlimited: {ID: PlanUnlimited, Name: "No plan limits", OwnNodes: -1, Invitations: true},
}

// Subscription is an account's subscription row; Status "" means the account has none.
type Subscription struct {
	Account        string `json:"account"`
	Plan           string `json:"plan"`
	Status         string `json:"status"`
	TrialEndsAt    int64  `json:"trial_ends_at,omitempty"`
	PeriodEndsAt   int64  `json:"period_ends_at,omitempty"`
	CancelAt       int64  `json:"cancel_at,omitempty"`
	Provider       string `json:"provider,omitempty"`
	CustomerID     string `json:"customer_id,omitempty"`
	SubscriptionID string `json:"subscription_id,omitempty"`
	UpdatedAt      int64  `json:"updated_at,omitempty"`
	eventAt        int64
}

// Effective is the plan whose limits apply at now: none without a row; a trial is Pro until it ends, then Free;
// an active or past-due (the provider retries the payment) subscription its plan; paused or canceled Free.
func (s Subscription) Effective(now time.Time) Plan {
	switch {
	case s.Status == "" || s.Plan == PlanUnlimited:
		return Plans[PlanUnlimited]
	case s.Status == SubTrialing:
		if s.TrialEndsAt > now.Unix() {
			return Plans[PlanPro]
		}
		return Plans[PlanFree]
	case s.Status == SubActive || s.Status == SubPastDue:
		if p, ok := Plans[s.Plan]; ok {
			return p
		}
	}
	return Plans[PlanFree]
}

// PlanLimitError is a refusal by the plan: Core answers 403 with reason plan_limit and a link to Billing.
type PlanLimitError struct {
	Plan    string
	Message string
}

func (e *PlanLimitError) Error() string { return e.Message }
func (e *PlanLimitError) Unwrap() error { return ErrForbidden }

func (q querierCtx) accountOf(ctx context.Context, company string) (string, error) {
	var account string
	err := q.QueryRowContext(ctx, "SELECT account_id FROM companies WHERE id=?", company).Scan(&account)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return account, err
}

// querierCtx wraps a querier (Store.DB or a Tx) for the billing reads.
type querierCtx struct{ querier }

const subscriptionColumns = "account_id,plan,status,trial_ends_at,period_ends_at,cancel_at,provider,customer_id,subscription_id,updated_at,event_at"

func scanSubscription(row interface{ Scan(...any) error }) (Subscription, error) {
	var s Subscription
	err := row.Scan(&s.Account, &s.Plan, &s.Status, &s.TrialEndsAt, &s.PeriodEndsAt, &s.CancelAt, &s.Provider, &s.CustomerID, &s.SubscriptionID, &s.UpdatedAt, &s.eventAt)
	return s, err
}

func (q querierCtx) subscription(ctx context.Context, account string) (Subscription, error) {
	s, err := scanSubscription(q.QueryRowContext(ctx, "SELECT "+subscriptionColumns+" FROM account_subscriptions WHERE account_id=?", account))
	if errors.Is(err, sql.ErrNoRows) {
		return Subscription{Account: account}, nil
	}
	return s, err
}

// AccountOf is the account that owns company.
func (s *Store) AccountOf(ctx context.Context, company string) (string, error) {
	return querierCtx{s.DB}.accountOf(ctx, company)
}

// AccountSubscription is account's subscription (Status "" when it has none).
func (s *Store) AccountSubscription(ctx context.Context, account string) (Subscription, error) {
	return querierCtx{s.DB}.subscription(ctx, account)
}

// StartTrial gives a new account its Pro trial; an account that already has a subscription row keeps it.
func (s *Store) StartTrial(ctx context.Context, account string) error {
	now := s.Now()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO account_subscriptions(account_id,plan,status,trial_ends_at,updated_at) VALUES(?,?,?,?,?)
 ON CONFLICT(account_id) DO NOTHING`, account, PlanPro, SubTrialing, now.Add(TrialDays*24*time.Hour).Unix(), now.Unix())
	return constraint(err)
}

// SetAccountPlan is the operator's plan for account (before or besides a provider): free, pro or unlimited, active
// from now. A provider event that occurred later overrides it.
func (s *Store) SetAccountPlan(ctx context.Context, account, plan string) error {
	if _, ok := Plans[plan]; !ok {
		return inputError("plan must be free, pro or unlimited")
	}
	now := s.Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO account_subscriptions(account_id,plan,status,provider,event_at,updated_at)
 SELECT id,?,?,'operator',?,? FROM accounts WHERE id=?
 ON CONFLICT(account_id) DO UPDATE SET plan=excluded.plan,status=excluded.status,provider=CASE WHEN account_subscriptions.subscription_id='' THEN 'operator' ELSE account_subscriptions.provider END,
 trial_ends_at=0,event_at=excluded.event_at,updated_at=excluded.updated_at`, plan, SubActive, now.UnixNano(), now.Unix(), account)
	if err != nil {
		return constraint(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// OwnNode is one of an account's own nodes (active keys in any of its companies) and whether it takes work.
type OwnNode struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Company   string `json:"company"`
	TakesWork bool   `json:"takes_work"`
	Picked    bool   `json:"picked"`
}

// ownNodes lists account's own nodes in the order they take work (most recently picked, then oldest approved);
// the first limit take work (limit < 0: all).
func (q querierCtx) ownNodes(ctx context.Context, account string, limit int) ([]OwnNode, error) {
	rows, err := q.QueryContext(ctx, `SELECT k.node_id,k.name,k.company_id,k.picked_at FROM node_keys k JOIN companies c ON c.id=k.company_id
 WHERE c.account_id=? AND k.status=? ORDER BY k.picked_at DESC,k.approved_at,k.node_id`, account, NodeKeyActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OwnNode{}
	for rows.Next() {
		var n OwnNode
		var picked int64
		if err = rows.Scan(&n.ID, &n.Name, &n.Company, &picked); err != nil {
			return nil, err
		}
		n.Picked, n.TakesWork = picked > 0, limit < 0 || len(out) < limit
		out = append(out, n)
	}
	return out, rows.Err()
}

// Billing is an account's plan, its limits and what it uses of them.
type Billing struct {
	Subscription
	Effective Plan      `json:"effective"`
	Nodes     []OwnNode `json:"nodes"`
	Members   int       `json:"members"`
}

// AccountBilling is the plan of company's account with its usage: own nodes and people across its companies.
func (s *Store) AccountBilling(ctx context.Context, company string) (Billing, error) {
	var b Billing
	q := querierCtx{s.DB}
	account, err := q.accountOf(ctx, company)
	if err != nil {
		return b, err
	}
	if b.Subscription, err = q.subscription(ctx, account); err != nil {
		return b, err
	}
	b.Effective = b.Subscription.Effective(s.Now())
	if b.Nodes, err = q.ownNodes(ctx, account, b.Effective.OwnNodes); err != nil {
		return b, err
	}
	err = s.DB.QueryRowContext(ctx, "SELECT count(DISTINCT m.user_id) FROM memberships m JOIN companies c ON c.id=m.company_id WHERE c.account_id=?", account).Scan(&b.Members)
	return b, err
}

// PickNode makes node the first of its account's own nodes to take work (the owner's pick after a downgrade).
func (s *Store) PickNode(ctx context.Context, company, node string) error {
	res, err := s.DB.ExecContext(ctx, "UPDATE node_keys SET picked_at=? WHERE company_id=? AND node_id=? AND status=?", s.Now().UnixNano(), company, node, NodeKeyActive)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if s.Wake != nil {
		s.Wake(company)
	}
	return nil
}

// planOf is the effective plan of company's account inside tx, after locking the account's billing (two
// companies of one account approving nodes at once count each other). Without PlanLimits: unlimited.
func planOf(ctx context.Context, tx *Tx, company string) (Plan, string, error) {
	if !tx.store.PlanLimits {
		return Plans[PlanUnlimited], "", nil
	}
	q := querierCtx{tx}
	account, err := q.accountOf(ctx, company)
	if err != nil {
		return Plan{}, "", err
	}
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "billing:"+account); err != nil {
		return Plan{}, "", err
	}
	sub, err := q.subscription(ctx, account)
	if err != nil {
		return Plan{}, "", err
	}
	return sub.Effective(tx.now()), account, nil
}

// checkNodeLimit refuses activating one more own node of company's account over its plan.
func checkNodeLimit(ctx context.Context, tx *Tx, company string) error {
	plan, account, err := planOf(ctx, tx, company)
	if err != nil || plan.OwnNodes < 0 {
		return err
	}
	var n int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM node_keys k JOIN companies c ON c.id=k.company_id WHERE c.account_id=? AND k.status=?", account, NodeKeyActive).Scan(&n); err != nil {
		return err
	}
	if n < plan.OwnNodes {
		return nil
	}
	msg := fmt.Sprintf("your %s plan connects %s and you have %d", plan.Name, nodesWord(plan.OwnNodes), n)
	if pro := Plans[PlanPro]; plan.OwnNodes < pro.OwnNodes {
		msg += fmt.Sprintf(": Pro connects %s, see Billing", nodesWord(pro.OwnNodes))
	} else {
		msg += ": remove a node first"
	}
	return &PlanLimitError{Plan: plan.ID, Message: msg}
}

func nodesWord(n int) string {
	if n == 1 {
		return "1 own node"
	}
	return fmt.Sprintf("%d own nodes", n)
}

// checkInvitations refuses inviting people into company on a plan without invitations.
func checkInvitations(ctx context.Context, tx *Tx, company string) error {
	plan, _, err := planOf(ctx, tx, company)
	if err != nil || plan.Invitations {
		return err
	}
	return &PlanLimitError{Plan: plan.ID, Message: fmt.Sprintf("inviting people starts with Pro (this company is on %s): see Billing", plan.Name)}
}

// nodeOverPlan says why node takes no work under its account's plan ("" when it may): its account has more own
// nodes than the plan allows and this one is not among those that take work.
func (s *Store) nodeOverPlan(ctx context.Context, q querier, company, node string) (string, error) {
	if !s.PlanLimits {
		return "", nil
	}
	qc := querierCtx{q}
	account, err := qc.accountOf(ctx, company)
	if err != nil {
		return "", err
	}
	sub, err := qc.subscription(ctx, account)
	if err != nil {
		return "", err
	}
	plan := sub.Effective(s.Now())
	if plan.OwnNodes < 0 {
		return "", nil
	}
	nodes, err := qc.ownNodes(ctx, account, plan.OwnNodes)
	if err != nil {
		return "", err
	}
	for _, n := range nodes {
		if n.ID == node && !n.TakesWork {
			return fmt.Sprintf("over the %s plan: it runs %s and this account has %d; pick the nodes that take work in Billing", plan.Name, nodesWord(plan.OwnNodes), len(nodes)), nil
		}
	}
	return "", nil
}

// BillingEvent is a provider's webhook event, already verified and parsed.
type BillingEvent struct {
	Provider   string
	EventID    string
	Type       string
	OccurredAt time.Time
	// Account is the account the checkout named (custom data); "" falls back to the subscription or customer.
	Account        string
	CustomerID     string
	SubscriptionID string
	// Plan is the plan of the subscription's price ("" for a price Core does not know).
	Plan         string
	Status       string
	PeriodEndsAt int64
	CancelAt     int64
}

// Billing event outcomes.
const (
	BillingApplied   = "applied"
	BillingDuplicate = "duplicate"
	BillingStale     = "stale"
	BillingUnknown   = "unknown_account"
	BillingIgnored   = "ignored"
)

// ApplyBillingEvent applies a subscription event once (by provider and event id) and answers what became of it:
// a redelivered event is a duplicate, one older than the last applied a stale one; an event of another
// subscription than the account's live one does not cancel it.
func (s *Store) ApplyBillingEvent(ctx context.Context, ev BillingEvent) (string, error) {
	if ev.Provider == "" || ev.EventID == "" {
		return "", inputError("billing event requires provider and id")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	t := &Tx{Tx: tx, store: s}
	now := s.Now()
	occurred := ev.OccurredAt.UnixNano()
	record := func(account, outcome string) (string, error) {
		res, err := t.ExecContext(ctx, `INSERT INTO billing_events(provider,event_id,type,account_id,occurred_at,received_at,outcome) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(provider,event_id) DO NOTHING`, ev.Provider, ev.EventID, ev.Type, account, occurred, now.Unix(), outcome)
		if err != nil {
			return "", err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return BillingDuplicate, nil
		}
		return outcome, nil
	}
	var seen int
	if err = t.QueryRowContext(ctx, "SELECT count(*) FROM billing_events WHERE provider=? AND event_id=?", ev.Provider, ev.EventID).Scan(&seen); err != nil {
		return "", err
	}
	if seen > 0 {
		return BillingDuplicate, nil
	}
	account := ev.Account
	if account != "" {
		if err = t.QueryRowContext(ctx, "SELECT id FROM accounts WHERE id=?", account).Scan(&account); errors.Is(err, sql.ErrNoRows) {
			account = ""
		} else if err != nil {
			return "", err
		}
	}
	for _, by := range [][2]string{{"subscription_id", ev.SubscriptionID}, {"customer_id", ev.CustomerID}} {
		if account != "" || by[1] == "" {
			continue
		}
		err = t.QueryRowContext(ctx, "SELECT account_id FROM account_subscriptions WHERE provider=? AND "+by[0]+"=? ORDER BY updated_at DESC LIMIT 1", ev.Provider, by[1]).Scan(&account)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	if account == "" {
		out, err := record("", BillingUnknown)
		if err != nil {
			return "", err
		}
		return out, tx.Commit()
	}
	if ev.SubscriptionID == "" || ev.Status == "" || ev.Plan == "" {
		out, err := record(account, BillingIgnored)
		if err != nil {
			return "", err
		}
		return out, tx.Commit()
	}
	if _, err = t.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", "billing:"+account); err != nil {
		return "", err
	}
	cur, err := querierCtx{t}.subscription(ctx, account)
	if err != nil {
		return "", err
	}
	outcome := BillingApplied
	switch {
	case cur.Status != "" && cur.eventAt > occurred:
		outcome = BillingStale
	case cur.SubscriptionID != "" && cur.SubscriptionID != ev.SubscriptionID && (cur.Status == SubActive || cur.Status == SubPastDue) &&
		(ev.Status == SubCanceled || ev.Status == SubPaused):
		outcome = BillingStale // an old subscription ending does not end the live one
	}
	if outcome == BillingApplied {
		if _, err = t.ExecContext(ctx, `INSERT INTO account_subscriptions(account_id,plan,status,period_ends_at,cancel_at,provider,customer_id,subscription_id,event_at,updated_at)
 VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account_id) DO UPDATE SET plan=excluded.plan,status=excluded.status,period_ends_at=excluded.period_ends_at,
 cancel_at=excluded.cancel_at,provider=excluded.provider,customer_id=CASE WHEN excluded.customer_id='' THEN account_subscriptions.customer_id ELSE excluded.customer_id END,
 subscription_id=excluded.subscription_id,event_at=excluded.event_at,updated_at=excluded.updated_at`,
			account, ev.Plan, ev.Status, ev.PeriodEndsAt, ev.CancelAt, ev.Provider, ev.CustomerID, ev.SubscriptionID, occurred, now.Unix()); err != nil {
			return "", constraint(err)
		}
	}
	if outcome, err = record(account, outcome); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	if s.Wake != nil && outcome == BillingApplied {
		companies, _ := s.strings(ctx, "SELECT id FROM companies WHERE account_id=?", account)
		for _, c := range companies {
			s.Wake(c)
		}
	}
	return outcome, nil
}

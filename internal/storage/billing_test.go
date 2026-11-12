package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

// nodeKey stores an approved (or pending) key for node in company, as approval would.
func nodeKey(t *testing.T, s *Store, company, node, status string, approvedAt int64) {
	t.Helper()
	if _, err := s.DB.Exec("INSERT INTO node_keys(node_id,company_id,name,sign_pub,box_pub,fingerprint,status,created_at,approved_at) VALUES(?,?,?,?,?,?,?,?,?)",
		node, company, "mini-"+node, "sign-"+node, "box-"+node, "fp-"+node, status, s.Now().Unix(), approvedAt); err != nil {
		t.Fatal(err)
	}
}

func planLimited(t *testing.T, err error) {
	t.Helper()
	var limit *PlanLimitError
	if !errors.As(err, &limit) || !errors.Is(err, ErrForbidden) {
		t.Fatalf("want a plan limit, got %v", err)
	}
}

// TestPlanEffective (saas-billing 1.1): no row is no limits; a trial is Pro until it ends; a paid plan holds while
// active or past due; paused and canceled are Free.
func TestPlanEffective(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, c := range []struct {
		sub  Subscription
		want string
	}{
		{Subscription{}, PlanUnlimited},
		{Subscription{Plan: PlanUnlimited, Status: SubActive}, PlanUnlimited},
		{Subscription{Plan: PlanPro, Status: SubTrialing, TrialEndsAt: now.Unix() + 1}, PlanPro},
		{Subscription{Plan: PlanPro, Status: SubTrialing, TrialEndsAt: now.Unix()}, PlanFree},
		{Subscription{Plan: PlanPro, Status: SubActive}, PlanPro},
		{Subscription{Plan: PlanPro, Status: SubPastDue}, PlanPro},
		{Subscription{Plan: PlanPro, Status: SubPaused}, PlanFree},
		{Subscription{Plan: PlanPro, Status: SubCanceled}, PlanFree},
		{Subscription{Plan: PlanFree, Status: SubActive}, PlanFree},
	} {
		if got := c.sub.Effective(now).ID; got != c.want {
			t.Errorf("%+v: %s, want %s", c.sub, got, c.want)
		}
	}
	if Plans[PlanFree].OwnNodes != 1 || Plans[PlanPro].OwnNodes != 5 || Plans[PlanFree].Invitations || !Plans[PlanPro].Invitations || Plans[PlanPro].Credits != 2000 || Plans[PlanPro].PriceUSD != 29 {
		t.Fatal("plans differ from the owner's decisions", Plans)
	}
}

// TestPlanLimits (saas-billing 1.2): with limits on, an account without a plan is not limited; on Free a second own
// node is refused, inviting is refused, an own node over the plan gets no offers, its claims are refused and the
// queued run says why; the owner picks which node takes work; Pro lifts it. With limits off nothing is refused.
func TestPlanLimits(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	register(t, s, "n1", 1)
	register(t, s, "n2", 1)
	nodeKey(t, s, co, "n1", NodeKeyActive, 100)
	nodeKey(t, s, co, "n2", NodeKeyActive, 200)
	nodeKey(t, s, co, "n3", NodeKeyPending, 0)
	s.PlanLimits = true

	// No subscription row (the default account, an account from before billing): no limits.
	if _, _, err := s.CreateInvitation(ctx, co, RoleMember, "owner"); err != nil {
		t.Fatal(err)
	}
	queue(t, s, "r1", "p1", "dev", "")
	if offers, err := s.Offers(ctx, co, "n2", 1, map[string]int{"codex": 1}, nil); err != nil || len(offers) != 1 {
		t.Fatal(offers, err)
	}

	// Free: one own node takes work (the oldest approved), nothing new connects, nobody is invited.
	if err := s.SetAccountPlan(ctx, DefaultAccount, PlanFree); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.CreateInvitation(ctx, co, RoleMember, "owner")
	planLimited(t, err)
	_, err = s.ApproveNodeKey(ctx, co, "n3", "fp-n3")
	planLimited(t, err)
	if offers, err := s.Offers(ctx, co, "n2", 1, map[string]int{"codex": 1}, nil); err != nil || len(offers) != 0 {
		t.Fatal("a node over the plan got offers", offers, err)
	}
	refused(t, func() error { _, err := claimV2(s, "r1", "n2", 1, "1-0-1"); return err }(), true, "plan_limit")
	q, err := s.QueuedWhy(ctx, co, "r1")
	if err != nil || len(q.Nodes) != 2 || q.Nodes[1].Node != "n2" || q.Nodes[1].Reason != QueuedPlanLimit {
		t.Fatalf("%+v %v", q, err)
	}
	b, err := s.AccountBilling(ctx, co)
	if err != nil || b.Effective.ID != PlanFree || len(b.Nodes) != 2 || !b.Nodes[0].TakesWork || b.Nodes[0].ID != "n1" || b.Nodes[1].TakesWork {
		t.Fatalf("%+v %v", b, err)
	}

	// The owner picks n2: it takes work, n1 does not.
	if err = s.PickNode(ctx, co, "n2"); err != nil {
		t.Fatal(err)
	}
	if err = s.PickNode(ctx, co, "n3"); !errors.Is(err, ErrNotFound) {
		t.Fatal("picked a pending node", err)
	}
	refused(t, func() error { _, err := claimV2(s, "r1", "n1", 1, "1-0-2"); return err }(), true, "plan_limit")
	if _, err = claimV2(s, "r1", "n2", 1, "1-0-3"); err != nil {
		t.Fatal("the picked node cannot claim", err)
	}

	// Pro: both nodes work, the third connects, invitations open.
	if err = s.SetAccountPlan(ctx, DefaultAccount, PlanPro); err != nil {
		t.Fatal(err)
	}
	queue(t, s, "r2", "p2", "web", "")
	if _, err = claimV2(s, "r2", "n1", 1, "1-0-4"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApproveNodeKey(ctx, co, "n3", "fp-n3"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CreateInvitation(ctx, co, RoleMember, "owner"); err != nil {
		t.Fatal(err)
	}

	// Limits off (self-hosted, dev): a Free account is not limited.
	if err = s.SetAccountPlan(ctx, DefaultAccount, PlanFree); err != nil {
		t.Fatal(err)
	}
	s.PlanLimits = false
	if _, _, err = s.CreateInvitation(ctx, co, RoleMember, "owner"); err != nil {
		t.Fatal(err)
	}
	queue(t, s, "r3", "p3", "qa", "")
	if _, err = claimV2(s, "r3", "n2", 1, "1-0-5"); err != nil {
		t.Fatal("limits off but refused", err)
	}
}

// TestPlanLimitsAcrossCompanies: the limit counts the own nodes of every company of the account, and a link made
// on Pro is not accepted once the account is on Free.
func TestPlanLimitsAcrossCompanies(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	s.PlanLimits = true
	if err := s.CreateAccount(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	for _, co := range []string{"acme-a", "acme-b"} {
		if err := s.CreateAccountCompany(ctx, "acme", co, co); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateUser(ctx, DefaultAccount, "bob", "bob@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAccountPlan(ctx, "acme", PlanPro); err != nil {
		t.Fatal(err)
	}
	_, token, err := s.CreateInvitation(ctx, "acme-a", RoleMember, "owner")
	if err != nil {
		t.Fatal(err)
	}
	nodeKey(t, s, "acme-a", "a1", NodeKeyActive, 1)
	nodeKey(t, s, "acme-b", "b1", NodeKeyPending, 0)
	if err = s.SetAccountPlan(ctx, "acme", PlanFree); err != nil {
		t.Fatal(err)
	}
	_, err = s.ApproveNodeKey(ctx, "acme-b", "b1", "fp-b1")
	planLimited(t, err)
	_, err = s.AcceptInvitation(ctx, token, "bob")
	planLimited(t, err)
	// Another account is not counted.
	nodeKey(t, s, DefaultCompany, "d1", NodeKeyPending, 0)
	if err = s.SetAccountPlan(ctx, DefaultAccount, PlanFree); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApproveNodeKey(ctx, DefaultCompany, "d1", "fp-d1"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetAccountPlan(ctx, "nobody", PlanPro); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err = s.SetAccountPlan(ctx, "acme", "team"); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
}

// TestBillingEvents (saas-billing 1.1, 1.3): a trial is Pro for 14 days, then Free; provider events apply once, in
// the order they occurred; an account is found by the checkout's custom data, else by its subscription; an old
// subscription ending does not end the live one.
func TestBillingEvents(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	if err := s.CreateAccount(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAccountCompany(ctx, "acme", "acme-co", "Acme"); err != nil {
		t.Fatal(err)
	}
	if err := s.StartTrial(ctx, "acme"); err != nil {
		t.Fatal(err)
	}
	plan := func() string {
		t.Helper()
		b, err := s.AccountBilling(ctx, "acme-co")
		if err != nil {
			t.Fatal(err)
		}
		return b.Effective.ID
	}
	if plan() != PlanPro {
		t.Fatal("no trial")
	}
	if err := s.StartTrial(ctx, "acme"); err != nil { // a second sign-in keeps the first trial
		t.Fatal(err)
	}
	fake.Advance(TrialDays*24*time.Hour + time.Second)
	if plan() != PlanFree {
		t.Fatal("the trial did not end")
	}

	at := s.Now()
	apply := func(ev BillingEvent, want string) {
		t.Helper()
		ev.Provider = "paddle"
		if got, err := s.ApplyBillingEvent(ctx, ev); err != nil || got != want {
			t.Fatalf("%s: %s %v, want %s", ev.EventID, got, err, want)
		}
	}
	created := BillingEvent{EventID: "evt_1", Type: "subscription.created", OccurredAt: at, Account: "acme", CustomerID: "ctm_1", SubscriptionID: "sub_1", Plan: PlanPro, Status: SubActive, PeriodEndsAt: at.Add(30 * 24 * time.Hour).Unix()}
	apply(created, BillingApplied)
	if plan() != PlanPro {
		t.Fatal("paid but not Pro")
	}
	apply(created, BillingDuplicate)
	// Delivered late: occurred before the last applied one.
	apply(BillingEvent{EventID: "evt_0", Type: "subscription.updated", OccurredAt: at.Add(-time.Minute), SubscriptionID: "sub_1", Plan: PlanPro, Status: SubPastDue}, BillingStale)
	// No custom data: found by the subscription.
	apply(BillingEvent{EventID: "evt_2", Type: "subscription.updated", OccurredAt: at.Add(time.Minute), SubscriptionID: "sub_1", Plan: PlanPro, Status: SubActive, CancelAt: at.Add(20 * 24 * time.Hour).Unix()}, BillingApplied)
	sub, err := s.AccountSubscription(ctx, "acme")
	if err != nil || sub.CancelAt == 0 || sub.CustomerID != "ctm_1" || sub.Provider != "paddle" || sub.TrialEndsAt == 0 {
		t.Fatalf("%+v %v", sub, err)
	}
	// A new subscription replaces it; the old one's cancellation arrives later and changes nothing.
	apply(BillingEvent{EventID: "evt_3", Type: "subscription.created", OccurredAt: at.Add(2 * time.Minute), Account: "acme", CustomerID: "ctm_1", SubscriptionID: "sub_2", Plan: PlanPro, Status: SubActive}, BillingApplied)
	apply(BillingEvent{EventID: "evt_4", Type: "subscription.canceled", OccurredAt: at.Add(3 * time.Minute), SubscriptionID: "sub_1", CustomerID: "ctm_1", Plan: PlanPro, Status: SubCanceled}, BillingStale)
	if plan() != PlanPro {
		t.Fatal("an old subscription ended the live one")
	}
	apply(BillingEvent{EventID: "evt_5", Type: "subscription.canceled", OccurredAt: at.Add(4 * time.Minute), SubscriptionID: "sub_2", Plan: PlanPro, Status: SubCanceled}, BillingApplied)
	if plan() != PlanFree {
		t.Fatal("canceled but still Pro")
	}
	apply(BillingEvent{EventID: "evt_6", Type: "subscription.created", OccurredAt: at, Account: "ghost", SubscriptionID: "sub_x", Plan: PlanPro, Status: SubActive}, BillingUnknown)
	apply(BillingEvent{EventID: "evt_7", Type: "transaction.completed", OccurredAt: at, Account: "acme"}, BillingIgnored)
	apply(BillingEvent{EventID: "evt_7", Type: "transaction.completed", OccurredAt: at, Account: "acme"}, BillingDuplicate)
	var n int
	if err = s.DB.QueryRow("SELECT count(*) FROM billing_events").Scan(&n); err != nil || n != 8 {
		t.Fatal(n, err)
	}
}

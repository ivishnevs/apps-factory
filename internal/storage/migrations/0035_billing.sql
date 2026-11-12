-- SaaS billing (saas-billing): an account's subscription and the provider events applied to it. An account
-- without a row has no plan limits (accounts created before billing was switched on, self-hosted and dev Cores);
-- a sign-up on a Core with billing on gets a 14-day Pro trial row. The plan's limits are in Go (storage.Plans).
-- event_at is the occurred_at (Unix nanoseconds) of the last provider event applied: an older event never
-- overwrites a newer one (providers do not deliver in order).
CREATE TABLE account_subscriptions (
 account_id text PRIMARY KEY REFERENCES accounts(id),
 plan text NOT NULL CHECK (plan IN ('free', 'pro', 'unlimited')),
 status text NOT NULL CHECK (status IN ('trialing', 'active', 'past_due', 'paused', 'canceled')),
 trial_ends_at bigint NOT NULL DEFAULT 0,
 period_ends_at bigint NOT NULL DEFAULT 0,
 cancel_at bigint NOT NULL DEFAULT 0,
 provider text NOT NULL DEFAULT '',
 customer_id text NOT NULL DEFAULT '',
 subscription_id text NOT NULL DEFAULT '',
 event_at bigint NOT NULL DEFAULT 0,
 updated_at bigint NOT NULL
);
CREATE INDEX account_subscriptions_subscription ON account_subscriptions(provider, subscription_id) WHERE subscription_id <> '';
CREATE INDEX account_subscriptions_customer ON account_subscriptions(provider, customer_id) WHERE customer_id <> '';

-- Every provider webhook event once (idempotency): a redelivered event is answered without applying it again.
CREATE TABLE billing_events (
 provider text NOT NULL,
 event_id text NOT NULL,
 type text NOT NULL,
 account_id text NOT NULL DEFAULT '',
 occurred_at bigint NOT NULL,
 received_at bigint NOT NULL,
 outcome text NOT NULL,
 PRIMARY KEY (provider, event_id)
);

-- The owner picks which own nodes take work when the account has more than its plan allows (after a downgrade):
-- the most recently picked first, then the oldest approved.
ALTER TABLE node_keys ADD COLUMN picked_at bigint NOT NULL DEFAULT 0;

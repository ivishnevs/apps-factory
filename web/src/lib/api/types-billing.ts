// The account's plan (Core GET /billing, saas-billing). Only owners read it.
export interface Plan { id: "free" | "pro" | "unlimited"; name: string; price_usd: number; own_nodes: number; invitations: boolean; credits: number }

export interface OwnNode { id: string; name: string; company: string; takes_work: boolean; picked: boolean }

export type SubscriptionStatus = "" | "trialing" | "active" | "past_due" | "paused" | "canceled";

export interface Billing {
  // enabled: plan limits apply on this Core (FACTORY_BILLING); provider: payments are configured ("paddle").
  enabled: boolean;
  provider?: string;
  account: string;
  // plan and status are the account's subscription row ("" status: none, no limits); effective is what applies now.
  plan: string;
  status: SubscriptionStatus;
  trial_ends_at?: number;
  period_ends_at?: number;
  cancel_at?: number;
  effective: Plan;
  nodes: OwnNode[];
  members: number;
  plans: Plan[];
  checkout: boolean;
  portal: boolean;
}

// trialDaysLeft is the whole days left of a running trial, null when the account is not on one.
export function trialDaysLeft(b?: Billing, now = Date.now()): number | null {
  if (!b || b.status !== "trialing" || !b.trial_ends_at) return null;
  const left = b.trial_ends_at * 1000 - now;
  return left > 0 ? Math.ceil(left / 86_400_000) : null;
}

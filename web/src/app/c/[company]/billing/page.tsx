"use client";

import { useQueryClient } from "@tanstack/react-query";
import { Check, CreditCard, ExternalLink, Minus, Server, Sparkles, Users } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { useBilling } from "@/components/app/billing";
import { ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { core, useCompanyInfo } from "@/lib/api/client";
import { trialDaysLeft, type Billing, type Plan } from "@/lib/api/types-billing";
import { cn } from "@/lib/utils";

// Billing (saas-billing 1.4): the account's plan, what it uses of its limits, Upgrade (the provider's hosted
// checkout) and Manage (the provider's customer portal: card, invoices, cancel). Only the account's owners see it.
// Core never sees card data; the plan changes when the provider's signed webhook arrives.

const day = (t?: number) => (t ? new Date(t * 1000).toLocaleDateString([], { year: "numeric", month: "short", day: "numeric" }) : "");

function statusLine(b: Billing): { badge: string; tone: string; text: string } {
  const days = trialDaysLeft(b);
  switch (b.status) {
    case "trialing":
      return days !== null
        ? { badge: "Trial", tone: "bg-primary/15 text-primary", text: `Pro for free until ${day(b.trial_ends_at)} (${days} ${days === 1 ? "day" : "days"} left), no card needed. Then Free unless you upgrade.` }
        : { badge: "Trial ended", tone: "bg-muted text-muted-foreground", text: `The Pro trial ended on ${day(b.trial_ends_at)}. You are on Free.` };
    case "active":
      if (b.plan === "unlimited") return { badge: "No limits", tone: "bg-muted text-muted-foreground", text: "This account has no plan limits." };
      return b.cancel_at
        ? { badge: "Cancels", tone: "bg-amber-500/15 text-amber-700 dark:text-amber-300", text: `Pro until ${day(b.cancel_at)}, then Free. Nothing is deleted.` }
        : { badge: "Active", tone: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400", text: b.period_ends_at ? `Renews on ${day(b.period_ends_at)}.` : "" };
    case "past_due":
      return { badge: "Payment due", tone: "bg-red-500/15 text-red-700 dark:text-red-400", text: "The last payment failed. Update your card in Manage billing; Pro stays on while the provider retries." };
    case "paused":
    case "canceled":
      return { badge: b.status === "paused" ? "Paused" : "Canceled", tone: "bg-muted text-muted-foreground", text: "You are on Free. Everything you have stays; what is over the plan waits." };
    default:
      return { badge: "No limits", tone: "bg-muted text-muted-foreground", text: "This account has no plan: nothing is limited." };
  }
}

function PlanCard({ plan, current, action }: { plan: Plan; current: boolean; action?: React.ReactNode }) {
  const rows: [boolean, string][] = [
    [true, plan.own_nodes === 1 ? "1 machine of your own (node)" : `${plan.own_nodes} machines of your own (nodes)`],
    [plan.invitations, plan.invitations ? "Invite your team" : "Just you: no invitations"],
    [plan.credits > 0, plan.credits > 0 ? `${plan.credits.toLocaleString()} cloud credits a month (cloud nodes, coming soon)` : "No cloud nodes"],
    [true, "Agents, companies and history not limited"],
  ];
  return (
    <div className={cn("flex flex-col rounded-2xl border bg-card p-4 sm:p-5", current && "border-primary ring-1 ring-primary")}>
      <div className="flex items-baseline justify-between gap-2">
        <h3 className="font-semibold">{plan.name}</h3>
        {current && <Badge variant="secondary">Your plan</Badge>}
      </div>
      <p className="mt-1"><span className="text-3xl font-semibold tracking-tight">${plan.price_usd}</span><span className="text-sm text-muted-foreground"> / month</span></p>
      <ul className="mt-4 flex-1 space-y-2 text-sm">
        {rows.map(([on, text]) => (
          <li key={text} className={cn("flex items-start gap-2", !on && "text-muted-foreground")}>
            {on ? <Check className="mt-0.5 size-4 shrink-0 text-emerald-600" /> : <Minus className="mt-0.5 size-4 shrink-0" />}{text}
          </li>
        ))}
      </ul>
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

export default function Page() {
  const role = useCompanyInfo().data?.role;
  const billing = useBilling();
  const qc = useQueryClient();
  const [busy, setBusy] = useState("");
  const open = async (what: "checkout" | "portal") => {
    setBusy(what);
    try {
      const { url } = await core<{ url: string }>(`/billing/${what}`, { method: "POST", body: "{}" });
      window.location.assign(url);
    } catch (e) { toast.error(e instanceof Error ? e.message : String(e)); setBusy(""); }
  };
  const pick = async (id: string, name: string) => {
    setBusy(id);
    try {
      await core(`/billing/nodes/${encodeURIComponent(id)}/pick`, { method: "POST", body: "{}" });
      await qc.invalidateQueries({ queryKey: ["billing"] });
      toast.success(`${name} takes work`);
    } catch (e) { toast.error(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(""); }
  };
  if (role && role !== "owner") {
    return (<><PageHeader title="Billing" /><ErrorState error="Only the account's owners see Billing." /></>);
  }
  const b = billing.data;
  return (
    <>
      <PageHeader title="Billing" description="Your plan covers every company of your account. Payments go through Paddle, which handles taxes and invoices; App Factory never sees your card." />
      {billing.error ? <ErrorState error={billing.error} /> : !b ? <LoadingRows rows={3} /> : !b.enabled ? (
        <div className="rounded-2xl border bg-card p-5 text-sm text-muted-foreground">Billing is off on this Core: there are no plan limits.</div>
      ) : (
        <div className="space-y-5">
          <Current b={b} busy={busy} open={open} />
          <Usage b={b} busy={busy} pick={pick} />
          <section>
            <h2 className="mb-3 font-medium">Plans</h2>
            <div className="grid gap-4 sm:grid-cols-2">
              {b.plans.map((p) => (
                <PlanCard key={p.id} plan={p} current={b.effective.id === p.id}
                  action={p.id === "pro" && b.checkout ? <Button className="h-10 w-full" disabled={!!busy} onClick={() => open("checkout")}><Sparkles className="size-4" />{b.status === "trialing" ? "Keep Pro after the trial" : "Upgrade to Pro"}</Button> : undefined} />
              ))}
            </div>
            <p className="mt-3 text-xs text-muted-foreground">Prices in US dollars, taxes added at checkout where they apply. Downgrading keeps everything: members stay, extra nodes stay connected and only stop taking work.</p>
          </section>
        </div>
      )}
    </>
  );
}

function Current({ b, busy, open }: { b: Billing; busy: string; open: (what: "checkout" | "portal") => void }) {
  const s = statusLine(b);
  return (
    <section className="rounded-2xl border bg-card p-4 sm:p-5">
      <div className="flex flex-wrap items-center gap-2">
        <CreditCard className="size-4 text-muted-foreground" />
        <h2 className="font-medium">{b.effective.name}</h2>
        <span className={cn("rounded-full px-2 py-0.5 text-xs font-medium", s.tone)}>{s.badge}</span>
      </div>
      {s.text && <p className="mt-2 text-sm text-muted-foreground">{s.text}</p>}
      {!b.provider && <p className="mt-2 text-sm text-muted-foreground">Payments are not set up on this Core; the operator sets plans.</p>}
      {(b.checkout || b.portal) && (
        <div className="mt-4 flex flex-wrap gap-2">
          {b.checkout && <Button className="h-10" disabled={!!busy} onClick={() => open("checkout")}><Sparkles className="size-4" />Upgrade to Pro · ${b.plans.find((p) => p.id === "pro")?.price_usd}/month</Button>}
          {b.portal && <Button variant="outline" className="h-10" disabled={!!busy} onClick={() => open("portal")}><ExternalLink className="size-4" />Manage billing</Button>}
        </div>
      )}
    </section>
  );
}

function Usage({ b, busy, pick }: { b: Billing; busy: string; pick: (id: string, name: string) => void }) {
  const limit = b.effective.own_nodes;
  const used = b.nodes.length;
  const over = limit >= 0 && used > limit;
  return (
    <section className="rounded-2xl border bg-card p-4 sm:p-5">
      <h2 className="font-medium">Usage</h2>
      <div className="mt-4 grid gap-5 sm:grid-cols-2">
        <div>
          <p className="flex items-center gap-2 text-sm"><Server className="size-4 text-muted-foreground" />Own nodes</p>
          <p className="mt-1 text-2xl font-semibold tabular-nums">{used}<span className="text-base font-normal text-muted-foreground"> / {limit < 0 ? "no limit" : limit}</span></p>
          {limit > 0 && <Progress value={Math.min(100, (used / limit) * 100)} className={cn("mt-2", over && "[&>*]:bg-amber-500")} aria-label="Own nodes used" />}
        </div>
        <div>
          <p className="flex items-center gap-2 text-sm"><Users className="size-4 text-muted-foreground" />People</p>
          <p className="mt-1 text-2xl font-semibold tabular-nums">{b.members}</p>
          <p className="mt-1 text-xs text-muted-foreground">{b.effective.invitations ? "You can invite people." : "Inviting people starts with Pro."}</p>
        </div>
      </div>
      {over && <p className="mt-4 rounded-lg bg-amber-500/10 p-3 text-sm">Your plan runs {limit} {limit === 1 ? "node" : "nodes"} and you have {used}. The others stay connected but take no work: pick the ones that should work.</p>}
      {b.nodes.length > 0 && (
        <ul className="mt-4 divide-y rounded-xl border">
          {b.nodes.map((n) => (
            <li key={n.id} className="flex items-center gap-3 px-4 py-2.5 text-sm">
              <span className="min-w-0 flex-1 truncate"><span className="font-medium">{n.name || n.id.slice(0, 8)}</span> <span className="text-muted-foreground">· {n.company}</span></span>
              {n.takes_work ? <span className="text-xs text-emerald-700 dark:text-emerald-400">Takes work</span> : (
                <>
                  <span className="hidden text-xs text-muted-foreground sm:inline">Waits: over the plan</span>
                  <Button variant="outline" size="sm" className="h-9" disabled={!!busy} onClick={() => pick(n.id, n.name || n.id)}>Make it work</Button>
                </>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

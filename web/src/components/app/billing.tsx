"use client";

import { useQuery } from "@tanstack/react-query";
import { Sparkles } from "lucide-react";
import NextLink from "next/link";
import { toast } from "sonner";
import { ApiError, core, useCompanyInfo } from "@/lib/api/client";
import { trialDaysLeft, type Billing } from "@/lib/api/types-billing";
import { currentCompany, useCompany } from "@/lib/company";

// Billing (saas-billing): the account's plan for its owners, the trial banner and the plan-limit refusals that
// link to the Billing page.

// useBilling reads the plan; only owners may (others never ask).
export function useBilling() {
  const owner = useCompanyInfo().data?.role === "owner";
  return useQuery({ queryKey: ["billing"], queryFn: () => core<Billing>("/billing"), enabled: owner, staleTime: 60_000, retry: false });
}

export const billingHref = (company: string) => `/c/${encodeURIComponent(company)}/billing`;

// isPlanLimit: Core refused because of the account's plan (403, reason plan_limit).
export const isPlanLimit = (e: unknown): e is ApiError => e instanceof ApiError && e.reason === "plan_limit";

// failWithBilling shows a refusal as a toast; a plan limit gets a button to Billing.
export function failWithBilling(e: unknown, company = currentCompany()) {
  const message = e instanceof Error ? e.message : String(e);
  if (isPlanLimit(e) && company) {
    toast.error(message, { action: { label: "Billing", onClick: () => window.location.assign(billingHref(company)) } });
    return;
  }
  toast.error(message);
}

// PlanLimitLink is the link under a plan-limit refusal shown inline.
export function PlanLimitLink({ company }: { company?: string }) {
  const c = company || currentCompany();
  if (!c) return null;
  return <NextLink href={billingHref(c)} className="font-medium underline underline-offset-4">See plans in Billing</NextLink>;
}

// TrialBanner reminds owners how long their Pro trial runs.
export function TrialBanner() {
  const company = useCompany();
  const b = useBilling().data;
  const days = trialDaysLeft(b);
  if (!b?.enabled || days === null) return null;
  return (
    <div className="flex items-center justify-center gap-2 bg-primary/10 px-4 py-1.5 text-center text-sm">
      <Sparkles className="size-4 shrink-0 text-primary" />
      <span>Pro trial: {days} {days === 1 ? "day" : "days"} left.</span>
      <NextLink href={billingHref(company)} className="font-medium underline underline-offset-4">Billing</NextLink>
    </div>
  );
}

"use client";

import { CircleDollarSign, TriangleAlert } from "lucide-react";
import { useState } from "react";
import { EmptyState, ErrorState, PageHeader } from "@/components/app/page";
import { Breakdown, sharePct } from "@/components/app/costs/breakdown";
import { costDays, DayChart } from "@/components/app/costs/day-chart";
import { Segmented } from "@/components/app/costs/segmented";
import { Skeleton } from "@/components/ui/skeleton";
import { useCosts } from "@/lib/api/client";
import { tokens, usd } from "@/lib/format";
import { cn } from "@/lib/utils";

// Costs: the cost ledger (ended runs) over the last 24 hours, 7 or 30 days: the total, spend by day, and
// the spend by agent, model and project with each one's share.
type Period = "24h" | "168h" | "720h";
const periods: { value: Period; label: string; long: string }[] = [
  { value: "24h", label: "24h", long: "the last 24 hours" },
  { value: "168h", label: "7d", long: "the last 7 days" },
  { value: "720h", label: "30d", long: "the last 30 days" },
];

function Stat({ label, value, sub, className }: { label: string; value: React.ReactNode; sub?: React.ReactNode; className?: string }) {
  return (
    <div className={cn("min-w-0 rounded-2xl border bg-card p-4 shadow-xs md:p-5", className)}>
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <p className="mt-1.5 truncate text-2xl font-semibold tabular-nums tracking-tight">{value}</p>
      {sub && <p className="mt-0.5 truncate text-xs tabular-nums text-muted-foreground">{sub}</p>}
    </div>
  );
}

export default function Page() {
  const [since, setSince] = useState<Period>("168h");
  const q = useCosts(since);
  const v = q.data;
  const period = periods.find((p) => p.value === since)!;
  const t = v?.days.total;
  const unpriced = t?.sources?.none ?? 0;
  // The ledger counts uncached input, cache reads and cache writes apart (storage/costs.go): the prompt
  // tokens are all three, and the cache share is the part of them read from the cache.
  const tokIn = (t?.input_tokens ?? 0) + (t?.cache_read_tokens ?? 0) + (t?.cache_write_tokens ?? 0);
  const days = v ? costDays(v.days, new Date(v.since)) : [];

  return (
    <>
      <PageHeader title="Costs" description={`What the company spent in ${period.long}`}
        actions={<Segmented label="Period" value={since} onChange={setSince} options={periods} />} />

      {q.error && <ErrorState error={q.error} />}

      {q.isLoading && (
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-[6.5rem] rounded-2xl" />)}</div>
          <Skeleton className="h-64 rounded-2xl md:h-80" />
          <div className="grid gap-4 lg:grid-cols-3">{[0, 1, 2].map((i) => <Skeleton key={i} className="h-56 rounded-2xl" />)}</div>
        </div>
      )}

      {v && t && (
        <div className={cn("space-y-4 transition-opacity", q.isFetching && !q.isLoading && "opacity-70")}>
          <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
            <Stat label="Total spend" value={usd(t.cost_usd)}
              sub={t.runs > 0 ? `${usd(t.cost_usd / t.runs)} per run` : "no ended runs"} />
            <Stat label="Runs" value={t.runs.toLocaleString()} sub={unpriced > 0 ? `${unpriced} without a price` : t.runs > 0 ? "all priced" : "ended in this period"} />
            <Stat label="Tokens" value={tokens(tokIn + t.output_tokens)} sub={`${tokens(tokIn)} in · ${tokens(t.output_tokens)} out`} />
            <Stat label="Input from cache" value={tokIn > 0 ? sharePct(t.cache_read_tokens, tokIn) : "—"}
              sub={tokIn > 0 ? `${tokens(t.input_tokens + t.cache_write_tokens)} tokens not cached` : "no input yet"} />
          </div>

          {unpriced > 0 && (
            <div className="flex items-start gap-2.5 rounded-xl border border-amber-500/30 bg-amber-500/5 px-4 py-3 text-sm">
              <TriangleAlert className="mt-0.5 size-4 shrink-0 text-amber-500" />
              <span>{unpriced} run{unpriced === 1 ? " has" : "s have"} no price (the model is not in the price table), so the total is a lower bound.</span>
            </div>
          )}

          {t.runs === 0 ? (
            <EmptyState icon={CircleDollarSign} title={`Nothing spent in ${period.long}`}
              hint="Costs appear here when runs end. Pick a longer period to see earlier spend." className="px-6" />
          ) : (
            <>
              <section className="rounded-2xl border bg-card p-4 shadow-xs md:p-5">
                <div className="mb-4 flex items-baseline justify-between gap-2">
                  <h2 className="text-sm font-semibold">Spend by day</h2>
                  <span className="text-xs tabular-nums text-muted-foreground">
                    peak {usd(Math.max(...days.map((d) => d.cost)))}
                  </span>
                </div>
                <DayChart days={days} />
              </section>
              <div className="grid items-start gap-4 lg:grid-cols-3">
                <Breakdown title="By agent" report={v.agents} total={t.cost_usd} avatars />
                <Breakdown title="By model" report={v.models} total={t.cost_usd} />
                <Breakdown title="By project" report={v.projects} total={t.cost_usd} />
              </div>
            </>
          )}
        </div>
      )}
    </>
  );
}

"use client";

import { addDays, format, parseISO, startOfDay } from "date-fns";
import { Bar, BarChart, CartesianGrid, Cell, XAxis, YAxis } from "recharts";
import { ChartContainer, ChartTooltip, type ChartConfig } from "@/components/ui/chart";
import type { CostReport } from "@/lib/api/types";
import { tokens, usd } from "@/lib/format";

const config = { cost: { label: "Cost", color: "var(--color-emerald-500)" } } satisfies ChartConfig;

interface Day { key: string; label: string; cost: number; runs: number; tok: number; today: boolean }

// days: every day from since to today (empty ones too), from the ledger grouped by day ("2006-01-02").
export function costDays(report: CostReport, since: Date): Day[] {
  const by = new Map((report.groups ?? []).map((g) => [g.key, g]));
  const out: Day[] = [];
  const today = format(new Date(), "yyyy-MM-dd");
  for (let d = startOfDay(since); d <= new Date(); d = addDays(d, 1)) {
    const key = format(d, "yyyy-MM-dd");
    const g = by.get(key);
    out.push({ key, label: format(d, "MMM d"), cost: g?.cost_usd ?? 0, runs: g?.runs ?? 0, tok: (g?.input_tokens ?? 0) + (g?.cache_read_tokens ?? 0) + (g?.output_tokens ?? 0), today: key === today });
  }
  return out;
}

export function DayChart({ days }: { days: Day[] }) {
  return (
    <ChartContainer config={config} className="aspect-auto h-48 w-full md:h-64">
      <BarChart data={days} margin={{ top: 8, right: 0, left: 0, bottom: 0 }} barCategoryGap={days.length > 14 ? "18%" : "28%"}>
        <CartesianGrid vertical={false} strokeDasharray="3 3" />
        <XAxis dataKey="label" tickLine={false} axisLine={false} tickMargin={8} minTickGap={18} fontSize={11} />
        <YAxis tickLine={false} axisLine={false} width={44} fontSize={11} tickFormatter={(v: number) => `$${Number.isInteger(v) ? v : v.toFixed(1)}`} />
        <ChartTooltip cursor={{ fill: "var(--muted)", opacity: 0.6 }} content={({ active, payload }) => {
          const d = active && (payload?.[0]?.payload as Day | undefined);
          if (!d) return null;
          return (
            <div className="rounded-lg border bg-background px-3 py-2 text-xs shadow-lg">
              <p className="font-medium">{format(parseISO(d.key), "EEEE, MMM d")}</p>
              <p className="mt-1 text-base font-semibold tabular-nums">{usd(d.cost)}</p>
              <p className="tabular-nums text-muted-foreground">{d.runs} run{d.runs === 1 ? "" : "s"} · {tokens(d.tok)} tokens</p>
            </div>
          );
        }} />
        <Bar dataKey="cost" radius={[4, 4, 1, 1]} maxBarSize={40}>
          {days.map((d) => <Cell key={d.key} fill="var(--color-cost)" fillOpacity={d.today ? 1 : 0.75} />)}
        </Bar>
      </BarChart>
    </ChartContainer>
  );
}

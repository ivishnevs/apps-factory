import { AgentAvatar } from "@/components/app/chat/agent-avatar";
import type { CostGroup, CostReport } from "@/lib/api/types";
import { tokens, usd } from "@/lib/format";
import { cn } from "@/lib/utils";

export const pct = (part: number, whole: number) => (whole > 0 ? `${Math.round((part / whole) * 100)}%` : "0%");

// Breakdown is one grouping (agent, model, project) as a ranked list: each row its cost, share of the
// period's total and a proportion bar scaled to the total.
export function Breakdown({ title, report, total, avatars, limit = 8 }: {
  title: string; report: CostReport; total: number; avatars?: boolean; limit?: number;
}) {
  const groups = [...(report.groups ?? [])].sort((a, b) => b.cost_usd - a.cost_usd);
  const shown = groups.slice(0, limit);
  const rest = groups.slice(limit);
  const restSum = rest.reduce((n, g) => n + g.cost_usd, 0);
  return (
    <section className="rounded-2xl border bg-card p-4 shadow-xs md:p-5">
      <div className="mb-3 flex items-baseline justify-between gap-2">
        <h2 className="text-sm font-semibold">{title}</h2>
        <span className="text-xs text-muted-foreground">{groups.length} {groups.length === 1 ? "entry" : "entries"}</span>
      </div>
      {groups.length === 0 ? (
        <p className="py-6 text-center text-sm text-muted-foreground">No ended runs</p>
      ) : (
        <ol className="space-y-3.5">
          {shown.map((g, i) => <Row key={g.key || i} g={g} total={total} avatar={avatars} rank={i} />)}
          {rest.length > 0 && (
            <li className="flex justify-between text-xs text-muted-foreground">
              <span>{rest.length} more</span><span className="tabular-nums">{usd(restSum)}</span>
            </li>
          )}
        </ol>
      )}
    </section>
  );
}

function Row({ g, total, avatar, rank }: { g: CostGroup; total: number; avatar?: boolean; rank: number }) {
  const name = g.key || "unknown";
  const share = total > 0 ? g.cost_usd / total : 0;
  return (
    <li>
      <div className="flex items-center gap-2.5">
        {avatar && <AgentAvatar name={name} size="sm" />}
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline gap-2">
            <span className={cn("min-w-0 flex-1 truncate text-sm", !g.key && "italic text-muted-foreground")} title={name}>{name}</span>
            <span className="shrink-0 text-sm font-medium tabular-nums">{usd(g.cost_usd)}</span>
          </div>
          <div className="mt-1.5 flex items-center gap-2.5">
            <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted" role="meter" aria-valuemin={0} aria-valuemax={100}
              aria-valuenow={Math.round(share * 100)} aria-label={`${name}: ${pct(g.cost_usd, total)} of the total`}>
              <div className={cn("h-full rounded-full transition-[width] duration-500", rank === 0 ? "bg-emerald-500" : "bg-emerald-500/60")}
                style={{ width: `${Math.max(share * 100, g.cost_usd > 0 ? 1.5 : 0)}%` }} />
            </div>
            <span className="w-24 shrink-0 text-right text-[11px] tabular-nums text-muted-foreground">
              {pct(g.cost_usd, total)} · {g.runs} run{g.runs === 1 ? "" : "s"}
            </span>
          </div>
          <p className="mt-0.5 text-[11px] tabular-nums text-muted-foreground">
            {tokens(g.input_tokens + g.cache_read_tokens)} in · {tokens(g.output_tokens)} out
            {g.cache_read_tokens > 0 && ` · ${pct(g.cache_read_tokens, g.input_tokens + g.cache_read_tokens)} cached`}
          </p>
        </div>
      </div>
    </li>
  );
}

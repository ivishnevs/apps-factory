"use client";

import { CircleDollarSign, Hand, KanbanSquare, Play, Server } from "lucide-react";
import { Stat } from "@/components/app/home/card";
import { LiveRuns, NeedsYou, Nodes, Projects, Team } from "@/components/app/home/sections";
import { ErrorState } from "@/components/app/page";
import { isConversation, isTerminal, needsOwner, useClock, useOverviewData } from "@/components/app/tasks/model";
import { Skeleton } from "@/components/ui/skeleton";
import { useMeta } from "@/lib/api/client";
import type { Overview } from "@/lib/api/types";
import type { BoardTask } from "@/lib/api/types-tasks";
import { usd } from "@/lib/format";
import { cn } from "@/lib/utils";

function greeting(now: number) {
  const h = new Date(now * 1000).getHours();
  return h < 5 ? "Good night" : h < 12 ? "Good morning" : h < 18 ? "Good afternoon" : "Good evening";
}

export default function Page() {
  const { data: ov, error, isFetching } = useOverviewData();
  const { data: meta } = useMeta();
  const now = useClock();

  if (!ov) {
    return (
      <>
        <div className="mb-6"><h1 className="text-2xl font-semibold tracking-tight">Home</h1></div>
        {error ? <ErrorState error={error} /> : <HomeSkeleton />}
      </>
    );
  }
  const stopped = ov.pauses.some((p) => p.scope === "company");
  return (
    <>
      <div className="mb-6 flex items-end justify-between gap-3">
        <div className="min-w-0">
          <p className="text-sm text-muted-foreground">{greeting(now)}</p>
          <h1 className="truncate text-2xl font-semibold tracking-tight">{ov.health.company}</h1>
        </div>
        <span className={cn("inline-flex h-7 shrink-0 items-center gap-1.5 rounded-full border px-2.5 text-xs font-medium",
          stopped ? "border-red-500/40 text-red-600 dark:text-red-400" : "text-muted-foreground")} title={meta?.describe}>
          <span aria-hidden className={cn("size-1.5 rounded-full", stopped ? "bg-red-500" : error ? "bg-amber-500" : "bg-emerald-500", isFetching && !stopped && "animate-pulse")} />
          {stopped ? "Stopped" : error ? "Reconnecting" : meta?.mode === "demo" ? "Demo" : "Live"}
        </span>
      </div>
      {ov.errors && Object.keys(ov.errors).length > 0 && (
        <div className="mb-4"><ErrorState error={`Some data could not be read: ${Object.entries(ov.errors).map(([k, v]) => `${k} (${v})`).join("; ")}`} /></div>
      )}
      <Stats ov={ov} />
      <div className="mt-4 grid gap-4 lg:grid-cols-[minmax(0,1fr)_22rem] lg:items-start">
        <div className="grid min-w-0 gap-4">
          <NeedsYou ov={ov} now={now} />
          <LiveRuns ov={ov} now={now} />
          <Projects ov={ov} />
        </div>
        <div className="grid min-w-0 gap-4">
          <Team ov={ov} />
          <Nodes ov={ov} now={now} />
        </div>
      </div>
    </>
  );
}

function Stats({ ov }: { ov: Overview }) {
  const runs = Object.values(ov.live_runs ?? {});
  const running = runs.filter((r) => r.status === "RUNNING" || r.status === "CANCELLING").length;
  const queued = runs.filter((r) => r.status === "QUEUED" || r.status === "CLAIMED").length;
  const work = (ov.tasks as BoardTask[]).filter((t) => !isConversation(t) && !isTerminal(t.status));
  const forYou = work.filter(needsOwner).length;
  const online = ov.nodes.filter((n) => n.online);
  const slots = online.reduce((n, x) => n + (x.slots ?? 0), 0), used = online.reduce((n, x) => n + x.in_use.runs, 0);
  return (
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <Stat href="/feed" icon={Play} accent="text-emerald-600 dark:text-emerald-400" label="Live runs" value={running} sub={queued ? `${queued} waiting for a node` : "none queued"} />
      <Stat href="/tasks" icon={forYou ? Hand : KanbanSquare} accent={forYou ? "text-amber-600 dark:text-amber-400" : undefined} label="Open tasks" value={work.length}
        sub={forYou ? <span className="text-amber-700 dark:text-amber-300">{forYou} need{forYou === 1 ? "s" : ""} you</span> : `${ov.pending.in_review} in review`} />
      <Stat href="/costs" icon={CircleDollarSign} label="Spent today" value={usd(ov.costs_today.total.cost_usd)} sub={`${ov.costs_today.total.runs} runs`} />
      <Stat href="/nodes" icon={Server} label="Nodes online" value={<>{online.length}<span className="text-base font-normal text-muted-foreground">/{ov.nodes.length}</span></>}
        sub={`${used} of ${slots} slots busy`} />
    </div>
  );
}

function HomeSkeleton() {
  return (
    <div aria-busy="true" aria-label="Loading">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-[104px] rounded-2xl" />)}</div>
      <div className="mt-4 grid gap-4 lg:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="grid gap-4"><Skeleton className="h-64 rounded-2xl" /><Skeleton className="h-48 rounded-2xl" /></div>
        <Skeleton className="h-96 rounded-2xl" />
      </div>
    </div>
  );
}

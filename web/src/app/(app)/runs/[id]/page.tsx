"use client";

import { ArrowDown, ArrowLeft, Bot, CircleStop, Clock, Coins, Cpu, Hash, TriangleAlert } from "lucide-react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { EmptyState, ErrorState } from "@/components/app/page";
import { StatusBadge } from "@/components/app/status";
import { errorCount, transcriptFromEvents } from "@/components/app/run/transcript";
import { Transcript } from "@/components/app/run/transcript-view";
import { useFollow } from "@/components/app/run/use-follow";
import { duration, useNow } from "@/components/app/run/use-now";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useAction, useOverview, useRun, useRunEvents, useStream, useToolCalls } from "@/lib/api/client";
import type { Run, RunEvent } from "@/lib/api/types";
import { tokens, usd, displayId } from "@/lib/format";
import { cn } from "@/lib/utils";

export default function RunPage() {
  const { id } = useParams<{ id: string }>();
  const run = useRun(id);
  const stored = useRunEvents(id);
  const calls = useToolCalls(id);
  const { data: ov } = useOverview();

  const r = run.data;
  const ended = r?.status === "ENDED";
  const [extra, setExtra] = useState<RunEvent[]>([]);
  const [endedLive, setEndedLive] = useState(false);
  // The stream starts after the last stored event, once; reconnects keep the same start and duplicates drop.
  const [after, setAfter] = useState<number | null>(null);
  useEffect(() => {
    if (after === null && stored.data) setAfter(stored.data.reduce((m, e) => Math.max(m, e.seq), 0));
  }, [stored.data, after]);
  const live = !!r && !ended && !endedLive;
  const { connected, ended: streamEnd } = useStream<RunEvent>(live && after !== null ? `/stream/run/${id}?after=${after}` : null, "run",
    (e) => setExtra((xs) => (xs.some((x) => x.seq === e.seq) ? xs : [...xs, e])));
  useEffect(() => {
    if (streamEnd) {
      setEndedLive(true);
      run.refetch();
      calls.refetch();
    }
  }, [streamEnd]); // eslint-disable-line react-hooks/exhaustive-deps

  const events = useMemo(() => {
    const seen = new Set<number>();
    return [...(stored.data ?? []), ...extra].filter((e) => !seen.has(e.seq) && seen.add(e.seq)).sort((a, b) => a.seq - b.seq);
  }, [stored.data, extra]);
  const entries = useMemo(() => {
    const out = transcriptFromEvents(events, calls.data ?? []);
    // An ended run whose terminal event is not among the stored ones still closes with its result.
    if (r?.status === "ENDED" && stored.data && !out.some((e) => e.kind === "end")) {
      out.push({ key: "end", kind: "end", at: (r.finished_at || 0) * 1000, text: r.result, outcome: r.outcome });
    }
    return out;
  }, [events, calls.data, r?.status, r?.finished_at, r?.result, r?.outcome, stored.data]);
  const errors = useMemo(() => errorCount(entries), [entries]);
  const { away, jump } = useFollow(entries.length, live);

  const task = ov?.tasks.find((t) => t.id === r?.spec.task_id);

  if (run.error) return <><Back taskId={undefined} /><ErrorState error={run.error} /></>;
  if (!r) return <RunSkeleton />;

  return (
    <div className="mx-auto max-w-4xl pb-6">
      <Back taskId={r.spec.task_id} />
      <Header run={r} live={live} connected={connected} taskTitle={task?.title} taskVersion={task?.version} errors={errors} />

      <section className="mt-8" aria-label="Transcript">
        <div className="mb-4 flex items-center gap-2">
          <h2 className="text-sm font-semibold">Transcript</h2>
          <span className="text-xs text-muted-foreground tabular-nums">{entries.length} entries</span>
        </div>
        {stored.error ? <ErrorState error={stored.error} />
          : !stored.data ? <TranscriptSkeleton />
          : entries.length === 0 ? <EmptyState icon={Bot} title={live ? "Waiting for the agent" : "No transcript"} hint={live ? "Entries appear here as the agent works." : "This run recorded no events."} />
          : <Transcript entries={entries} live={live} />}
        {live && entries.length > 0 && (
          <div className="mt-5 flex items-center gap-2 text-sm text-muted-foreground">
            <span className="relative flex size-2"><span className="absolute inline-flex size-full animate-ping rounded-full bg-blue-500 opacity-60" /><span className="relative inline-flex size-2 rounded-full bg-blue-500" /></span>
            Working…
          </div>
        )}
      </section>

      {away && (
        <button type="button" onClick={jump}
          className="fixed bottom-[calc(5rem+env(safe-area-inset-bottom))] left-1/2 z-30 flex h-10 -translate-x-1/2 items-center gap-1.5 rounded-full border bg-background/95 px-4 text-sm font-medium shadow-lg backdrop-blur animate-in fade-in-0 slide-in-from-bottom-2 md:bottom-8 md:left-[calc(50%+7.5rem)]">
          <ArrowDown className="size-4" /> Jump to latest
        </button>
      )}
    </div>
  );
}

function Back({ taskId }: { taskId?: string }) {
  return (
    <Link href={taskId ? `/tasks/${taskId}` : "/tasks"}
      className="-ml-2 mb-3 inline-flex h-10 items-center gap-1.5 rounded-md px-2 text-sm text-muted-foreground transition-colors hover:text-foreground">
      <ArrowLeft className="size-4" /> {taskId ? `Task ${taskId}` : "Tasks"}
    </Link>
  );
}

function Header({ run, live, connected, taskTitle, taskVersion, errors }: {
  run: Run; live: boolean; connected: boolean; taskTitle?: string; taskVersion?: number; errors: number;
}) {
  const now = useNow(1000, live);
  const confirm = useConfirm();
  const action = useAction();
  const usage = run.usage ?? [];
  const input = usage.reduce((s, u) => s + u.input, 0);
  const output = usage.reduce((s, u) => s + u.output, 0);
  const cache = usage.reduce((s, u) => s + (u.cache_read ?? 0), 0);
  const hasCost = usage.some((u) => u.cost_usd != null);
  const cost = usage.reduce((s, u) => s + (u.cost_usd ?? 0), 0);
  const models = [...new Set(usage.map((u) => u.model).filter(Boolean))];
  const end = run.finished_at || (live ? now / 1000 : 0);
  const elapsed = run.started_at && end ? duration(end - run.started_at) : "—";
  const taskId = run.spec.task_id;

  const cancel = async () => {
    if (!taskId) return;
    const ok = await confirm({ title: `Cancel ${taskId}?`, description: `${run.spec.agent} stops this run and the task is cancelled. This cannot be undone.`, confirm: "Cancel task", destructive: true });
    if (!ok) return;
    action.mutate({ Kind: "cancel", TaskID: taskId, Version: taskVersion }, {
      onSuccess: () => toast.success(`Cancel requested for ${taskId}`),
      onError: (e) => toast.error(e.message),
    });
  };

  return (
    <header>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-2xl font-semibold tracking-tight">{run.spec.agent}</h1>
            <StatusBadge value={run.outcome || run.status} />
            {live && <LiveDot connected={connected} />}
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            {taskId ? <><Link href={`/tasks/${taskId}`} className="font-medium text-foreground underline-offset-4 hover:underline">{taskId}</Link>{taskTitle && <> · {taskTitle}</>}</> : "No task"}
            <span className="mx-1.5 text-border">/</span>
            <span className="font-mono text-xs" title={run.spec.id}>{displayId(run.spec.id)}</span>
          </p>
        </div>
        {live && taskId && (
          <Button variant="outline" onClick={cancel} disabled={action.isPending}
            className="h-10 border-red-500/30 text-red-600 hover:bg-red-500/10 hover:text-red-700 dark:text-red-400">
            <CircleStop /> Cancel run
          </Button>
        )}
      </div>

      <dl className="mt-5 grid grid-cols-2 overflow-hidden rounded-xl border bg-card sm:grid-cols-4 [&>div]:border-border">
        <Stat icon={Clock} label="Elapsed" value={elapsed} className="border-r border-b sm:border-b-0" />
        <Stat icon={Hash} label="Tokens" value={usage.length ? `${tokens(input)} in · ${tokens(output)} out` : "—"} sub={cache ? `${tokens(cache)} cached` : undefined} className="border-b sm:border-r sm:border-b-0" />
        <Stat icon={Coins} label="Cost" value={hasCost ? usd(cost) : "—"} sub={hasCost ? undefined : "not reported"} className="border-r" />
        <Stat icon={Cpu} label="Model" value={models.join(", ") || "—"} sub={run.spec.provider || undefined} />
      </dl>

      {errors > 0 && (
        <p className="mt-3 inline-flex items-center gap-1.5 rounded-full bg-red-500/10 px-2.5 py-1 text-xs font-medium text-red-700 dark:text-red-300">
          <TriangleAlert className="size-3.5" /> {errors} {errors === 1 ? "error or refusal" : "errors and refusals"}
        </p>
      )}
    </header>
  );
}

function LiveDot({ connected }: { connected: boolean }) {
  return (
    <span className={cn("inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium",
      connected ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300" : "bg-muted text-muted-foreground")}>
      <span className={cn("size-1.5 rounded-full", connected ? "animate-pulse bg-emerald-500" : "bg-muted-foreground/50")} />
      {connected ? "Live" : "Connecting"}
    </span>
  );
}

function Stat({ icon: Icon, label, value, sub, className }: { icon: React.ElementType; label: string; value: string; sub?: string; className?: string }) {
  return (
    <div className={cn("min-w-0 p-3 sm:p-4", className)}>
      <dt className="flex items-center gap-1.5 text-xs text-muted-foreground"><Icon className="size-3.5" />{label}</dt>
      <dd className="mt-1 truncate text-sm font-medium tabular-nums" title={value}>{value}</dd>
      {sub && <dd className="truncate text-xs text-muted-foreground">{sub}</dd>}
    </div>
  );
}

function RunSkeleton() {
  return (
    <div>
      <Skeleton className="mb-4 h-5 w-24" />
      <Skeleton className="h-8 w-48" />
      <Skeleton className="mt-2 h-4 w-64" />
      <Skeleton className="mt-5 h-20 w-full rounded-xl" />
      <div className="mt-8"><TranscriptSkeleton /></div>
    </div>
  );
}

function TranscriptSkeleton() {
  return (
    <div className="space-y-4">
      <Skeleton className="h-4 w-3/4" />
      <Skeleton className="h-4 w-1/2" />
      <Skeleton className="h-28 w-full rounded-xl" />
      <Skeleton className="h-4 w-2/3" />
    </div>
  );
}

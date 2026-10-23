"use client";

import { Hand, Search, X } from "lucide-react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { Suspense, useMemo, useState } from "react";
import { EmptyState, ErrorState, PageHeader } from "@/components/app/page";
import { toneClass } from "@/components/app/status";
import { columnOf, columns, isConversation, needsOwner, projectName, runsByTask, useClock, useOverviewData, type ColumnKey } from "@/components/app/tasks/model";
import { NewTask } from "@/components/app/tasks/new-task";
import { TaskCard } from "@/components/app/tasks/parts";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import type { Task } from "@/lib/api/types";
import type { BoardTask } from "@/lib/api/types-tasks";
import { cn } from "@/lib/utils";

const ALL = "all";
type View = "open" | "you" | ColumnKey;
const views: { key: View; name: string }[] = [
  { key: "open", name: "Open" }, { key: "you", name: "Needs you" }, { key: "running", name: "Running" }, { key: "waiting", name: "Waiting" },
  { key: "ready", name: "Ready" }, { key: "review", name: "In review" }, { key: "backlog", name: "Backlog" }, { key: "done", name: "Done" },
];

// Owner first, then by priority, then the most recently changed.
const order = (a: Task, b: Task) => Number(needsOwner(b)) - Number(needsOwner(a)) || b.priority - a.priority || b.updated_at - a.updated_at;

export default function Page() {
  return <Suspense fallback={<BoardSkeleton />}><Tasks /></Suspense>;
}

function Tasks() {
  const { data: ov, error, isLoading } = useOverviewData();
  const now = useClock();
  const router = useRouter();
  const pathname = usePathname();
  const params = useSearchParams();
  const project = params.get("project") ?? ALL;
  const assignee = params.get("assignee") ?? ALL;
  const view = (params.get("view") as View) ?? "open";
  const [q, setQ] = useState(params.get("q") ?? "");

  const set = (k: string, v: string, def: string) => {
    const p = new URLSearchParams(params.toString());
    if (v === def) p.delete(k); else p.set(k, v);
    const s = p.toString();
    router.replace(s ? `${pathname}?${s}` : pathname, { scroll: false });
  };

  const { filtered, byCol } = useMemo(() => {
    const runs = runsByTask(ov);
    const needle = q.trim().toLowerCase();
    const filtered = ((ov?.tasks ?? []) as BoardTask[])
      .filter((t) => !isConversation(t))
      .filter((t) => project === ALL || t.project_id === project)
      .filter((t) => assignee === ALL || t.assignee === assignee)
      .filter((t) => !needle || [t.id, t.title, t.description, t.assignee].some((f) => f?.toLowerCase().includes(needle)))
      .map((t) => ({ task: t, col: columnOf(t, runs) }))
      .sort((a, b) => order(a.task, b.task));
    const byCol = new Map<ColumnKey, Task[]>(columns.map((c) => [c.key, []]));
    for (const f of filtered) byCol.get(f.col)!.push(f.task);
    return { filtered, byCol };
  }, [ov, project, assignee, q]);

  const count = (v: View) => v === "open" ? filtered.filter((f) => f.col !== "done").length
    : v === "you" ? filtered.filter((f) => f.col !== "done" && needsOwner(f.task)).length : byCol.get(v)?.length ?? 0;
  const list = filtered.filter((f) => view === "open" ? f.col !== "done" : view === "you" ? f.col !== "done" && needsOwner(f.task) : f.col === view);
  const filteredOut = project !== ALL || assignee !== ALL || q.trim() !== "";
  const name = (id: string) => projectName(ov, id);
  const total = count("open"), forYou = count("you");

  return (
    <>
      <PageHeader title="Tasks"
        description={ov ? <>{total} open{forYou > 0 && <> · <span className="font-medium text-amber-700 dark:text-amber-300">{forYou} need{forYou === 1 ? "s" : ""} you</span></>}</> : "Loading…"}
        actions={<NewTask overview={ov} defaultProject={project === ALL ? undefined : project} />} />

      <div className="mb-4 flex flex-col gap-2 sm:flex-row sm:items-center">
        <div className="relative flex-1">
          <Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input type="search" aria-label="Search tasks" placeholder="Search by title, ID or assignee" value={q}
            onChange={(e) => { setQ(e.target.value); }} onBlur={() => set("q", q.trim(), "")}
            className="h-11 pl-9 text-base sm:h-9 sm:text-sm [&::-webkit-search-cancel-button]:hidden" />
          {q && (
            <button type="button" aria-label="Clear search" onClick={() => { setQ(""); set("q", "", ""); }}
              className="absolute top-1/2 right-1 flex size-9 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground hover:text-foreground">
              <X className="size-4" />
            </button>
          )}
        </div>
        <div className="grid grid-cols-2 gap-2 sm:flex">
          <Select value={project} onValueChange={(v) => set("project", v, ALL)}>
            <SelectTrigger aria-label="Project" className="w-full data-[size=default]:h-11 sm:w-40 sm:data-[size=default]:h-9"><SelectValue /></SelectTrigger>
            <SelectContent position="popper">
              <SelectItem value={ALL}>All projects</SelectItem>
              {ov?.status.map((s) => <SelectItem key={s.project.id} value={s.project.id}>{s.project.name}</SelectItem>)}
            </SelectContent>
          </Select>
          <Select value={assignee} onValueChange={(v) => set("assignee", v, ALL)}>
            <SelectTrigger aria-label="Assignee" className="w-full data-[size=default]:h-11 sm:w-40 sm:data-[size=default]:h-9"><SelectValue /></SelectTrigger>
            <SelectContent position="popper">
              <SelectItem value={ALL}>Everyone</SelectItem>
              {ov?.agents.map((a) => <SelectItem key={a.name} value={a.name}>{a.name}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
      </div>

      {error && !ov ? <ErrorState error={error} /> : isLoading || !ov ? <BoardSkeleton /> : (
        <>
          {/* Phone and tablet: a status chip bar over one list. */}
          <div className="lg:hidden">
            <div role="tablist" aria-label="Status" className="-mx-4 mb-4 flex gap-2 overflow-x-auto px-4 pb-1 [scrollbar-width:none] md:-mx-8 md:px-8">
              {views.map((v) => {
                const on = view === v.key, n = count(v.key);
                return (
                  <button key={v.key} role="tab" aria-selected={on} onClick={() => set("view", v.key, "open")}
                    className={cn("inline-flex h-10 shrink-0 items-center gap-1.5 rounded-full border px-3.5 text-sm transition-colors",
                      on ? "border-foreground bg-foreground text-background" : "bg-background text-muted-foreground hover:text-foreground",
                      v.key === "you" && n > 0 && !on && "border-amber-500/40 text-amber-700 dark:text-amber-300")}>
                    {v.key === "you" && <Hand className="size-3.5" />}
                    {v.name}
                    <span className={cn("tabular-nums text-xs", on ? "text-background/70" : "text-muted-foreground")}>{n}</span>
                  </button>
                );
              })}
            </div>
            {list.length === 0 ? <Empty view={view} filtered={filteredOut} /> : (
              <ul className="grid gap-2.5 sm:grid-cols-2">
                {list.map((f) => <li key={f.task.id}><TaskCard task={f.task} project={name(f.task.project_id)} now={now} column={view === "open" || view === "you" ? f.col : undefined} /></li>)}
              </ul>
            )}
          </div>

          {/* Desktop: the Kanban board. */}
          <div className="relative -mx-8 hidden overflow-x-auto px-8 pb-4 lg:block">
            <div className="grid min-w-max auto-cols-[17rem] grid-flow-col gap-4 xl:min-w-0 xl:auto-cols-[minmax(15rem,1fr)]">
              {columns.map((c) => {
                const items = byCol.get(c.key) ?? [];
                return (
                  <section key={c.key} aria-label={c.name} className="flex min-w-0 flex-col rounded-2xl bg-muted/40 p-2 dark:bg-muted/20">
                    <header className="flex items-center gap-2 px-2 pt-1 pb-2.5">
                      <span aria-hidden className={cn("size-2 rounded-full", dot(c.tone))} />
                      <h2 className="text-sm font-medium">{c.name}</h2>
                      <span className="text-xs tabular-nums text-muted-foreground">{items.length}</span>
                    </header>
                    <div className="flex flex-col gap-2">
                      {items.length === 0 ? <p className="rounded-xl border border-dashed px-3 py-6 text-center text-xs text-muted-foreground">No tasks</p>
                        : items.map((t) => <TaskCard key={t.id} task={t} project={name(t.project_id)} now={now} />)}
                    </div>
                  </section>
                );
              })}
            </div>
          </div>
        </>
      )}
    </>
  );
}

// dot: the solid colour of a status tone (the badge tones are tints).
const dot = (tone: string) => ({
  BACKLOG: "bg-zinc-400", READY: "bg-sky-500", REWORK: "bg-amber-500", IN_PROGRESS: "bg-blue-500", IN_REVIEW: "bg-violet-500", DONE: "bg-emerald-500",
} as Record<string, string>)[tone] ?? toneClass(tone);

function Empty({ view, filtered }: { view: View; filtered: boolean }) {
  if (filtered) return <EmptyState icon={Search} title="No matching tasks" hint="Try another search, project or assignee." />;
  if (view === "you") return <EmptyState icon={Hand} title="Nothing needs you" hint="No task waits on an answer, an approval or a decision from you." />;
  return <EmptyState title={view === "open" ? "No open tasks" : "No tasks here"} hint="Create a task, or ask the PM for a plan in Chat." />;
}

function BoardSkeleton() {
  return (
    <div aria-busy="true" aria-label="Loading tasks">
      <div className="mb-4 flex gap-2 lg:hidden">{[0, 1, 2, 3].map((i) => <Skeleton key={i} className="h-10 w-24 rounded-full" />)}</div>
      <div className="grid gap-2.5 sm:grid-cols-2 lg:grid-cols-5">{Array.from({ length: 6 }, (_, i) => <Skeleton key={i} className="h-32 rounded-xl" />)}</div>
    </div>
  );
}

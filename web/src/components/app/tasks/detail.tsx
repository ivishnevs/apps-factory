"use client";

import { format } from "date-fns";
import { ArrowUpRight, CheckCircle2, ChevronRight, CircleDot, Clock, FileText, GitBranch, GitPullRequest, Hand, Loader2, MessageSquareWarning, OctagonAlert, Send } from "lucide-react";
import Link from "next/link";
import { useState } from "react";
import { StatusBadge, toneClass } from "@/components/app/status";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import type { Comment, FeedItem, PRFacts, QueuedReason, Run, Submission as Sub, Task, TaskPark, Verdict } from "@/lib/api/types";
import type { BoardTask } from "@/lib/api/types-tasks";
import { usd, displayId } from "@/lib/format";
import { cn } from "@/lib/utils";
import { isTerminal, runCost, since } from "./model";
import { cleanText, humanize, party as partyOf, taskName, triggerWords, type Lookup, type Party } from "@/lib/humanize";
import { AgentAvatar, PriorityMark } from "./parts";
import { useOwnerAction } from "./use-owner-action";

const stamp = (t?: number | string) => {
  if (!t) return "";
  const d = typeof t === "number" ? new Date(t * 1000) : new Date(t);
  return Number.isNaN(d.getTime()) ? "" : format(d, "MMM d, HH:mm");
};
const span = (s: number) => (s < 60 ? `${Math.max(0, s)}s` : s < 3600 ? `${Math.round(s / 60)}m` : `${Math.floor(s / 3600)}h ${Math.round((s % 3600) / 60)}m`);
const secs = (t: string) => Math.floor(new Date(t).getTime() / 1000);

function Block({ title, children, className, action }: { title: string; children: React.ReactNode; className?: string; action?: React.ReactNode }) {
  return (
    <section aria-label={title} className={className}>
      <div className="mb-2.5 flex items-center gap-2">
        <h2 className="text-sm font-semibold tracking-tight">{title}</h2>
        {action && <div className="ml-auto">{action}</div>}
      </div>
      {children}
    </section>
  );
}

export function Header({ task, project, pr, now, actions }: { task: BoardTask; project: string; pr?: PRFacts; now: number; actions: React.ReactNode }) {
  return (
    <header>
      <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
        <span className="font-mono" title={task.id}>{displayId(task.id)}</span>
        <StatusBadge value={task.status} />
        {task.paused_by && <span className={cn("rounded-full px-2 text-xs font-medium leading-5", toneClass("REWORK"))}>paused by {task.paused_by === "owner" ? "you" : task.paused_by}</span>}
        {task.cancel_requested && !isTerminal(task.status) && <span className={cn("rounded-full px-2 text-xs font-medium leading-5", toneClass("BLOCKED"))}>cancelling</span>}
        <PriorityMark value={task.priority} className="ml-1" />
      </div>
      <div className="mt-2 flex items-start justify-between gap-4">
        <h1 className="min-w-0 text-2xl font-semibold leading-tight tracking-tight text-balance wrap-anywhere sm:text-[28px]">{task.title}</h1>
        {actions}
      </div>
      <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
        <span className="inline-flex min-w-0 items-center gap-2"><AgentAvatar name={task.assignee} size="sm" /><span className="truncate">{task.assignee || "Unassigned"}</span></span>
        <Link href={`/tasks?project=${encodeURIComponent(task.project_id)}`} className="text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">{project}</Link>
        <span className="text-muted-foreground">Updated {since(task.updated_at, now) === "now" ? "just now" : `${since(task.updated_at, now)} ago`}</span>
      </div>
      {(pr || task.openspec_change) && (
        <div className="mt-3 flex flex-wrap gap-2">
          {pr && (
            pr.url ? (
              <a href={pr.url} target="_blank" rel="noreferrer" className="inline-flex h-9 items-center gap-1.5 rounded-full border px-3 text-sm transition-colors hover:bg-accent">
                <GitPullRequest className={cn("size-4", pr.state === "merged" ? "text-violet-500" : pr.state === "closed" ? "text-red-500" : "text-emerald-500")} />
                PR #{pr.number}<span className="text-muted-foreground">{pr.state}</span><ArrowUpRight className="size-3.5 text-muted-foreground" />
              </a>
            ) : <span className="inline-flex h-9 items-center gap-1.5 rounded-full border px-3 text-sm"><GitPullRequest className="size-4" />PR #{pr.number} · {pr.state}</span>
          )}
          {task.openspec_change && (
            <span className="inline-flex h-9 max-w-full min-w-0 items-center gap-1.5 rounded-full border px-3 text-sm" title={task.openspec_change}><FileText className="size-4 shrink-0 text-muted-foreground" />
              <span className="shrink-0 text-muted-foreground">OpenSpec</span><span className="min-w-0 truncate font-mono text-xs">{task.openspec_change}</span></span>
          )}
        </div>
      )}
    </header>
  );
}

// Why: the verdict on why the task is where it is and who clears it — the first thing the owner reads,
// in product language (lib/humanize).
export function Why({ why, queued, task, look, now }: { why: Verdict; queued?: QueuedReason; task: Task; look?: Lookup; now?: number }) {
  const done = isTerminal(task.status);
  const h = humanize({ ...why, status: task.status }, look, now);
  // A queued run explains itself with its node reason.
  const q = queued ? humanize({ reason: queued.reason, detail: queued.detail, status: task.status }, look, now) : undefined;
  const you = h.you && !done;
  const live = h.live && !queued;
  const Icon = done ? CheckCircle2 : you ? Hand : live ? Loader2 : queued || h.queued ? Clock : CircleDot;
  const triggers = why.pending_triggers ?? [];
  const msg = /\bm-[\w-]+/.exec(why.clearer?.action ?? "")?.[0];
  const headline = done ? h.headline : queued ? "Waiting for a node" : h.headline;
  const detail = done ? undefined : q ? q.detail : h.detail;
  return (
    <section aria-label="Status" className={cn("rounded-2xl border p-4 sm:p-5",
      you ? "border-amber-500/40 bg-amber-500/[0.06]" : live ? "border-blue-500/25 bg-blue-500/[0.04]" : "bg-muted/30")}>
      <div className="flex items-start gap-3">
        <span className={cn("flex size-9 shrink-0 items-center justify-center rounded-full",
          you ? "bg-amber-500/15 text-amber-700 dark:text-amber-300" : live ? "bg-blue-500/15 text-blue-600 dark:text-blue-300" : done ? toneClass(task.status) : "bg-muted text-muted-foreground")}>
          <Icon aria-hidden className={cn("size-[18px]", live && "animate-spin [animation-duration:2.5s]")} />
        </span>
        <div className="min-w-0 flex-1">
          <p className={cn("text-xs font-medium", you ? "text-amber-700 dark:text-amber-300" : "text-muted-foreground")}>{done ? "Result" : you ? "Needs you" : "Status"}</p>
          <p className="mt-0.5 text-base font-semibold leading-snug text-balance wrap-anywhere">{headline}</p>
          {done && task.result && <p className="mt-1 text-sm text-muted-foreground whitespace-pre-wrap wrap-anywhere">{cleanText(task.result, look)}</p>}
          {detail && <p className="mt-1 text-sm text-muted-foreground wrap-anywhere">{detail}</p>}
          {!done && (h.who && !h.who.you || h.next) && (
            <dl className="mt-3 grid gap-2 text-sm sm:grid-cols-[auto_minmax(0,1fr)] sm:gap-x-4">
              {h.who && !h.who.you && (
                <>
                  <dt className="text-muted-foreground">Waiting on</dt>
                  <dd className="-mt-1.5 min-w-0 sm:mt-0"><PartyChip party={h.who} /></dd>
                </>
              )}
              {h.next && (
                <>
                  <dt className="text-muted-foreground">{you ? "What to do" : "Next"}</dt>
                  <dd className={cn("-mt-1.5 min-w-0 wrap-anywhere sm:mt-0", you && "font-medium")}>{h.next}</dd>
                </>
              )}
            </dl>
          )}
          {!done && triggers.length > 0 && (
            <p className="mt-3 text-xs text-muted-foreground">
              When it runs next, it picks up {triggers.map((t) => triggerWords(t)).join(", ")}.
            </p>
          )}
          {you && msg && (
            <Button asChild size="sm" className="mt-3 h-10 sm:h-8"><Link href={`/inbox?m=${encodeURIComponent(msg)}`}>Open in inbox<ChevronRight /></Link></Button>
          )}
        </div>
      </div>
    </section>
  );
}

function PartyChip({ party }: { party: Party }) {
  const inner = (
    <>
      {party.agent && <AgentAvatar name={party.agent} size="xs" />}
      <span className="min-w-0 truncate">{party.label}</span>
      {party.href && <ChevronRight aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />}
    </>
  );
  const cls = "inline-flex max-w-full items-center gap-1.5 font-medium";
  return party.href
    ? <Link href={party.href} className={cn(cls, "rounded-md underline-offset-4 hover:underline")}>{inner}</Link>
    : <span className={cls}>{inner}</span>;
}

export function Description({ text }: { text: string }) {
  return (
    <Block title="Description">
      {text.trim() ? <div className="text-[15px] leading-relaxed whitespace-pre-wrap wrap-anywhere sm:text-sm">{text}</div>
        : <p className="text-sm text-muted-foreground">No description.</p>}
    </Block>
  );
}

export function Submission({ sub, look }: { sub: Sub; look?: Lookup }) {
  return (
    <Block title="Submission">
      <div className="rounded-xl border bg-card p-4">
        <p className="text-sm whitespace-pre-wrap wrap-anywhere">{cleanText(sub.summary, look)}</p>
        {(sub.commit || (sub.checks?.length ?? 0) > 0) && (
          <div className="mt-3 flex flex-wrap gap-2">
            {sub.commit && <span className="inline-flex items-center gap-1.5 rounded-md bg-muted px-2 py-1 font-mono text-xs"><GitBranch className="size-3.5" />{sub.commit.slice(0, 12)}</span>}
            {sub.checks?.map((c) => <span key={c} className="inline-flex items-center gap-1.5 rounded-md bg-emerald-500/10 px-2 py-1 font-mono text-xs text-emerald-700 dark:text-emerald-300"><CheckCircle2 className="size-3.5" />{c}</span>)}
          </div>
        )}
        {(sub.artifacts?.length ?? 0) > 0 && (
          <ul className="mt-3 space-y-1 text-sm">
            {sub.artifacts!.map((a) => <li key={a.id + a.version} className="flex items-center gap-2"><FileText className="size-4 text-muted-foreground" />{a.name ?? a.id}<span className="text-xs text-muted-foreground">{a.version}</span></li>)}
          </ul>
        )}
      </div>
    </Block>
  );
}

export function ReviewNotes({ task, look }: { task: Task; look?: Lookup }) {
  if (!task.review_feedback && !task.block_reason) return null;
  return (
    <div className="grid gap-3">
      {task.review_feedback && (
        <div className="rounded-xl border border-amber-500/30 bg-amber-500/[0.05] p-4">
          <p className="mb-1.5 flex items-center gap-2 text-sm font-semibold"><MessageSquareWarning className="size-4 text-amber-600 dark:text-amber-400" />Review feedback</p>
          <p className="text-sm whitespace-pre-wrap wrap-anywhere">{cleanText(task.review_feedback, look)}</p>
        </div>
      )}
      {task.block_reason && (
        <div className="rounded-xl border border-red-500/30 bg-red-500/[0.05] p-4">
          <p className="mb-1.5 flex items-center gap-2 text-sm font-semibold"><OctagonAlert className="size-4 text-red-600 dark:text-red-400" />Blocked</p>
          <p className="text-sm whitespace-pre-wrap wrap-anywhere">{cleanText(task.block_reason, look)}</p>
        </div>
      )}
    </div>
  );
}

export function Comments({ taskId, comments, now, look }: { taskId: string; comments: Comment[]; now: number; look?: Lookup }) {
  const [text, setText] = useState("");
  const { run, pending } = useOwnerAction();
  const send = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!text.trim()) return;
    const res = await run({ Kind: "comment", TaskID: taskId, Text: text.trim() }, { title: "Post this comment?", confirm: "Comment" });
    if (res !== null) setText("");
  };
  return (
    <Block title={`Comments${comments.length ? ` · ${comments.length}` : ""}`}>
      {comments.length > 0 && (
        <ol className="mb-3 space-y-3">
          {comments.map((c) => (
            <li key={c.id} className="flex gap-3">
              <AgentAvatar name={c.author} size="sm" className="mt-0.5" />
              <div className={cn("min-w-0 flex-1 rounded-xl rounded-tl-sm px-3.5 py-2.5", c.author === "owner" ? "bg-foreground/[0.06]" : "bg-muted/60")}>
                <p className="flex min-w-0 items-baseline gap-2 text-xs"><span className="min-w-0 truncate font-semibold">{c.author === "owner" ? "You" : c.author}</span><span className="text-muted-foreground" title={stamp(c.created_at)}>{since(c.created_at, now)}</span></p>
                <p className="mt-0.5 text-sm whitespace-pre-wrap wrap-anywhere">{cleanText(c.text, look)}</p>
              </div>
            </li>
          ))}
        </ol>
      )}
      <form onSubmit={send} className="flex items-end gap-2">
        <label htmlFor="comment" className="sr-only">Add a comment</label>
        <Textarea id="comment" value={text} onChange={(e) => setText(e.target.value)} rows={1} placeholder="Add a comment for the team…"
          onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) send(e); }}
          className="max-h-40 min-h-11 flex-1 resize-none text-base md:min-h-10 md:text-sm" />
        <Button type="submit" size="icon" className="size-11 md:size-10" aria-label="Post comment" disabled={!text.trim() || pending}><Send /></Button>
      </form>
    </Block>
  );
}

// Event type in words: "task.trigger.dependency_done" → "dependency done".
const eventName = (t: string) => {
  const parts = t.split(".");
  const last = parts.slice(parts[0] === "task" ? 1 : 0).filter((p) => p !== "trigger").join(" ");
  return last.replace(/_/g, " ");
};

export function Timeline({ events, now, look }: { events: FeedItem[]; now: number; look?: Lookup }) {
  if (events.length === 0) return null;
  const list = [...events].sort((a, b) => b.sequence - a.sequence);
  return (
    <Block title="Activity">
      <ol className="relative ml-3 border-l">
        {list.map((e) => (
          <li key={e.event_id} className="relative pb-4 pl-5 last:pb-0">
            <span aria-hidden className={cn("absolute top-1.5 -left-[5px] size-2.5 rounded-full border-2 border-background", e.attention ? "bg-amber-500" : "bg-muted-foreground/40")} />
            <p className="text-sm leading-snug wrap-anywhere">
              <span className="font-medium">{e.actor === "owner" ? "You" : e.actor === "system" || (!e.actor && !e.agent) ? "App Factory" : e.actor || e.agent}</span>{" "}
              <span className="text-muted-foreground">{eventName(e.type)}</span>
            </p>
            {e.summary && <p className="mt-0.5 text-sm text-muted-foreground wrap-anywhere">{cleanText(e.summary, look)}</p>}
            <p className="mt-0.5 text-xs text-muted-foreground/80" title={stamp(e.created_at)}>
              {since(secs(e.created_at), now) === "now" ? "just now" : `${since(secs(e.created_at), now)} ago`}
              {e.run_id && <> · <Link href={`/runs/${encodeURIComponent(e.run_id)}`} className="underline-offset-4 hover:text-foreground hover:underline" title={e.run_id}>View run</Link></>}
            </p>
          </li>
        ))}
      </ol>
    </Block>
  );
}

export function DetailsList({ task, parent, cost, now, look }: { task: Task; parent?: string; cost: number; now: number; look?: Lookup }) {
  const rows: [string, React.ReactNode][] = [];
  rows.push(["Created", <span key="c" title={stamp(task.created_at)}>{since(task.created_at, now)} ago by {task.created_by === "owner" ? "you" : task.created_by}</span>]);
  if (parent) rows.push(["Parent", <TaskLink key="p" id={parent} look={look} />]);
  if (task.dependencies?.length) rows.push(["Depends on", <span key="d" className="flex flex-col items-end gap-1.5">{task.dependencies.map((d) => <TaskLink key={d} id={d} look={look} />)}</span>]);
  if (task.blocked_on?.length) rows.push(["Blocked on", <span key="b" className="flex flex-col items-end gap-1.5">{task.blocked_on.map((d) => <TaskLink key={d} id={d} look={look} />)}</span>]);
  if (task.implementer) rows.push(["Implementer", task.implementer]);
  if (task.reviewer) rows.push(["Reviewer", task.reviewer]);
  if (task.model) rows.push(["Model", <span key="m" className="font-mono text-xs">{task.model}</span>]);
  rows.push(["Attempt", String(task.attempt)]);
  if (task.fail_streak) rows.push(["Failures in a row", String(task.fail_streak)]);
  rows.push(["Cost", usd(cost)]);
  return (
    <Block title="Details">
      <dl className="divide-y rounded-xl border bg-card text-sm">
        {rows.map(([k, v]) => (
          <div key={k} className="flex min-h-11 items-center justify-between gap-4 px-4 py-2">
            <dt className="shrink-0 text-muted-foreground">{k}</dt>
            <dd className="min-w-0 text-right wrap-anywhere">{v}</dd>
          </div>
        ))}
      </dl>
    </Block>
  );
}

function TaskLink({ id, look }: { id: string; look?: Lookup }) {
  const t = look?.(id);
  return (
    <Link href={`/tasks/${encodeURIComponent(id)}`} title={t?.title ?? id}
      className={cn("inline-block max-w-full truncate align-bottom underline-offset-4 hover:underline", !t && "font-mono text-xs")}>
      {t ? taskName(id, look) : displayId(id)}
    </Link>
  );
}

export function Attempts({ runs, now }: { runs: Run[]; now: number }) {
  const list = [...runs].reverse();
  return (
    <Block title={`Attempts${runs.length ? ` · ${runs.length}` : ""}`}>
      {list.length === 0 ? <p className="rounded-xl border border-dashed px-4 py-5 text-center text-sm text-muted-foreground">No run yet.</p> : (
        <ul className="divide-y rounded-xl border bg-card">
          {list.map((r, i) => {
            const live = !r.finished_at && r.status !== "ENDED";
            const dur = r.started_at ? `${live ? "running " : "took "}${span((r.finished_at || now) - r.started_at)}` : "";
            const cost = runCost(r);
            return (
              <li key={r.spec.id}>
                <Link href={`/runs/${encodeURIComponent(r.spec.id)}`} className="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-muted/50 focus-visible:bg-muted/60 focus-visible:outline-none">
                  <AgentAvatar name={r.spec.agent} size="sm" className="mt-0.5" />
                  <span className="min-w-0 flex-1">
                    <span className="flex flex-wrap items-center gap-1.5">
                      <span className="text-sm font-medium" title={r.spec.id}>Attempt {list.length - i}</span>
                      <StatusBadge value={live ? r.status : r.outcome || r.status} className="h-5 px-1.5 text-[11px]" />
                    </span>
                    {r.result && <span className="mt-1 line-clamp-2 text-xs text-muted-foreground wrap-anywhere">{cleanText(r.result)}</span>}
                    <span className="mt-1 block text-xs text-muted-foreground tabular-nums">
                      {r.started_at ? `${stamp(r.started_at)}${dur ? ` · ${dur}` : ""}` : "not started"}{cost > 0 && ` · ${usd(cost)}`}
                    </span>
                  </span>
                  <ChevronRight aria-hidden className="mt-1 size-4 shrink-0 text-muted-foreground/60" />
                </Link>
              </li>
            );
          })}
        </ul>
      )}
    </Block>
  );
}

export function Children({ tasks }: { tasks: Task[] }) {
  const done = tasks.filter((t) => t.status === "DONE").length;
  return (
    <Block title={`Subtasks · ${done}/${tasks.length}`}>
      <div className="mb-2 h-1 overflow-hidden rounded-full bg-muted"><div className="h-full rounded-full bg-emerald-500 transition-[width]" style={{ width: `${(done / tasks.length) * 100}%` }} /></div>
      <ul className="divide-y rounded-xl border bg-card">
        {tasks.map((t) => (
          <li key={t.id}>
            <Link href={`/tasks/${encodeURIComponent(t.id)}`} title={t.title} className="flex min-h-12 min-w-0 items-center gap-2.5 px-4 py-2 transition-colors hover:bg-muted/50 focus-visible:bg-muted/60 focus-visible:outline-none">
              <span className={cn("min-w-0 flex-1 truncate text-sm", isTerminal(t.status) && "text-muted-foreground")}>{t.title}</span>
              <StatusBadge value={t.status} className="h-5 shrink-0 px-1.5 text-[11px]" />
            </Link>
          </li>
        ))}
      </ul>
    </Block>
  );
}

const parkKind: Record<string, string> = {
  children: "Waiting for subtasks", escalation: "Escalation", inspect: "Inspection", ask: "Question", block: "Blocked", wait: "Sleeping",
};

export function Parks({ parks, now, look }: { parks: TaskPark[]; now: number; look?: Lookup }) {
  return (
    <Block title="Holds">
      <ul className="divide-y rounded-xl border bg-card">
        {parks.map((p) => (
          <li key={p.id} className="px-4 py-3">
            <p className="flex items-center gap-2 text-sm">
              <span className="font-medium">{parkKind[p.kind] ?? p.kind}</span>
              <span className={cn("rounded-full px-1.5 text-[11px] font-medium leading-5", p.cleared_at ? toneClass("DONE") : toneClass("REWORK"))}>{p.cleared_at ? "cleared" : p.status.toLowerCase()}</span>
              <span className="ml-auto text-xs text-muted-foreground">{since(p.created_at, now)} ago</span>
            </p>
            {p.reason && <p className="mt-1 text-sm text-muted-foreground wrap-anywhere">{cleanText(p.reason, look)}</p>}
            {!p.cleared_at && partyOf(p.who, look) && <p className="mt-1 text-xs text-muted-foreground">Waiting on {partyOf(p.who, look)!.label}</p>}
          </li>
        ))}
      </ul>
    </Block>
  );
}

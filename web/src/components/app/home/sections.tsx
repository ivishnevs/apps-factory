"use client";

import { format } from "date-fns";
import { AlertTriangle, Bell, CheckCircle2, ChevronRight, CirclePause, MessageCircleQuestion, ShieldCheck } from "lucide-react";
import Link from "next/link";
import { toneClass } from "@/components/app/status";
import { clearerName, elapsed, isConversation, reasonLabel, runCost, since } from "@/components/app/tasks/model";
import { AgentAvatar } from "@/components/app/tasks/parts";
import type { Agent, Message, Overview, Run } from "@/lib/api/types";
import type { BoardTask } from "@/lib/api/types-tasks";
import { usd } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Row, Section } from "./card";

const clock = (t?: number) => (t ? format(new Date(t * 1000), "HH:mm") : "");

// What an inbox message asks of the owner.
function kindOf(m: Message) {
  if (m.escalation_reason) return { label: "Escalation", icon: AlertTriangle, tone: "bg-red-500/12 text-red-600 dark:text-red-400" };
  if (m.approval_id) return { label: "Approval", icon: ShieldCheck, tone: "bg-violet-500/12 text-violet-600 dark:text-violet-400" };
  if (m.message_type === "question") return { label: "Question", icon: MessageCircleQuestion, tone: "bg-sky-500/12 text-sky-600 dark:text-sky-400" };
  return { label: "Update", icon: Bell, tone: "bg-muted text-muted-foreground" };
}
const kindRank = (m: Message) => (m.escalation_reason ? 0 : m.approval_id ? 1 : m.message_type === "question" ? (m.blocking ? 2 : 3) : 4);

export function NeedsYou({ ov, now }: { ov: Overview; now: number }) {
  const items = [...ov.inbox].sort((a, b) => kindRank(a) - kindRank(b) || b.created_at - a.created_at);
  const shown = items.slice(0, 5);
  const p = ov.pending;
  const counts = [
    { label: "Questions", n: p.inbox }, { label: "Approvals", n: p.approvals }, { label: "Alerts", n: p.alerts }, { label: "To inspect", n: p.inspect },
  ].filter((c) => c.n > 0);
  const pauses = ov.pauses.filter((x) => x.scope !== "company");
  if (items.length === 0 && pauses.length === 0) {
    return (
      <Section title="Needs you">
        <div className="flex items-center gap-3 border-t px-4 py-5 sm:px-5">
          <span className="flex size-9 items-center justify-center rounded-full bg-emerald-500/12 text-emerald-600 dark:text-emerald-400"><CheckCircle2 className="size-5" /></span>
          <div>
            <p className="text-sm font-medium">All clear</p>
            <p className="text-xs text-muted-foreground">No questions, approvals or alerts are waiting for you.</p>
          </div>
        </div>
      </Section>
    );
  }
  return (
    <Section title="Needs you" count={items.length} href="/inbox" linkLabel="Open inbox" tone={items.length ? "attention" : undefined}>
      {counts.length > 0 && (
        <div className="flex flex-wrap gap-1.5 px-4 pb-3 sm:px-5">
          {counts.map((c) => (
            <span key={c.label} className="rounded-full bg-muted px-2.5 py-0.5 text-xs text-muted-foreground"><span className="font-semibold tabular-nums text-foreground">{c.n}</span> {c.label.toLowerCase()}</span>
          ))}
        </div>
      )}
      <ul>
        {shown.map((m) => {
          const k = kindOf(m);
          return (
            <li key={m.id}>
              <Row href={`/inbox?m=${encodeURIComponent(m.id)}`} className="items-start">
                <span className={cn("mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg", k.tone)}><k.icon className="size-4" aria-hidden /></span>
                <span className="min-w-0 flex-1">
                  <span className="line-clamp-2 text-sm leading-snug">{m.body}</span>
                  <span className="mt-1 flex flex-wrap items-center gap-x-1.5 text-xs text-muted-foreground">
                    <span className="font-medium text-foreground/80">{k.label}</span>
                    <span aria-hidden>·</span><span>{m.sender}</span>
                    {m.task_id && <><span aria-hidden>·</span><span className="font-mono">{m.task_id}</span></>}
                    <span aria-hidden>·</span><span className="tabular-nums">{since(m.created_at, now)}</span>
                    {m.blocking && <span className="rounded-full bg-amber-500/15 px-1.5 text-[11px] font-medium text-amber-700 dark:text-amber-300">blocking</span>}
                  </span>
                </span>
                <ChevronRight aria-hidden className="mt-1.5 size-4 shrink-0 text-muted-foreground/60" />
              </Row>
            </li>
          );
        })}
        {items.length > shown.length && (
          <li><Row href="/inbox" className="min-h-11 justify-center text-xs font-medium text-muted-foreground">{items.length - shown.length} more in the inbox</Row></li>
        )}
        {pauses.map((x) => (
          <li key={`${x.scope}:${x.target}`}>
            <Row href="/nodes" className="items-start">
              <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg bg-amber-500/12 text-amber-600 dark:text-amber-400"><CirclePause className="size-4" aria-hidden /></span>
              <span className="min-w-0 flex-1">
                <span className="block text-sm leading-snug">Paused {x.scope.replace("_", " ")} <span className="font-medium">{x.target}</span></span>
                <span className="mt-1 block text-xs text-muted-foreground">
                  {x.reason || "no reason given"} · by {x.actor === "owner" ? "you" : x.actor}{x.until ? ` · until ${clock(x.until)}` : ""}
                </span>
              </span>
              <ChevronRight aria-hidden className="mt-1.5 size-4 shrink-0 text-muted-foreground/60" />
            </Row>
          </li>
        ))}
      </ul>
    </Section>
  );
}

export function LiveRuns({ ov, now }: { ov: Overview; now: number }) {
  const tasks = new Map(ov.tasks.map((t) => [t.id, t as BoardTask]));
  const queuedWhy = new Map(ov.status.flatMap((s) => s.runs).filter((r) => r.queued).map((r) => [r.id, r.queued!]));
  const runs = Object.values(ov.live_runs ?? {}).filter((r) => r.status !== "ENDED")
    .sort((a, b) => Number(a.status === "QUEUED") - Number(b.status === "QUEUED") || (a.started_at || 0) - (b.started_at || 0));
  return (
    <Section title="Live runs" count={runs.length} href="/feed" linkLabel="Activity">
      {runs.length === 0 ? <p className="border-t px-4 py-6 text-center text-sm text-muted-foreground sm:px-5">No agent is working right now.</p> : (
        <ul>
          {runs.map((r) => <li key={r.spec.id}><RunRow run={r} task={r.spec.task_id ? tasks.get(r.spec.task_id) : undefined} queued={queuedWhy.get(r.spec.id)?.detail} now={now} /></li>)}
        </ul>
      )}
    </Section>
  );
}

function RunRow({ run, task, queued, now }: { run: Run; task?: BoardTask; queued?: string; now: number }) {
  const waiting = run.status === "QUEUED" || run.status === "CLAIMED";
  const cost = runCost(run);
  const what = task ? (isConversation(task) ? "Chat with you" : task.title) : run.spec.task_id ?? "Run";
  return (
    <Row href={`/runs/${encodeURIComponent(run.spec.id)}`}>
      <span className="relative">
        <AgentAvatar name={run.spec.agent} size="md" />
        <span aria-hidden className={cn("absolute -right-0.5 -bottom-0.5 size-3 rounded-full border-2 border-card", waiting ? "bg-sky-400" : "bg-emerald-500")}>
          {!waiting && <span className="absolute inset-0 animate-ping rounded-full bg-emerald-500/60 [animation-duration:2s]" />}
        </span>
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-baseline gap-1.5 text-sm">
          <span className="shrink-0 font-medium">{run.spec.agent}</span>
          {task && !isConversation(task) && <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">{task.id.slice(0, 12)}</span>}
        </span>
        <span className="block truncate text-xs text-muted-foreground">
          {waiting ? <span className="text-sky-700 dark:text-sky-300">Waiting for a node{queued ? ` · ${queued}` : ""}</span> : what}
        </span>
      </span>
      <span className="shrink-0 text-right">
        <span className="block font-mono text-sm tabular-nums">{waiting ? "—" : elapsed(run.started_at, now)}</span>
        <span className="block text-xs tabular-nums text-muted-foreground">{cost > 0 ? usd(cost) : waiting ? "queued" : ""}</span>
      </span>
    </Row>
  );
}

// Order and colour of the status segments of a project's bar.
const segments: { key: string; label: string; color: string }[] = [
  { key: "IN_PROGRESS", label: "in progress", color: "bg-blue-500" }, { key: "IN_REVIEW", label: "in review", color: "bg-violet-500" },
  { key: "REWORK", label: "rework", color: "bg-amber-500" }, { key: "BLOCKED", label: "blocked", color: "bg-red-500" },
  { key: "READY", label: "ready", color: "bg-sky-400" }, { key: "BACKLOG", label: "backlog", color: "bg-zinc-300 dark:bg-zinc-600" },
  { key: "DONE", label: "done", color: "bg-emerald-500" }, { key: "CANCELLED", label: "cancelled", color: "bg-zinc-200 dark:bg-zinc-700" },
];

export function Projects({ ov }: { ov: Overview }) {
  const conv = new Set(ov.tasks.filter((t) => isConversation(t as BoardTask)).map((t) => t.id));
  return (
    <Section title="Projects" count={ov.status.length} href="/tasks" linkLabel="Board">
      {ov.status.length === 0 ? <p className="border-t px-4 py-6 text-center text-sm text-muted-foreground">No projects yet.</p> : (
        <ul>
          {ov.status.map((s) => {
            const total = Object.values(s.tasks).reduce((a, b) => a + b, 0);
            const open = total - (s.tasks.DONE ?? 0) - (s.tasks.CANCELLED ?? 0);
            const waiting = s.waiting.filter((w) => w.reason !== "backlog" && !conv.has(w.task_id));
            const live = s.runs.filter((r) => r.status !== "QUEUED" && r.task_id && !conv.has(r.task_id)).length;
            return (
              <li key={s.project.id}>
                <Link href={`/tasks?project=${encodeURIComponent(s.project.id)}`} className="block border-t px-4 py-4 transition-colors hover:bg-muted/50 focus-visible:bg-muted/60 focus-visible:outline-none sm:px-5">
                  <div className="flex items-baseline gap-2">
                    <span className="font-medium">{s.project.name}</span>
                    <span className="truncate text-xs text-muted-foreground">{open} open · {live} running{s.runnable > 0 ? ` · ${s.runnable} runnable` : ""}</span>
                    <ChevronRight aria-hidden className="ml-auto size-4 shrink-0 self-center text-muted-foreground/60" />
                  </div>
                  <div className="mt-2.5 flex h-1.5 gap-0.5 overflow-hidden rounded-full bg-muted" role="img"
                    aria-label={segments.filter((g) => s.tasks[g.key]).map((g) => `${s.tasks[g.key]} ${g.label}`).join(", ")}>
                    {segments.map((g) => s.tasks[g.key] ? <span key={g.key} className={g.color} style={{ flexGrow: s.tasks[g.key] }} /> : null)}
                  </div>
                  <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                    {segments.filter((g) => s.tasks[g.key]).map((g) => (
                      <span key={g.key} className="inline-flex items-center gap-1.5"><span aria-hidden className={cn("size-1.5 rounded-full", g.color)} />{s.tasks[g.key]} {g.label}</span>
                    ))}
                  </div>
                  {waiting.length > 0 && (
                    <ul className="mt-3 space-y-1.5">
                      {waiting.slice(0, 3).map((w) => {
                        const you = w.clearer.who === "owner";
                        return (
                          <li key={w.task_id} className="flex min-w-0 items-baseline gap-2 text-xs">
                            <span className="shrink-0 font-mono text-muted-foreground">{w.task_id.slice(0, 12)}</span>
                            <span className={cn("min-w-0 truncate", you ? "text-amber-700 dark:text-amber-300" : "text-muted-foreground")}>
                              <span className="font-medium">{you ? "Needs you" : reasonLabel(w.reason)}</span>
                              {" · "}{you ? w.clearer.action : `${clearerName(w.clearer.who)}${w.clearer.action ? `: ${w.clearer.action}` : ""}`}
                            </span>
                          </li>
                        );
                      })}
                      {waiting.length > 3 && <li className="text-xs text-muted-foreground">+{waiting.length - 3} more waiting</li>}
                    </ul>
                  )}
                </Link>
              </li>
            );
          })}
        </ul>
      )}
    </Section>
  );
}

// Team: the org tree by reports_to under the owner, with who is busy, queued or paused.
export function Team({ ov }: { ov: Overview }) {
  const kids = new Map<string, Agent[]>();
  const names = new Set(ov.agents.map((a) => a.name));
  for (const a of ov.agents) {
    const boss = a.reports_to && names.has(a.reports_to) ? a.reports_to : "owner";
    kids.set(boss, [...(kids.get(boss) ?? []), a]);
  }
  const busy = new Map<string, Run>();
  for (const r of Object.values(ov.live_runs ?? {})) {
    const cur = busy.get(r.spec.agent);
    if (r.status !== "ENDED" && (!cur || cur.status === "QUEUED")) busy.set(r.spec.agent, r);
  }
  const paused = new Set(ov.pauses.filter((p) => p.scope === "agent").map((p) => p.target));
  const working = ov.agents.filter((a) => busy.get(a.name) && busy.get(a.name)!.status !== "QUEUED").length;
  const tasks = new Map(ov.tasks.map((t) => [t.id, t as BoardTask]));

  const node = (a: Agent, depth: number): React.ReactNode => {
    const r = busy.get(a.name);
    const t = r?.spec.task_id ? tasks.get(r.spec.task_id) : undefined;
    const state = paused.has(a.name) ? "paused" : !r ? "idle" : r.status === "QUEUED" ? "queued" : "busy";
    return (
      <li key={a.name}>
        <Link href={`/tasks?assignee=${encodeURIComponent(a.name)}`}
          className="relative flex min-h-12 items-center gap-2.5 rounded-lg py-1.5 pr-2 transition-colors hover:bg-muted/60 focus-visible:bg-muted/60 focus-visible:outline-none"
          style={{ paddingLeft: `${0.5 + depth * 1.25}rem` }}>
          {depth > 0 && <span aria-hidden className="absolute top-0 bottom-1/2 w-2.5 rounded-bl-md border-b border-l border-border" style={{ left: `${depth * 1.25 - 0.75}rem` }} />}
          <AgentAvatar name={a.name} />
          <span className="min-w-0 flex-1">
            <span className="block truncate text-sm font-medium leading-tight">{a.name}</span>
            <span className="block truncate text-xs text-muted-foreground">
              {state === "busy" ? (t ? (isConversation(t) ? "chatting with you" : `on ${t.id} · ${t.title}`) : "working") : a.role || a.model}
            </span>
          </span>
          <span className={cn("shrink-0 rounded-full px-2 text-[11px] font-medium leading-5",
            state === "busy" ? toneClass("DONE") : state === "queued" ? toneClass("READY") : state === "paused" ? toneClass("REWORK") : "text-muted-foreground")}>
            {state}
          </span>
        </Link>
        {(kids.get(a.name)?.length ?? 0) > 0 && <ul>{kids.get(a.name)!.map((c) => node(c, depth + 1))}</ul>}
      </li>
    );
  };

  return (
    <Section title="Team" href="/chat" linkLabel="Chat">
      <p className="-mt-1 px-4 pb-2 text-xs text-muted-foreground sm:px-5">{working} of {ov.agents.length} agents working</p>
      <div className="border-t px-2 py-2 sm:px-3">
        <div className="flex min-h-11 items-center gap-2.5 px-2">
          <AgentAvatar name="owner" />
          <span className="text-sm font-medium">You</span>
          <span className="text-xs text-muted-foreground">owner</span>
        </div>
        <ul>{(kids.get("owner") ?? []).map((a) => node(a, 1))}</ul>
      </div>
    </Section>
  );
}

export function Nodes({ ov, now }: { ov: Overview; now: number }) {
  return (
    <Section title="Nodes" count={ov.nodes.length} href="/nodes" linkLabel="Manage">
      {ov.nodes.length === 0 ? <p className="border-t px-4 py-6 text-center text-sm text-muted-foreground">No node has registered.</p> : (
        <ul>
          {ov.nodes.map((n) => {
            const bad = (n.drivers ?? []).filter((d) => !d.ok).map((d) => d.name);
            const checks = ((n.health?.checks as { check: string; status: string; detail?: string }[] | undefined) ?? []).filter((c) => c.status === "fail");
            const slots = n.slots ?? 0;
            return (
              <li key={n.node_id ?? n.name}>
                <Row href="/nodes">
                  <span aria-hidden className={cn("size-2 shrink-0 rounded-full", n.online ? "bg-emerald-500" : "bg-zinc-400")} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">{n.name}</span>
                    <span className="block truncate text-xs text-muted-foreground">
                      {n.online ? `${n.in_use.runs}/${slots} slots in use` : `offline · seen ${since(n.last_seen, now)} ago`}
                      {bad.length > 0 && <span className="text-amber-700 dark:text-amber-300"> · {bad.join(", ")} down</span>}
                      {checks.length > 0 && <span className="text-amber-700 dark:text-amber-300"> · {checks.map((c) => c.detail ?? c.check).join(", ")}</span>}
                    </span>
                  </span>
                  {n.online && slots > 0 && (
                    <span aria-hidden className="flex shrink-0 gap-0.5">
                      {Array.from({ length: slots }, (_, i) => <span key={i} className={cn("h-3 w-1.5 rounded-sm", i < n.in_use.runs ? "bg-foreground/70" : "bg-foreground/12")} />)}
                    </span>
                  )}
                </Row>
              </li>
            );
          })}
        </ul>
      )}
    </Section>
  );
}

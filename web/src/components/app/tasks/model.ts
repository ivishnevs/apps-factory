"use client";

import { useEffect, useState, useSyncExternalStore } from "react";
import { useOverview } from "@/lib/api/client";
import type { Overview, Run, Task } from "@/lib/api/types";
import type { BoardTask } from "@/lib/api/types-tasks";

// useHydrated is false on the server and while hydrating, then true. Screens render cached data only once
// hydrated: a Suspense boundary hydrates late, and data fetched meanwhile would not match the server HTML.
const noop = () => () => {};
export const useHydrated = () => useSyncExternalStore(noop, () => true, () => false);

// useOverviewData is the overview once hydrated (see useHydrated).
export function useOverviewData() {
  const q = useOverview();
  const hydrated = useHydrated();
  return { ...q, data: hydrated ? q.data : undefined, isLoading: !hydrated || q.isLoading, error: hydrated ? q.error : null };
}

// useClock is the company's "now" in Unix seconds: the overview's server time plus the time since it was
// fetched, ticking every second. A recorded (demo) company stays at its recorded moment.
export function useClock(): number {
  const { data, dataUpdatedAt } = useOverview();
  const [tick, setTick] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setTick(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);
  const at = data?.at ? Date.parse(data.at) : NaN;
  if (Number.isNaN(at) || !dataUpdatedAt) return Math.floor(tick / 1000);
  return Math.floor((at + Math.max(0, tick - dataUpdatedAt)) / 1000);
}

// since: a compact age ("now", "4m", "3h", "2d") from Unix seconds against the clock.
export function since(t: number | undefined, now: number): string {
  if (!t) return "";
  const s = Math.max(0, now - t);
  if (s < 45) return "now";
  if (s < 3600) return `${Math.round(s / 60)}m`;
  if (s < 86400) return `${Math.round(s / 3600)}h`;
  return `${Math.round(s / 86400)}d`;
}

// elapsed: a run's running time, "12:04" or "1h 12m".
export function elapsed(from: number | undefined, now: number): string {
  if (!from) return "";
  const s = Math.max(0, now - from);
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
  return h > 0 ? `${h}h ${String(m).padStart(2, "0")}m` : `${m}:${String(sec).padStart(2, "0")}`;
}

export const isTerminal = (s: string) => s === "DONE" || s === "CANCELLED";
export const isConversation = (t: BoardTask) => t.kind === "conversation";
export const runCost = (r?: Run) => (r?.usage ?? []).reduce((n, u) => n + (u.cost_usd ?? 0), 0);

// The live (non-queued) and queued runs of tasks, by task id.
export function runsByTask(ov?: Overview) {
  const live = new Map<string, Run>(), queued = new Map<string, Run>();
  for (const r of Object.values(ov?.live_runs ?? {})) {
    const t = r.spec.task_id;
    if (!t || r.status === "ENDED") continue;
    (r.status === "QUEUED" ? queued : live).set(t, r);
  }
  return { live, queued };
}

// Board columns, in order (as the terminal board groups them: a run waiting for a node is still Ready).
export type ColumnKey = "backlog" | "ready" | "waiting" | "running" | "review" | "done";
export const columns: { key: ColumnKey; name: string; tone: string }[] = [
  { key: "backlog", name: "Backlog", tone: "BACKLOG" },
  { key: "waiting", name: "Waiting", tone: "REWORK" },
  { key: "ready", name: "Ready", tone: "READY" },
  { key: "running", name: "Running", tone: "IN_PROGRESS" },
  { key: "review", name: "In review", tone: "IN_REVIEW" },
  { key: "done", name: "Done", tone: "DONE" },
];

export function columnOf(t: Task, runs: ReturnType<typeof runsByTask>): ColumnKey {
  if (isTerminal(t.status)) return "done";
  if (t.status === "BACKLOG") return "backlog";
  if (t.status === "IN_REVIEW") return "review";
  if (runs.queued.has(t.id)) return "ready";
  if (runs.live.has(t.id) || t.status === "IN_PROGRESS") return "running";
  if (t.status === "BLOCKED" || t.status === "REWORK" || t.reason.startsWith("parked:") || t.reason.startsWith("paused:") ||
    ["dependencies", "artifacts", "unassigned", "pinned_node_unavailable", "openspec", "no_capable_node"].includes(t.reason)) return "waiting";
  return "ready";
}

// Why a task is where it is, in words: a short label and what clears it ("you" when it is the owner).
const reasonLabels: Record<string, string> = {
  live_run: "Running", dependencies: "Blocked by dependencies", artifacts: "Waiting for artifacts", unassigned: "No assignee",
  backoff: "Backing off", capacity: "Queued", idle: "Idle", runnable: "Runnable", backlog: "In backlog", terminal: "Closed",
  openspec: "OpenSpec check fails", pinned_node_unavailable: "Its node is unavailable", no_capable_node: "No node can run it",
  "parked:escalation": "Escalated", "parked:inspect": "Needs inspection", "parked:wait": "Waiting", "parked:ask": "Question asked",
  "parked:block": "Blocked", "parked:children": "Waiting for subtasks",
};
export function reasonLabel(reason: string): string {
  if (reasonLabels[reason]) return reasonLabels[reason];
  if (reason.startsWith("paused:")) return `Paused (${reason.slice(7).replace("_", " ")})`;
  if (reason.startsWith("parked:")) return `Parked: ${reason.slice(7)}`;
  return reason.replace(/_/g, " ");
}
// needsOwner: the task waits on the owner now (a backlog task merely could be released, so it does not count).
export const needsOwner = (t: { clearer?: { who: string }; reason?: string; status?: string }) =>
  t.clearer?.who === "owner" && t.reason !== "backlog" && t.reason !== "terminal" && !isTerminal(t.status ?? "");
export const clearerName = (who?: string) => (who === "owner" ? "you" : who?.startsWith("run:") ? who.slice(4) : who ?? "");
// showWhy: the reason line is worth a card's space (not for finished, backlog or plainly running tasks).
export const showWhy = (t: Task) => !isTerminal(t.status) && !["terminal", "runnable", "backlog", ""].includes(t.reason);

// A stable soft colour per agent for avatars.
const hues = [
  "bg-sky-500/15 text-sky-700 dark:text-sky-300", "bg-violet-500/15 text-violet-700 dark:text-violet-300",
  "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300", "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  "bg-rose-500/15 text-rose-700 dark:text-rose-300", "bg-teal-500/15 text-teal-700 dark:text-teal-300",
  "bg-indigo-500/15 text-indigo-700 dark:text-indigo-300", "bg-orange-500/15 text-orange-700 dark:text-orange-300",
];
export function agentHue(name: string): string {
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) >>> 0;
  return hues[h % hues.length];
}
export function initials(name: string): string {
  const parts = name.split(/[-_ .]+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return name.slice(0, 2).toUpperCase();
}

export const projectName = (ov: Overview | undefined, id: string) => ov?.status.find((s) => s.project.id === id)?.project.name ?? id;

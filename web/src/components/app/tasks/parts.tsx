"use client";

import { CircleUserRound, Clock, Copy, Hand, Loader2, Pin } from "lucide-react";
import Link from "@/components/app/link";
import { StatusBadge, toneClass } from "@/components/app/status";
import type { FactoryNode, QueuedReason, Run, Task } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import { agentHue, columns, type ColumnKey, elapsed, heldBack, initials, needsAction, needsOwner, queuedLine, showWhy, since } from "./model";
import { humanize, type Lookup } from "@/lib/humanize";
import { displayId } from "@/lib/format";
import { usePrefetchTask } from "@/lib/api/client";

export function AgentAvatar({ name, size = "sm", className }: { name?: string; size?: "xs" | "sm" | "md"; className?: string }) {
  const dims = { xs: "size-5 text-[9px]", sm: "size-6 text-[10px]", md: "size-9 text-xs" }[size];
  if (!name) {
    return <CircleUserRound aria-hidden className={cn("shrink-0 text-muted-foreground/60", size === "md" ? "size-9" : size === "sm" ? "size-6" : "size-5", className)} />;
  }
  if (name === "owner") {
    return <span aria-hidden className={cn("flex shrink-0 items-center justify-center rounded-full bg-foreground font-semibold text-background", dims, className)}>You</span>;
  }
  return (
    <span aria-hidden className={cn("flex shrink-0 items-center justify-center rounded-full font-semibold tracking-tight", agentHue(name), dims, className)}>
      {initials(name)}
    </span>
  );
}

// PriorityMark: three bars filled by priority (8+ high, 5–7 medium, below low), with the number.
export function PriorityMark({ value, showValue = true, className }: { value: number; showValue?: boolean; className?: string }) {
  const level = value >= 8 ? 3 : value >= 5 ? 2 : 1;
  return (
    <span className={cn("inline-flex items-center gap-1 text-xs tabular-nums text-muted-foreground", className)} title={`Priority ${value}`}>
      <span aria-hidden className="flex h-3 items-end gap-[2px]">
        {[1, 2, 3].map((i) => (
          <span key={i} className={cn("w-[3px] rounded-[1px]", i === 1 ? "h-1.5" : i === 2 ? "h-2.5" : "h-3",
            i <= level ? (level === 3 ? "bg-orange-500" : "bg-foreground/70") : "bg-foreground/15")} />
        ))}
      </span>
      {showValue && <span className="sr-only sm:not-sr-only">P{value}</span>}
      {!showValue && <span className="sr-only">Priority {value}</span>}
    </span>
  );
}

// WhyLine: why a task is where it is, in product words (lib/humanize); amber when it waits on the owner.
export function WhyLine({ task, look, now, compact, className }: {
  task: Pick<Task, "reason" | "clearer" | "status">; look?: Lookup; now?: number; compact?: boolean; className?: string;
}) {
  const h = humanize(task, look, now);
  const you = needsOwner(task) && h.you;
  const Icon = you ? Hand : h.live ? Loader2 : Clock;
  return (
    <p className={cn("flex min-w-0 items-start gap-1.5 text-xs leading-5", you ? "text-amber-700 dark:text-amber-300" : "text-muted-foreground", className)}>
      <Icon aria-hidden className={cn("mt-[3px] size-3.5 shrink-0", h.live && "animate-spin [animation-duration:2.5s]")} />
      <span className={cn("min-w-0 wrap-anywhere", compact && "line-clamp-2")}>
        <span className="font-medium">{h.headline}</span>
        {h.brief && <span className={you ? "" : "text-muted-foreground/80"}>{" · "}{h.brief}</span>}
      </span>
    </p>
  );
}

// TaskCard: a task on the board. In progress cards show the run time and node; cards that need someone to act
// are marked (yours amber, others red) so they stand out from tasks that wait their turn.
// On the board (onBoard) a card opens the task in the panel over it without scrolling the board, and its details
// are fetched as soon as the pointer or the focus is on it (task-board-ux 2.2).
export function TaskCard({ task, project, now, column, look, run, nodes, queued, className, onBoard }: {
  task: Task; project: string; now: number; column?: ColumnKey; look?: Lookup; run?: Run; nodes?: FactoryNode[]; queued?: QueuedReason; className?: string; onBoard?: boolean;
}) {
  const prefetch = usePrefetchTask();
  // node-readiness 4.1: a queued run held back by a paused node's session (or no ready node) says so on the card;
  // Release now is on the task and on the node's card.
  const held = heldBack(queued) && queued ? queuedLine(queued, (id) => nodes?.find((n) => n.node_id === id)?.name ?? id) : "";
  const col = column && columns.find((c) => c.key === column);
  const badge = !col && ["CANCELLED", "BLOCKED", "REWORK"].includes(task.status);
  const yours = needsOwner(task), action = !yours && needsAction(task);
  // task-duplicate-warnings: an unanswered warning that another open task covers the same work.
  const dup = task.possible_duplicates?.length ?? 0;
  const node = run?.node_id ? nodes?.find((n) => n.node_id === run.node_id)?.name ?? run.node_id.slice(0, 8) : "";
  const live = run && run.status !== "QUEUED" && run.started_at > 0;
  // node-ties: a task tied to a node by its unsaved work runs only there; the card names that node.
  const pinnedTo = !live && !["DONE", "CANCELLED"].includes(task.status) ? nodes?.find((n) => (n.tied?.work ?? n.tied?.pinned)?.includes(task.id)) : undefined;
  return (
    <Link href={`/tasks/${encodeURIComponent(task.id)}`} data-task-card={task.id} scroll={onBoard ? false : undefined}
      onMouseEnter={() => prefetch(task.id)} onFocus={() => prefetch(task.id)}
      className={cn("group relative block rounded-xl border bg-card p-3.5 shadow-xs transition-[border-color,box-shadow,transform] duration-150 hover:border-foreground/20 hover:shadow-sm focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 active:scale-[0.99]",
        yours && "border-amber-500/50 bg-amber-50/40 dark:border-amber-400/30 dark:bg-amber-400/[0.04]",
        action && "border-red-500/40 dark:border-red-400/30", className)}>
      <div className="mb-1 flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
        <span className="shrink-0 font-mono" title={task.id}>{displayId(task.id)}</span>
        <span aria-hidden className="text-muted-foreground/40">·</span>
        <span className="min-w-0 truncate">{project}</span>
        <span className="ml-auto flex shrink-0 items-center gap-2">
          {dup > 0 && (
            <span className="inline-flex items-center gap-1 rounded-full bg-amber-500/15 px-1.5 text-[11px] font-medium leading-5 text-amber-700 dark:text-amber-300"
              title={`Possible duplicate of ${task.possible_duplicates?.map((d) => `${displayId(d.task_id)} ${d.title}`).join(", ")}`}>
              <Copy aria-hidden className="size-3" />duplicate?
            </span>
          )}
          {badge && <StatusBadge value={task.status} className="h-5 px-1.5 text-[11px]" />}
          {col && <span className={cn("rounded-full px-1.5 text-[11px] font-medium leading-5", toneClass(col.key === "done" ? task.status : col.tone))}>{col.key === "done" ? task.status.toLowerCase() : col.name.toLowerCase()}</span>}
          <PriorityMark value={task.priority} showValue={false} />
        </span>
      </div>
      <p className="line-clamp-2 text-[15px] font-medium leading-snug text-foreground wrap-anywhere sm:text-sm">{task.title}</p>
      {action && <p className="mt-1.5 text-[11px] font-semibold tracking-wide text-red-700 uppercase dark:text-red-300">Needs action</p>}
      {held ? (
        <p className="mt-1.5 flex min-w-0 items-start gap-1.5 text-xs leading-5 text-amber-700 dark:text-amber-300">
          <Clock aria-hidden className="mt-[3px] size-3.5 shrink-0" /><span className="min-w-0 wrap-anywhere font-medium">{held}</span>
        </p>
      ) : showWhy(task) && <WhyLine task={task} look={look} now={now} compact className={action ? "mt-0.5" : "mt-1.5"} />}
      {live && (
        <p className="mt-1.5 flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
          <Loader2 aria-hidden className="size-3.5 shrink-0 animate-spin [animation-duration:2.5s]" />
          <span className="tabular-nums" title="Run time">{elapsed(run.started_at, now)}</span>
          {node && <><span aria-hidden className="text-muted-foreground/40">·</span><span className="min-w-0 truncate" title="Node">{node}</span></>}
        </p>
      )}
      <div className="mt-3 flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
        <AgentAvatar name={task.assignee} size="xs" />
        <span className="min-w-0 truncate">{task.assignee || "Unassigned"}</span>
        {pinnedTo && (
          <span className="flex min-w-0 shrink items-center gap-1" title={`Tied to ${pinnedTo.name || pinnedTo.node_id}: its unsaved work is there; it runs only there until the work is saved remotely or you reset the workspace`}>
            <Pin aria-hidden className="size-3 shrink-0" /><span className="truncate">{pinnedTo.name || pinnedTo.node_id.slice(0, 8)}</span>
          </span>
        )}
        <span className="ml-auto shrink-0 tabular-nums" title="Last updated">{since(task.updated_at, now)}</span>
      </div>
    </Link>
  );
}

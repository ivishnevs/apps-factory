"use client";

import {
  AlertTriangle, Ban, CheckCircle2, Circle, GitPullRequestArrow, HardDrive, MessageSquare, Pause, Pencil,
  Play, Plus, RotateCcw, ScrollText, ShieldQuestion, SquareTerminal,
} from "lucide-react";
import Link from "next/link";
import type { FeedItem } from "@/lib/api/types";
import { ago, displayId } from "@/lib/format";
import { cn } from "@/lib/utils";

type Tone = "ok" | "bad" | "warn" | "info" | "violet" | "muted" | "accent";
const toneCls: Record<Tone, string> = {
  ok: "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
  bad: "bg-red-500/10 text-red-600 dark:text-red-400",
  warn: "bg-amber-500/10 text-amber-600 dark:text-amber-400",
  info: "bg-sky-500/10 text-sky-600 dark:text-sky-400",
  violet: "bg-violet-500/10 text-violet-600 dark:text-violet-400",
  accent: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
  muted: "bg-muted text-muted-foreground",
};

// feedLook is the icon and tone of a feed event (internal/tui/feedview.go feedIcon).
export function feedLook(item: FeedItem): { icon: React.ElementType; tone: Tone } {
  const t = item.type;
  if (t.startsWith("approval.")) return { icon: ShieldQuestion, tone: "violet" };
  if (item.attention) {
    if (t.startsWith("node.")) return { icon: HardDrive, tone: "bad" };
    if (t.includes("paused")) return { icon: Pause, tone: "warn" };
    return { icon: AlertTriangle, tone: "bad" };
  }
  if (t.endsWith(".done")) return { icon: CheckCircle2, tone: "ok" };
  if (t === "task.created") return { icon: Plus, tone: "info" };
  if (t === "task.exit.submitted" || t === "task.exit.handoff") return { icon: GitPullRequestArrow, tone: "violet" };
  if (t.startsWith("task.exit.blocked")) return { icon: Ban, tone: "bad" };
  if (t.startsWith("task.exit.") || t.startsWith("message.")) return { icon: MessageSquare, tone: "violet" };
  if (t === "task.started") return { icon: Play, tone: "accent" };
  if (t.includes("refused")) return { icon: SquareTerminal, tone: "bad" };
  if (t === "task.retry") return { icon: RotateCcw, tone: "warn" };
  if (t.includes("paused") || t.includes("parked")) return { icon: Pause, tone: "warn" };
  if (t.startsWith("task.trigger.") || t === "task.commented") return { icon: Pencil, tone: "warn" };
  if (t.startsWith("node.") || t.startsWith("driver")) return { icon: HardDrive, tone: "muted" };
  return { icon: Circle, tone: "muted" };
}

// typeLabel: "task.exit.submitted" → "submitted".
const typeLabel = (t: string) => t.split(".").slice(-1)[0].replace(/_/g, " ");

// verb: what happened, in a word or two ("created", "handed off", "approval requested").
const verbs: Record<string, string> = {
  "task.created": "Created", "task.started": "Started", "task.retry": "Retry", "task.commented": "Comment",
  "task.exit.done": "Done", "task.exit.submitted": "Submitted", "task.exit.handoff": "Handed off", "task.exit.asked": "Asked",
  "task.exit.blocked": "Blocked", "message.created": "Message", "approval.requested": "Approval requested", "owner.paused": "Paused",
  "run.command_refused": "Command refused",
};
function verb(t: string) {
  if (verbs[t]) return verbs[t];
  if (t.startsWith("task.trigger.")) return t.endsWith("directive") ? "Directive" : "Trigger";
  if (t.startsWith("task.alert.")) return "Alert";
  if (t.startsWith("task.escalation.")) return "Escalation";
  if (t.startsWith("task.parked.")) return "Parked";
  const l = typeLabel(t);
  return l.charAt(0).toUpperCase() + l.slice(1);
}

export function FeedRow({ item, fresh, projectName }: { item: FeedItem; fresh?: boolean; projectName?: string }) {
  const { icon: Icon, tone } = feedLook(item);
  const href = item.task_id ? `/tasks/${item.task_id}` : item.run_id ? `/runs/${item.run_id}` : undefined;
  const who = item.agent || item.actor;
  const body = (
    <>
      <span className={cn("mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-full", toneCls[tone])}>
        <Icon className="size-4" />
      </span>
      <span className="min-w-0 flex-1">
        <span className={cn("line-clamp-2 text-sm leading-snug", item.attention && "font-medium")}>{item.summary || typeLabel(item.type)}</span>
        <span className="mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground">
          <span className={cn("font-medium", item.attention ? "text-red-600 dark:text-red-400" : "text-foreground/70")}>{verb(item.type)}</span>
          {who && <><Dot /><span className="font-medium text-foreground/80">{who}</span></>}
          {who && item.actor && item.agent && item.actor !== item.agent && <span>by {item.actor}</span>}
          {(projectName || item.project_id) && <><Dot /><span>{projectName || item.project_id}</span></>}
          {item.task_id && <><Dot /><span className="font-mono" title={item.task_id}>{displayId(item.task_id)}</span></>}
          <Dot /><span className="tabular-nums" title={new Date(item.created_at).toLocaleString()}>{ago(item.created_at)}</span>
        </span>
      </span>
    </>
  );
  return (
    <div className={cn("group flex items-stretch transition-colors duration-[2000ms]", fresh ? "bg-blue-500/[0.07]" : "bg-transparent")}>
      {href ? (
        <Link href={href} className="flex min-h-14 min-w-0 flex-1 items-start gap-3 px-3 py-3 transition-colors hover:bg-accent/50 sm:px-4">{body}</Link>
      ) : (
        <div className="flex min-h-14 min-w-0 flex-1 items-start gap-3 px-3 py-3 sm:px-4">{body}</div>
      )}
      {item.run_id && item.task_id && (
        <Link href={`/runs/${item.run_id}`} aria-label={`Open run ${item.run_id}`} title="Open run"
          className="flex w-11 shrink-0 items-center justify-center text-muted-foreground transition-colors hover:bg-accent/50 hover:text-foreground">
          <ScrollText className="size-4" />
        </Link>
      )}
    </div>
  );
}

const Dot = () => <span aria-hidden className="text-muted-foreground/40">·</span>;

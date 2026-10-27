"use client";

import { AlertTriangle, ArrowRight, Clock3, Loader2, Plus } from "lucide-react";
import Link from "next/link";
import { Button } from "@/components/ui/button";
import type { FactoryNode, Project } from "@/lib/api/types";
import { setupView, type SetupView } from "@/lib/project-setup";
import { cn } from "@/lib/utils";

const look: Record<SetupView["state"], { icon: React.ElementType; box: string; iconCls: string; title: string }> = {
  pending: { icon: Clock3, box: "border-amber-500/30 bg-amber-500/[0.06]", iconCls: "text-amber-600 dark:text-amber-400", title: "text-amber-800 dark:text-amber-200" },
  running: { icon: Loader2, box: "border-sky-500/30 bg-sky-500/[0.06]", iconCls: "text-sky-600 dark:text-sky-400 animate-spin [animation-duration:2s]", title: "text-sky-800 dark:text-sky-200" },
  failed: { icon: AlertTriangle, box: "border-red-500/35 bg-red-500/[0.06]", iconCls: "text-red-600 dark:text-red-400", title: "text-red-700 dark:text-red-300" },
};

// SetupLine: a project card's one- or two-line repository state (inside a link, so no controls).
export function SetupLine({ project, nodes }: { project: Project; nodes?: FactoryNode[] }) {
  const v = setupView(project, nodes);
  if (!v) return null;
  const l = look[v.state];
  return (
    <div className={cn("mt-2.5 flex items-start gap-2 rounded-lg border px-2.5 py-2 text-xs", l.box)}>
      <l.icon aria-hidden className={cn("mt-px size-3.5 shrink-0", l.iconCls)} />
      <span className="min-w-0">
        <span className={cn("font-medium", l.title)}>{v.title}</span>
        {v.state === "failed" && v.detail && <span className="mt-0.5 line-clamp-2 block break-words text-foreground/80">{v.detail}</span>}
      </span>
    </div>
  );
}

// SetupNotice: the board's banner for a project whose repository is not ready: what happens next, a failure's
// reason and what to do, the task that sets it up, and the first task when there is none.
export function SetupNotice({ project, nodes, taskTitle, empty, onAddTask }: {
  project: Project; nodes?: FactoryNode[]; taskTitle?: string; empty: boolean; onAddTask: () => void;
}) {
  const v = setupView(project, nodes);
  if (!v) return null;
  const l = look[v.state];
  return (
    <div role={v.state === "failed" ? "alert" : "status"} className={cn("mb-4 flex flex-col gap-3 rounded-xl border p-4 sm:flex-row sm:items-start", l.box)}>
      <span className="flex min-w-0 flex-1 items-start gap-3">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-background/70 ring-1 ring-border"><l.icon aria-hidden className={cn("size-4", l.iconCls)} /></span>
        <span className="min-w-0 text-sm">
          <span className={cn("block font-medium", l.title)}>{v.title}</span>
          {v.detail && <span className={cn("mt-0.5 block break-words", v.state === "failed" ? "text-foreground" : "text-muted-foreground")}>{v.detail}</span>}
          {v.hint && <span className="mt-1 block text-muted-foreground">{v.hint}</span>}
          {v.taskId && (
            <Link href={`/tasks/${encodeURIComponent(v.taskId)}`} className="mt-1.5 inline-flex min-h-9 items-center gap-1 text-xs font-medium underline-offset-4 hover:underline sm:min-h-0">
              {v.state === "failed" ? "Open the task to retry" : "Open the task that sets it up"}{taskTitle ? `: ${taskTitle}` : ""}<ArrowRight className="size-3.5" />
            </Link>
          )}
        </span>
      </span>
      {empty && (
        <Button className="h-10 shrink-0 self-start sm:self-center" onClick={onAddTask}><Plus />Add the first task</Button>
      )}
    </div>
  );
}

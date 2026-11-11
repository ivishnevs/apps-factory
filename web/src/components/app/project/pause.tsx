"use client";

import { Pause as PauseIcon, Play } from "lucide-react";
import { useOwnerAction } from "@/components/app/nodes/owner-action";
import { Button } from "@/components/ui/button";
import type { Overview, Pause } from "@/lib/api/types";

// project-pause-visible: a project's pause where the project is shown, not only on Nodes & control.

// projectPause is the owner's active pause of the project, if any.
export function projectPause(ov: Pick<Overview, "pauses"> | undefined, project: string): Pause | undefined {
  return ov?.pauses?.find((p) => p.scope === "project" && p.target === project);
}

// PausedBadge marks a paused project in a list.
export function PausedBadge({ pause }: { pause: Pause }) {
  return (
    <span title={pause.reason ? `Paused: ${pause.reason}` : "Paused"}
      className="inline-flex shrink-0 items-center gap-1 rounded-full bg-amber-500/15 px-2 py-0.5 text-xs font-medium text-amber-700 dark:text-amber-300">
      <PauseIcon className="size-3" />Paused
    </span>
  );
}

// ProjectPause is the project page's pause state: a banner with the reason and Resume while paused, a Pause
// button otherwise (owners and admins).
export function ProjectPause({ project, name, pause, manage }: { project: string; name: string; pause?: Pause; manage: boolean }) {
  const { run, pending } = useOwnerAction();
  const hold = () => run({ Kind: "hold", Scope: "project", Target: project, Text: "paused from the project page" },
    { title: `Pause project ${name}?`, description: "No new work starts and its agents stop answering chats; live runs continue.", confirm: "Pause" }, `Paused ${name}`);
  const release = () => run({ Kind: "release", Scope: "project", Target: project },
    { title: `Resume project ${name}?`, description: "Its waiting tasks and chats start running again.", confirm: "Resume" }, `Resumed ${name}`);
  if (!pause) {
    return manage ? <Button variant="outline" className="h-10" onClick={hold} disabled={pending}><PauseIcon />Pause</Button> : null;
  }
  return (
    <section aria-label="Paused" className="mb-6 flex flex-wrap items-center gap-3 rounded-2xl border border-amber-500/40 bg-amber-500/10 p-4">
      <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-amber-500/15 text-amber-700 dark:text-amber-300"><PauseIcon className="size-4" /></span>
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium">Paused: no new work starts and its agents do not answer chats</p>
        <p className="text-sm text-muted-foreground">{pause.reason ? `${pause.reason} · ` : ""}by {pause.actor} · {new Date(pause.created_at * 1000).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}</p>
      </div>
      {manage && <Button className="h-10 shrink-0" onClick={release} disabled={pending}><Play />Resume</Button>}
    </section>
  );
}

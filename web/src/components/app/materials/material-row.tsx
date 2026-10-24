"use client";

import { ArrowUpRight, ChevronRight, EyeOff } from "lucide-react";
import type { Material } from "@/lib/api/types-materials";
import { fileSize } from "@/lib/format";
import { cn } from "@/lib/utils";
import { since } from "@/components/app/tasks/model";
import { ApprovalBadge, KindIcon, PRBadge, authorName, kinds, materialTitle, opensInViewer } from "./kinds";

// MaterialRow is one material in a list: its icon, name and who made it when. A file opens the viewer
// (onOpen); a link or a pull request opens where it lives, in a new tab.
export function MaterialRow({ m, now, onOpen, context, className }: {
  m: Material; now: number; onOpen: (m: Material) => void;
  // context is shown under the name in the Library: the task and the project.
  context?: React.ReactNode; className?: string;
}) {
  const ago = since(m.created_at, now);
  const meta = [kinds[m.kind]?.label, m.kind === "pull_request" ? "" : fileSize(m.size), authorName(m.author), ago && (ago === "now" ? "just now" : `${ago} ago`)].filter(Boolean);
  const inner = (
    <>
      <KindIcon kind={m.kind} />
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-2">
          <span className="min-w-0 truncate text-[15px] font-medium sm:text-sm" title={m.path || m.name}>{materialTitle(m)}</span>
          {m.pr && <PRBadge state={m.pr.state} className="shrink-0" />}
          {m.approval && <ApprovalBadge status={m.approval.status} className="hidden shrink-0 sm:inline-flex" />}
          {m.redacted && <EyeOff aria-label="Secrets removed" className="size-3.5 shrink-0 text-muted-foreground" />}
        </span>
        {context && <span className="mt-0.5 block min-w-0 truncate text-[13px] text-foreground/80 sm:text-xs">{context}</span>}
        <span className="mt-0.5 block truncate text-xs text-muted-foreground tabular-nums">{meta.join(" · ")}</span>
        {m.approval && <ApprovalBadge status={m.approval.status} className="mt-1.5 sm:hidden" />}
      </span>
      {opensInViewer(m) ? <ChevronRight aria-hidden className="size-4 shrink-0 text-muted-foreground/60" />
        : m.url ? <ArrowUpRight aria-hidden className="size-4 shrink-0 text-muted-foreground/60" /> : null}
    </>
  );
  const cls = cn("flex w-full min-w-0 items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/50 focus-visible:bg-muted/60 focus-visible:outline-none", className);
  if (opensInViewer(m)) return <button type="button" onClick={() => onOpen(m)} className={cls}>{inner}</button>;
  if (m.url) return <a href={m.url} target="_blank" rel="noreferrer noopener" className={cls}>{inner}</a>;
  return <div className={cls}>{inner}</div>;
}

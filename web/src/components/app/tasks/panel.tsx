"use client";

import { ChevronDown, ChevronUp, Maximize2, X } from "lucide-react";
import { Dialog } from "radix-ui";
import { useEffect, useRef } from "react";
import { useAppPath, useRouter } from "@/components/app/link";
import { useBoardMounted, useNeighbours } from "@/components/app/tasks/board-store";
import { useOverviewData } from "@/components/app/tasks/model";
import { usePrefetchTask } from "@/lib/api/client";
import { companyHref, useCompany } from "@/lib/company";
import { displayId } from "@/lib/format";
import { cn } from "@/lib/utils";

const taskPath = /^\/tasks\/([^/?#]+)$/;

// TaskPanelHost is the task opened over the board (task-board-ux 2.1, owner 2026-11-11: as a modal). Next.js
// intercepts /tasks/{id} from the board into the @modal slot (`panel`); the board stays mounted under it, so its
// columns, filters and scroll are as they were when the panel closes. The dialog lives here, not in the slot's
// page, so previous/next swaps only its content. It opens only over the board: following a task link from a task's
// full page shows that task's full page (the page reads the address too).
export function TaskPanelHost({ panel }: { panel: React.ReactNode }) {
  const m = useAppPath().match(taskPath);
  const board = useBoardMounted();
  const open = !!m && board;
  return open ? <Panel taskId={decodeURIComponent(m[1])}>{panel}</Panel> : null;
}

function Panel({ taskId, children }: { taskId: string; children: React.ReactNode }) {
  const router = useRouter();
  const company = useCompany();
  const { prev, next, column, index, total } = useNeighbours(taskId);
  const prefetch = usePrefetchTask();
  const title = useOverviewData().data?.tasks.find((t) => t.id === taskId)?.title;
  const content = useRef<HTMLDivElement>(null);
  const scroller = useRef<HTMLDivElement>(null);
  // The last task shown, for giving the focus back to its card on close.
  const shown = useRef(taskId);
  shown.current = taskId;

  // Neighbours are a key press away: fetch them ahead. A new task starts at its top.
  useEffect(() => { if (prev) prefetch(prev); if (next) prefetch(next); }, [prev, next, prefetch]);
  useEffect(() => { scroller.current?.scrollTo({ top: 0 }); }, [taskId]);

  // Previous/next replace the address, so Back (and Esc) still returns to the board in one step.
  const go = (id?: string) => { if (id) router.replace(`/tasks/${encodeURIComponent(id)}`, { scroll: false }); };
  const close = () => router.back();

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.altKey || e.ctrlKey || e.metaKey || e.defaultPrevented) return;
    const t = e.target as HTMLElement;
    if (t.closest("input, textarea, select, [contenteditable=''], [contenteditable='true'], [role='menu'], [role='listbox']")) return;
    if (e.key === "j") { e.preventDefault(); go(next); }
    else if (e.key === "k") { e.preventDefault(); go(prev); }
  };

  const iconButton = "inline-flex size-10 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-40 sm:size-9";
  return (
    <Dialog.Root open onOpenChange={(o) => { if (!o) close(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/50 animate-in fade-in-0" />
        <Dialog.Content ref={content} aria-describedby={undefined} onKeyDown={onKeyDown}
          onOpenAutoFocus={(e) => { e.preventDefault(); content.current?.focus(); }}
          onCloseAutoFocus={(e) => {
            // Back to the card of the task last shown (it may differ from the one opened), the one on screen.
            const cards = document.querySelectorAll<HTMLElement>(`[data-task-card="${CSS.escape(shown.current)}"]`);
            const card = [...cards].find((c) => c.offsetParent !== null);
            if (card) { e.preventDefault(); card.focus(); }
          }}
          className={cn("fixed inset-0 z-50 flex flex-col bg-background outline-none animate-in fade-in-0",
            "sm:inset-auto sm:top-1/2 sm:left-1/2 sm:h-[calc(100dvh-4rem)] sm:w-[calc(100vw-4rem)] sm:max-w-5xl sm:-translate-x-1/2 sm:-translate-y-1/2 sm:rounded-2xl sm:border sm:shadow-xl")}>
          <div className="flex h-14 shrink-0 items-center gap-1 border-b px-2 sm:h-12 sm:px-3">
            <Dialog.Title className="sr-only">{title ?? `Task ${displayId(taskId)}`}</Dialog.Title>
            <button type="button" className={iconButton} onClick={() => go(prev)} disabled={!prev} aria-label="Previous task" title="Previous task (k)" aria-keyshortcuts="k">
              <ChevronUp className="size-4" />
            </button>
            <button type="button" className={iconButton} onClick={() => go(next)} disabled={!next} aria-label="Next task" title="Next task (j)" aria-keyshortcuts="j">
              <ChevronDown className="size-4" />
            </button>
            {column && index >= 0 && (
              <span className="ml-1 min-w-0 truncate text-sm text-muted-foreground" aria-live="polite">
                {column} · <span className="tabular-nums">{index + 1} of {total}</span>
              </span>
            )}
            <a href={companyHref(company, `/tasks/${encodeURIComponent(taskId)}`)} className={cn(iconButton, "ml-auto")} aria-label="Open full page" title="Open full page">
              <Maximize2 className="size-4" />
            </a>
            <Dialog.Close className={iconButton} aria-label="Close" title="Close (Esc)" aria-keyshortcuts="Escape">
              <X className="size-4" />
            </Dialog.Close>
          </div>
          <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 pt-5 pb-[max(1.5rem,env(safe-area-inset-bottom))] sm:px-8 sm:pt-6">
            {children}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

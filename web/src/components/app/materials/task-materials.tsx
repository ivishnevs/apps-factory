"use client";

import { ArrowRight, FolderOpen } from "lucide-react";
import { Suspense } from "react";
import { ErrorState } from "@/components/app/page";
import { useHydrated } from "@/components/app/tasks/model";
import { Skeleton } from "@/components/ui/skeleton";
import { useTaskMaterials } from "@/lib/api/client";
import type { Material, MaterialKind } from "@/lib/api/types-materials";
import { fileSize } from "@/lib/format";
import { cleanText } from "@/lib/humanize";
import { ApprovalBadge, KindIcon, authorName, kinds, materialTitle } from "./kinds";
import { MaterialRow } from "./material-row";
import { useViewer } from "./use-viewer";
import { MaterialViewer } from "./viewer";

// Which material leads a finished task's section: a document first, then a page, a PDF, an image, data.
const lead: MaterialKind[] = ["document", "web", "pdf", "image", "data", "code", "file"];

// TaskMaterials is what the task produced: report files, artifact versions and pull requests. On a finished
// task the main file leads as a card (featured); the rest are rows. Nothing shows when it produced nothing.
export function TaskMaterials(props: { taskId: string; featured: boolean; now: number }) {
  return <Suspense fallback={null}><Materials {...props} /></Suspense>;
}

function Materials({ taskId, featured, now }: { taskId: string; featured: boolean; now: number }) {
  const hydrated = useHydrated();
  const q = useTaskMaterials(taskId);
  const items = (hydrated ? q.data : undefined) ?? [];
  const viewer = useViewer(items);
  if (!hydrated || q.isLoading) return featured ? <Skeleton className="h-28 rounded-2xl" /> : null;
  if (q.error) return <Section count={0}><ErrorState error={q.error} /></Section>;
  if (items.length === 0) return null;

  const top = featured ? lead.map((k) => items.find((m) => m.kind === k && m.content_url)).find(Boolean) : undefined;
  const rest = items.filter((m) => m !== top);
  return (
    <Section count={items.length}>
      <div className="grid gap-3">
        {top && <Featured m={top} onOpen={viewer.open} />}
        {rest.length > 0 && (
          <ul className="divide-y overflow-hidden rounded-xl border bg-card">
            {rest.map((m) => <li key={m.id}><MaterialRow m={m} now={now} onOpen={viewer.open} /></li>)}
          </ul>
        )}
      </div>
      <MaterialViewer material={viewer.current} onClose={viewer.close} now={now} showTask={false} />
    </Section>
  );
}

function Section({ count, children }: { count: number; children: React.ReactNode }) {
  return (
    <section aria-label="Materials">
      <div className="mb-2.5 flex items-center gap-2">
        <FolderOpen aria-hidden className="size-4 text-muted-foreground" />
        <h2 className="text-sm font-semibold tracking-tight">Materials{count ? ` · ${count}` : ""}</h2>
      </div>
      {children}
    </section>
  );
}

// Featured: the main file of a finished task, with the note it came with and one tap to read it.
function Featured({ m, onOpen }: { m: Material; onOpen: (m: Material) => void }) {
  const summary = m.report?.summary ? cleanText(m.report.summary) : "";
  const meta = [kinds[m.kind]?.label, fileSize(m.size), authorName(m.author)].filter(Boolean).join(" · ");
  return (
    <button type="button" onClick={() => onOpen(m)}
      className="group flex w-full min-w-0 flex-col gap-3 rounded-2xl border bg-card p-4 text-left shadow-xs transition-colors hover:border-foreground/20 hover:bg-muted/30 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none sm:flex-row sm:items-center sm:p-5">
      <span className="flex min-w-0 flex-1 items-start gap-3.5">
        <KindIcon kind={m.kind} className="size-12 rounded-2xl [&>svg]:size-[22px]" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-base font-semibold leading-snug" title={m.path || m.name}>{materialTitle(m)}</span>
          <span className="mt-0.5 block truncate text-xs text-muted-foreground tabular-nums">{meta}</span>
          {summary && <span className="mt-2 line-clamp-3 block text-sm leading-relaxed text-foreground/80 wrap-anywhere sm:line-clamp-2">{summary}</span>}
          {m.approval && <ApprovalBadge status={m.approval.status} className="mt-2" />}
        </span>
      </span>
      <span className="inline-flex h-10 shrink-0 items-center justify-center gap-1.5 rounded-lg bg-primary px-4 text-sm font-medium text-primary-foreground transition-colors group-hover:bg-primary/90 sm:h-9">
        {m.kind === "image" ? "View" : m.kind === "document" || m.kind === "pdf" ? "Read" : "Open"}<ArrowRight className="size-4" />
      </span>
    </button>
  );
}

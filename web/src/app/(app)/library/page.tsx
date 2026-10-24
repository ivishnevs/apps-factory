"use client";

import { format, isToday, isYesterday } from "date-fns";
import { FolderOpen, Loader2, Search, X } from "lucide-react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useMemo, useState } from "react";
import { Chips } from "@/components/app/feed/chips";
import { kindGroups } from "@/components/app/materials/kinds";
import { MaterialRow } from "@/components/app/materials/material-row";
import { useViewer } from "@/components/app/materials/use-viewer";
import { MaterialViewer } from "@/components/app/materials/viewer";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { projectName, useClock, useHydrated, useOverviewData } from "@/components/app/tasks/model";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useLibrary } from "@/lib/api/client";
import type { Material } from "@/lib/api/types-materials";

const ALL = "all";

// Library: everything the company's tasks produced, newest first, by project and type, searchable; each
// item opens in the viewer and links to its task.
export default function Page() {
  return <Suspense fallback={<><PageHeader title="Library" /><LoadingRows /></>}><Library /></Suspense>;
}

const dayLabel = (t: number) => {
  const d = new Date(t * 1000);
  return isToday(d) ? "Today" : isYesterday(d) ? "Yesterday" : format(d, d.getFullYear() === new Date().getFullYear() ? "EEEE, MMMM d" : "MMMM d, yyyy");
};

function Library() {
  const { data: ov } = useOverviewData();
  const hydrated = useHydrated();
  const now = useClock();
  const router = useRouter();
  const pathname = usePathname();
  const params = useSearchParams();
  const project = params.get("project") ?? ALL;
  const type = params.get("type") ?? ALL;
  const [q, setQ] = useState(params.get("q") ?? "");
  const [needle, setNeedle] = useState(q.trim());
  useEffect(() => { const t = setTimeout(() => setNeedle(q.trim()), 300); return () => clearTimeout(t); }, [q]);

  const set = (k: string, v: string, def: string) => {
    const p = new URLSearchParams(params.toString());
    if (v === def) p.delete(k); else p.set(k, v);
    const s = p.toString();
    router.replace(s ? `${pathname}?${s}` : pathname, { scroll: false });
  };

  const kind = kindGroups.find((g) => g.key === type)?.kinds.join(",");
  const lib = useLibrary({ project: project === ALL ? undefined : project, kind, q: needle || undefined });
  const items = useMemo(() => (hydrated ? lib.data?.pages.flatMap((p) => p.items) : undefined) ?? [], [hydrated, lib.data]);
  const viewer = useViewer(items);
  const days = useMemo(() => {
    const out: { label: string; items: Material[] }[] = [];
    for (const m of items) {
      const label = dayLabel(m.created_at);
      if (out[out.length - 1]?.label === label) out[out.length - 1].items.push(m); else out.push({ label, items: [m] });
    }
    return out;
  }, [items]);
  const filtered = project !== ALL || type !== ALL || !!needle;
  const loading = !hydrated || lib.isLoading;

  return (
    <>
      <PageHeader title="Library" description="Everything your team produced: documents, designs, data and pull requests." />

      <div className="mb-3 flex flex-col gap-2 sm:flex-row sm:items-center">
        <div className="relative flex-1">
          <Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input type="search" aria-label="Search the library" placeholder="Search by name, task or summary" value={q}
            onChange={(e) => setQ(e.target.value)} onBlur={() => set("q", q.trim(), "")}
            className="h-11 pl-9 text-base sm:h-9 sm:text-sm [&::-webkit-search-cancel-button]:hidden" />
          {q && (
            <button type="button" aria-label="Clear search" onClick={() => { setQ(""); set("q", "", ""); }}
              className="absolute top-1/2 right-1 flex size-9 -translate-y-1/2 items-center justify-center rounded-md text-muted-foreground hover:text-foreground">
              <X className="size-4" />
            </button>
          )}
        </div>
        <Select value={project} onValueChange={(v) => set("project", v, ALL)}>
          <SelectTrigger aria-label="Project" className="w-full data-[size=default]:h-11 sm:w-44 sm:data-[size=default]:h-9"><SelectValue /></SelectTrigger>
          <SelectContent position="popper">
            <SelectItem value={ALL}>All projects</SelectItem>
            {ov?.status.map((s) => <SelectItem key={s.project.id} value={s.project.id}>{s.project.name}</SelectItem>)}
          </SelectContent>
        </Select>
      </div>
      <div className="mb-5">
        <Chips label="Type" value={type} onChange={(v) => set("type", v, ALL)}
          options={[{ value: ALL, label: "All" }, ...kindGroups.map((g) => ({ value: g.key, label: g.label }))]} />
      </div>

      {lib.error ? <ErrorState error={lib.error} />
        : loading ? <LoadingRows rows={5} />
        : items.length === 0 ? (
          <EmptyState icon={FolderOpen} title={filtered ? "Nothing matches" : "Nothing here yet"}
            hint={filtered ? "Try another type or project, or clear the search." : "When your team finishes work, the documents, designs and pull requests it produced show up here."} />
        ) : (
          <div className="space-y-6">
            {days.map((d) => (
              <section key={d.label} aria-label={d.label}>
                <h2 className="mb-2 text-xs font-semibold tracking-wide text-muted-foreground uppercase">{d.label}</h2>
                <ul className="divide-y overflow-hidden rounded-xl border bg-card">
                  {d.items.map((m) => (
                    <li key={m.id}>
                      <MaterialRow m={m} now={now} onOpen={viewer.open}
                        context={<>{m.task_title || "A task"}<span className="text-muted-foreground"> · {projectName(ov, m.project_id)}</span></>} />
                    </li>
                  ))}
                </ul>
              </section>
            ))}
            {lib.hasNextPage && (
              <div className="flex justify-center">
                <Button variant="outline" className="h-11 sm:h-9" disabled={lib.isFetchingNextPage} onClick={() => lib.fetchNextPage()}>
                  {lib.isFetchingNextPage && <Loader2 className="animate-spin" />}Show older
                </Button>
              </div>
            )}
          </div>
        )}
      <MaterialViewer material={viewer.current} onClose={viewer.close} now={now} />
    </>
  );
}

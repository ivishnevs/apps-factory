"use client";

import { format, isToday, isYesterday } from "date-fns";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Activity, ChevronDown, Loader2 } from "lucide-react";
import { useCallback, useMemo, useRef, useState } from "react";
import { Chips, LiveIndicator } from "@/components/app/feed/chips";
import { FeedRow } from "@/components/app/feed/feed-row";
import { EmptyState, ErrorState, PageHeader } from "@/components/app/page";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { api, useOverview, useStream, type FeedFilter } from "@/lib/api/client";
import type { FeedItem, Page } from "@/lib/api/types";

const dayLabel = (d: Date) => (isToday(d) ? "Today" : isYesterday(d) ? "Yesterday" : format(d, "EEEE, MMM d"));

export default function FeedPage() {
  const [filter, setFilter] = useState("all");
  const { data: ov } = useOverview();
  const projects = useMemo(() => (ov?.status ?? []).map((s) => s.project), [ov?.status]);
  const projectName = useMemo(() => Object.fromEntries(projects.map((p) => [p.id, p.name])), [projects]);
  // Task titles by ID, and node names as "node:<id>" (project setup summaries name the node by its ID).
  const titles = useMemo(() => new Map([...(ov?.tasks ?? []).map((t) => [t.id, t.title] as const), ...(ov?.nodes ?? []).map((n) => [`node:${n.node_id}`, n.name] as const)]), [ov?.tasks, ov?.nodes]);
  const title = useCallback((id: string) => titles.get(id) || undefined, [titles]);

  const f: FeedFilter = filter === "attention" ? { attention: true } : filter.startsWith("p:") ? { project: filter.slice(2) } : {};
  const options = [
    { value: "all", label: "All" },
    { value: "attention", label: "Needs attention" },
    ...projects.map((p) => ({ value: `p:${p.id}`, label: p.name || p.id })),
  ];

  return (
    <div className="mx-auto max-w-4xl">
      <PageHeader title="Activity" description="Everything the company does, as it happens." />
      <div className="mb-5"><Chips label="Filter activity" value={filter} onChange={setFilter} options={options} /></div>
      {/* A new filter is a new history and stream: remount. */}
      <FeedList key={filter} filter={f} projectName={projectName} title={title} />
    </div>
  );
}

// PAGE is how many rows the feed shows at first and adds per "Load older".
const PAGE = 100;

function useFeedHistory(filter: FeedFilter) {
  // Pages read backwards from the newest row (/api/feed?before=); each next page is older.
  return useInfiniteQuery({
    queryKey: ["feed-tail", filter],
    initialPageParam: 0,
    queryFn: ({ pageParam }) => {
      const q = new URLSearchParams({ before: String(pageParam), limit: String(PAGE) });
      if (filter.project) q.set("project", filter.project);
      if (filter.attention) q.set("attention", "1");
      return api<Page<FeedItem>>(`/feed?${q}`);
    },
    getNextPageParam: (last) => (last.has_more && last.items.length ? last.items[0].sequence : undefined),
    // The live stream keeps the list current; a refetch would only reload pages already shown.
    staleTime: Infinity, refetchOnWindowFocus: false,
  });
}

function FeedList({ filter, projectName, title }: { filter: FeedFilter; projectName: Record<string, string>; title: (id: string) => string | undefined }) {
  const history = useFeedHistory(filter);
  const [live, setLive] = useState<FeedItem[]>([]);
  const [fresh, setFresh] = useState<Set<number>>(new Set());
  // The stream starts at the newest row of the first page, so everything it delivers happened since.
  const top = history.data?.pages[0]?.next_after;
  const known = useRef(new Set<number>());

  const qs = new URLSearchParams();
  if (top !== undefined) qs.set("after", String(top));
  if (filter.project) qs.set("project", filter.project);
  if (filter.attention) qs.set("attention", "1");
  const { connected } = useStream<FeedItem>(top !== undefined ? `/stream/feed?${qs}` : null, "feed", (item) => {
    // A reconnect replays from the same start: a row already shown is not new again.
    if (known.current.has(item.sequence)) return;
    known.current.add(item.sequence);
    setLive((xs) => [...xs, item]);
    setFresh((s) => new Set(s).add(item.sequence));
    setTimeout(() => setFresh((s) => { const n = new Set(s); n.delete(item.sequence); return n; }), 4000);
  });

  const { days, count } = useMemo(() => {
    const seen = new Set<number>();
    const all = [...live, ...(history.data?.pages.flatMap((p) => p.items) ?? [])]
      .filter((i) => !seen.has(i.sequence) && !!seen.add(i.sequence))
      .sort((a, b) => b.sequence - a.sequence);
    const out: { label: string; items: FeedItem[] }[] = [];
    for (const i of all) {
      const label = dayLabel(new Date(i.created_at));
      if (out[out.length - 1]?.label !== label) out.push({ label, items: [] });
      out[out.length - 1].items.push(i);
    }
    return { days: out, count: all.length };
  }, [live, history.data]);

  if (!history.data) return history.error ? <ErrorState error={history.error} /> : <FeedSkeleton />;

  return (
    <div>
      <div className="mb-3 flex items-center justify-between gap-3">
        <p className="text-xs text-muted-foreground tabular-nums">
          {count === 0 ? "No events" : history.hasNextPage ? `Latest ${count.toLocaleString()} events` : `${count.toLocaleString()} ${count === 1 ? "event" : "events"}`}
        </p>
        <LiveIndicator connected={connected} />
      </div>
      {days.length === 0 ? (
        <EmptyState icon={Activity} title={filter.attention ? "Nothing needs your attention" : "No activity yet"}
          hint={filter.attention ? "Alerts, stalls and refusals show up here." : "Events appear here live as agents work."} />
      ) : (
        <div className="space-y-6">
          {days.map((d) => (
            <section key={d.label} aria-label={d.label}>
              <h2 className="sticky top-0 z-10 -mx-4 mb-2 bg-background/90 px-4 py-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground backdrop-blur md:mx-0 md:px-1">
                {d.label}
              </h2>
              <div className="divide-y overflow-hidden rounded-xl border bg-card">
                {d.items.map((i) => <FeedRow key={i.sequence} item={i} fresh={fresh.has(i.sequence)} projectName={projectName[i.project_id]} title={title} />)}
              </div>
            </section>
          ))}
          <div className="flex justify-center pb-2">
            {history.hasNextPage ? (
              <Button variant="outline" className="h-10 min-w-40 rounded-full" onClick={() => history.fetchNextPage()} disabled={history.isFetchingNextPage}>
                {history.isFetchingNextPage ? <><Loader2 className="animate-spin" /> Loading…</> : <><ChevronDown /> Load older</>}
              </Button>
            ) : (
              <p className="text-xs text-muted-foreground">That&apos;s everything — the start of the activity.</p>
            )}
          </div>
          {history.isFetchNextPageError && <ErrorState error={history.error} />}
        </div>
      )}
    </div>
  );
}

function FeedSkeleton() {
  return (
    <div className="divide-y rounded-xl border">
      {Array.from({ length: 7 }, (_, i) => (
        <div key={i} className="flex gap-3 p-4">
          <Skeleton className="size-8 rounded-full" />
          <div className="flex-1 space-y-2"><Skeleton className="h-4 w-4/5" /><Skeleton className="h-3 w-2/5" /></div>
        </div>
      ))}
    </div>
  );
}

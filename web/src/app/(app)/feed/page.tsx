"use client";

import { format, isToday, isYesterday } from "date-fns";
import { Activity } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Chips, LiveIndicator } from "@/components/app/feed/chips";
import { FeedRow } from "@/components/app/feed/feed-row";
import { EmptyState, ErrorState, PageHeader } from "@/components/app/page";
import { Skeleton } from "@/components/ui/skeleton";
import { useFeed, useOverview, useStream, type FeedFilter } from "@/lib/api/client";
import type { FeedItem } from "@/lib/api/types";

const KEEP = 500;

const dayLabel = (d: Date) => (isToday(d) ? "Today" : isYesterday(d) ? "Yesterday" : format(d, "EEEE, MMM d"));

export default function FeedPage() {
  const [filter, setFilter] = useState("all");
  const { data: ov } = useOverview();
  const projects = useMemo(() => (ov?.status ?? []).map((s) => s.project), [ov?.status]);
  const projectName = useMemo(() => Object.fromEntries(projects.map((p) => [p.id, p.name])), [projects]);

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
      <FeedList key={filter} filter={f} projectName={projectName} />
    </div>
  );
}

function FeedList({ filter, projectName }: { filter: FeedFilter; projectName: Record<string, string> }) {
  const history = useFeed(filter, 100);
  const [live, setLive] = useState<FeedItem[]>([]);
  const [fresh, setFresh] = useState<Set<number>>(new Set());
  const opened = useRef(Date.now());
  const [after, setAfter] = useState<number | null>(null);
  useEffect(() => {
    if (after === null && history.data) setAfter(history.data.next_after ?? history.data.items.reduce((m, i) => Math.max(m, i.sequence), 0));
  }, [history.data, after]);

  const qs = new URLSearchParams();
  if (after !== null) qs.set("after", String(after));
  if (filter.project) qs.set("project", filter.project);
  if (filter.attention) qs.set("attention", "1");
  const { connected } = useStream<FeedItem>(after !== null ? `/stream/feed?${qs}` : null, "feed", (item) => {
    setLive((xs) => (xs.some((x) => x.sequence === item.sequence) ? xs : [...xs, item].slice(-KEEP)));
    // The stream first catches up on older history; only what happened since the page opened is new.
    if (Date.parse(item.created_at) > opened.current - 30_000) {
      setFresh((s) => new Set(s).add(item.sequence));
      setTimeout(() => setFresh((s) => { const n = new Set(s); n.delete(item.sequence); return n; }), 4000);
    }
  });

  const days = useMemo(() => {
    const seen = new Set<number>();
    const all = [...live, ...(history.data?.items ?? [])]
      .filter((i) => !seen.has(i.sequence) && !!seen.add(i.sequence))
      .sort((a, b) => b.sequence - a.sequence)
      .slice(0, KEEP);
    const out: { label: string; items: FeedItem[] }[] = [];
    for (const i of all) {
      const label = dayLabel(new Date(i.created_at));
      if (out[out.length - 1]?.label !== label) out.push({ label, items: [] });
      out[out.length - 1].items.push(i);
    }
    return out;
  }, [live, history.data]);

  if (history.error) return <ErrorState error={history.error} />;
  if (!history.data) return <FeedSkeleton />;

  return (
    <div>
      <div className="mb-3 flex items-center justify-between gap-3">
        <p className="text-xs text-muted-foreground tabular-nums">{days.reduce((n, d) => n + d.items.length, 0)} events</p>
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
                {d.items.map((i) => <FeedRow key={i.sequence} item={i} fresh={fresh.has(i.sequence)} projectName={projectName[i.project_id]} />)}
              </div>
            </section>
          ))}
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

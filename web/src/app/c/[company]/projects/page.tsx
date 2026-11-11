"use client";

import { Archive, FolderGit2, Hand, Loader2 } from "lucide-react";
import Link from "@/components/app/link";
import { useMemo, useState } from "react";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { NewProject } from "@/components/app/project/new-project";
import { PausedBadge, projectPause } from "@/components/app/project/pause";
import { summarize } from "@/components/app/project/summary";
import { columns, since, useClock, useOverviewData } from "@/components/app/tasks/model";
import { toneClass } from "@/components/app/status";
import { usd } from "@/lib/format";
import { setupView } from "@/lib/project-setup";
import { cn } from "@/lib/utils";

// Projects (projects-page 1.1): every project with its team, repository, setup, tasks by column, what needs you,
// the last activity and its cost; a project opens its page. Archived projects are folded at the end.
export default function Page() {
  const { data: ov, error, isLoading } = useOverviewData();
  const now = useClock();
  const [showArchived, setShowArchived] = useState(false);
  const all = useMemo(() => (ov ? summarize(ov) : []), [ov]);
  const active = all.filter((s) => !s.project.archived).sort((a, b) => b.needsYou - a.needsYou || b.lastActivity - a.lastActivity);
  const archived = all.filter((s) => s.project.archived);
  return (
    <>
      <PageHeader title="Projects" description={ov ? `${active.length} project${active.length === 1 ? "" : "s"}` : "Loading…"} actions={<NewProject overview={ov} variant="default" />} />
      {error && !ov ? <ErrorState error={error} /> : isLoading || !ov ? <LoadingRows rows={3} /> : active.length === 0 && archived.length === 0 ? (
        <EmptyState icon={FolderGit2} title="No projects yet" hint="A project is a repository a team works in. Create the first one." />
      ) : (
        <div className="space-y-6">
          <ul className="grid gap-3 md:grid-cols-2">
            {active.map((s) => {
              const setup = setupView(s.project, ov.nodes);
              const paused = projectPause(ov, s.project.id);
              return (
                <li key={s.project.id}>
                  <Link href={`/projects/${encodeURIComponent(s.project.id)}`}
                    className="block rounded-2xl border bg-card p-4 shadow-xs transition-[border-color,box-shadow] hover:border-foreground/20 hover:shadow-sm focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50">
                    <div className="flex items-start gap-3">
                      <div className="min-w-0 flex-1">
                        <p className="truncate text-[15px] font-medium">{s.project.name}</p>
                        <p className="truncate text-xs text-muted-foreground">{s.project.team ? `Team ${s.project.team}` : "No team"} · {s.project.repo_url || "no repository yet"}</p>
                      </div>
                      {paused && <PausedBadge pause={paused} />}
                      {s.needsYou > 0 && <span className="inline-flex shrink-0 items-center gap-1 rounded-full bg-amber-500/15 px-2 py-0.5 text-xs font-medium text-amber-700 dark:text-amber-300"><Hand className="size-3" />{s.needsYou} need{s.needsYou === 1 ? "s" : ""} you</span>}
                    </div>
                    {setup && <p className={cn("mt-2 text-xs", setup.state === "failed" ? "text-red-700 dark:text-red-300" : "text-muted-foreground")}>{setup.title}</p>}
                    <div className="mt-3 flex flex-wrap gap-1.5">
                      {columns.filter((c) => c.key !== "done" && s.counts[c.key] > 0).map((c) => (
                        <span key={c.key} className={cn("rounded-full px-2 text-xs font-medium leading-5", toneClass(c.tone))}>{c.name} {s.counts[c.key]}</span>
                      ))}
                      {s.open === 0 && <span className="text-xs text-muted-foreground">No open tasks</span>}
                    </div>
                    <p className="mt-3 flex items-center gap-1.5 text-xs text-muted-foreground tabular-nums">
                      {s.live > 0 && <><Loader2 className="size-3 animate-spin [animation-duration:2.5s]" />{s.live} running · </>}
                      {s.counts.done} done · active {since(s.lastActivity, now)} ago · {usd(s.cost)}
                    </p>
                  </Link>
                </li>
              );
            })}
          </ul>
          {archived.length > 0 && (
            <section>
              <button type="button" onClick={() => setShowArchived((v) => !v)} className="inline-flex h-10 items-center gap-2 text-sm text-muted-foreground hover:text-foreground">
                <Archive className="size-4" />{showArchived ? "Hide" : "Show"} archived ({archived.length})
              </button>
              {showArchived && (
                <ul className="mt-2 divide-y rounded-xl border">
                  {archived.map((s) => (
                    <li key={s.project.id}><Link href={`/projects/${encodeURIComponent(s.project.id)}`} className="flex items-center justify-between px-4 py-3 text-sm hover:bg-accent/50">
                      <span className="truncate">{s.project.name}</span><span className="text-xs text-muted-foreground">{s.counts.done} done · {usd(s.cost)}</span>
                    </Link></li>
                  ))}
                </ul>
              )}
            </section>
          )}
        </div>
      )}
    </>
  );
}

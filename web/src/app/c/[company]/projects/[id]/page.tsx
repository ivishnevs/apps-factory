"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, ArrowLeft, CircleDollarSign, FolderOpen, KanbanSquare, MessageSquare, Undo2 } from "lucide-react";
import Link from "@/components/app/link";
import { useParams } from "next/navigation";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { summarize } from "@/components/app/project/summary";
import { ProjectApprovals } from "@/components/app/project/approvals";
import { ProjectPause, projectPause } from "@/components/app/project/pause";
import { toneClass } from "@/components/app/status";
import { TaskCard } from "@/components/app/tasks/parts";
import { columns, isConversation, needsOwner, useClock, useOverviewData } from "@/components/app/tasks/model";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { core, useCompanyInfo, useTeams } from "@/lib/api/client";
import { canManage } from "@/lib/api/types-account";
import type { Project } from "@/lib/api/types";
import type { BoardTask } from "@/lib/api/types-tasks";
import { ownerConversation } from "@/lib/api/types-ops";
import { usd } from "@/lib/format";
import { lookupFrom } from "@/lib/humanize";
import { setupView } from "@/lib/project-setup";
import { cn } from "@/lib/utils";

const fail = (e: unknown) => toast.error(e instanceof Error ? e.message : String(e));

// A project's page (projects-page 1.2): what is going on and what needs you, its goal and direction and its rules
// (what the lead decides against, lead-brainstorm-mode), its settings (moved here from Settings) and archiving,
// with links to its board, the chat with its lead, its library and costs.
export default function Page() {
  const { id } = useParams<{ id: string }>();
  const projectId = decodeURIComponent(id);
  const { data: ov, error } = useOverviewData();
  const now = useClock();
  const teams = useTeams().data?.teams ?? [];
  const manage = canManage(useCompanyInfo().data?.role);
  const summary = useMemo(() => (ov ? summarize(ov).find((s) => s.project.id === projectId) : undefined), [ov, projectId]);
  const look = useMemo(() => lookupFrom(ov?.tasks), [ov?.tasks]);
  const back = <Link href="/projects" className="-ml-2 mb-3 inline-flex h-10 items-center gap-1.5 rounded-md px-2 text-sm text-muted-foreground hover:text-foreground"><ArrowLeft className="size-4" />Projects</Link>;
  if (error && !ov) return <>{back}<ErrorState error={error} /></>;
  if (!ov) return <>{back}<LoadingRows rows={4} /></>;
  if (!summary) return <>{back}<EmptyState title="Project not found" hint="It may belong to another company, or the link is mistyped." /></>;
  const p = summary.project;
  const lead = p.team ? teams.find((t) => t.name === p.team)?.lead : undefined;
  const setup = setupView(p, ov.nodes);
  const paused = projectPause(ov, p.id);
  const tasks = (ov.tasks as BoardTask[]).filter((t) => t.project_id === p.id && !isConversation(t));
  const yours = tasks.filter(needsOwner);
  const done = tasks.filter((t) => t.status === "DONE").sort((a, b) => b.updated_at - a.updated_at).slice(0, 5);
  const links = [
    { href: `/tasks?project=${encodeURIComponent(p.id)}`, label: "Board", icon: KanbanSquare },
    ...(lead ? [{ href: `/chat?c=${encodeURIComponent(ownerConversation(lead, p.id))}`, label: `Chat with ${lead}`, icon: MessageSquare }] : []),
    { href: `/library?project=${encodeURIComponent(p.id)}`, label: "Library", icon: FolderOpen },
    ...(manage ? [{ href: `/costs?project=${encodeURIComponent(p.id)}`, label: "Costs", icon: CircleDollarSign }] : []),
  ];
  return (
    <div className="pb-20 md:pb-0">
      {back}
      <PageHeader title={p.name} description={<>{p.team ? `Team ${p.team}` : "No team"}{p.archived && " · archived"} · {p.repo_url || "no repository yet"}</>} />
      <nav aria-label="Project" className="mb-6 flex flex-wrap gap-2">
        {links.map((l) => (
          <Button key={l.href} variant="outline" className="h-10" asChild><Link href={l.href}><l.icon />{l.label}</Link></Button>
        ))}
        {!paused && !p.archived && <ProjectPause project={p.id} name={p.name} manage={manage} />}
      </nav>
      {paused && <ProjectPause project={p.id} name={p.name} pause={paused} manage={manage} />}
      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_22rem] lg:items-start">
        <div className="grid min-w-0 gap-6">
          <section aria-label="Overview" className="rounded-2xl border bg-card p-4 sm:p-5">
            <h2 className="font-medium">Overview</h2>
            {setup && <p className={cn("mt-2 text-sm", setup.state === "failed" ? "text-red-700 dark:text-red-300" : "text-muted-foreground")}>{setup.title}</p>}
            <div className="mt-3 flex flex-wrap gap-1.5">
              {columns.map((c) => <span key={c.key} className={cn("rounded-full px-2 text-xs font-medium leading-5", toneClass(c.tone))}>{c.name} {summary.counts[c.key]}</span>)}
            </div>
            <p className="mt-3 text-sm text-muted-foreground tabular-nums">{summary.live} running now · cost so far {usd(summary.cost)}</p>
          </section>
          {yours.length > 0 && (
            <section aria-label="Needs you">
              <h2 className="mb-2.5 text-sm font-semibold">Needs you</h2>
              <div className="grid gap-2 sm:grid-cols-2">{yours.map((t) => <TaskCard key={t.id} task={t} project={p.name} now={now} look={look} />)}</div>
            </section>
          )}
          {done.length > 0 && (
            <section aria-label="Recently done">
              <h2 className="mb-2.5 text-sm font-semibold">Recently done</h2>
              <div className="grid gap-2 sm:grid-cols-2">{done.map((t) => <TaskCard key={t.id} task={t} project={p.name} now={now} look={look} />)}</div>
            </section>
          )}
          <Direction project={p} manage={manage} />
        </div>
        <aside className="grid min-w-0 gap-6">
          <ProjectApprovals project={p} team={teams.find((t) => t.name === p.team)?.approvals} lead={lead} manage={manage}
            agents={(teams.find((t) => t.name === p.team)?.agents ?? []).map((a) => a.name)} />
          <ProjectSettings project={p} manage={manage} teams={teams.map((t) => t.name)} />
        </aside>
      </div>
    </div>
  );
}

// Direction: the project's goal and direction and its rules, carried by every brief.
function Direction({ project, manage }: { project: Project; manage: boolean }) {
  const qc = useQueryClient();
  const [direction, setDirection] = useState(project.direction ?? "");
  const [rules, setRules] = useState(project.rules ?? "");
  const [busy, setBusy] = useState(false);
  useEffect(() => { setDirection(project.direction ?? ""); setRules(project.rules ?? ""); }, [project.direction, project.rules]);
  const dirty = direction !== (project.direction ?? "") || rules !== (project.rules ?? "");
  const save = async () => {
    setBusy(true);
    try {
      await core(`/projects/${encodeURIComponent(project.id)}`, { method: "PATCH", body: JSON.stringify({ version: project.version, name: project.name, description: project.description, direction, rules }) });
      await qc.invalidateQueries({ queryKey: ["overview"] });
      toast.success("Saved");
    } catch (e) { fail(e); } finally { setBusy(false); }
  };
  return (
    <section aria-label="Goal and direction" className="rounded-2xl border bg-card p-4 sm:p-5">
      <h2 className="font-medium">Goal and direction</h2>
      <p className="mt-1 text-sm text-muted-foreground">What this project is for and where it is going. The lead decides on its own what fits it and asks you only about what does not.</p>
      <Textarea aria-label="Goal and direction" value={direction} onChange={(e) => setDirection(e.target.value)} disabled={!manage} rows={5} className="mt-3 text-base md:text-sm"
        placeholder={"An advanced MVP of a 3v3 arena shooter for iOS that looks production ready.\nMechanics first; 3D stays simple low-poly."} />
      <h3 className="mt-5 text-sm font-medium">Rules</h3>
      <p className="mt-1 text-sm text-muted-foreground">Conventions, facts and fixed decisions every agent of the project follows.</p>
      <Textarea aria-label="Rules" value={rules} onChange={(e) => setRules(e.target.value)} disabled={!manage} rows={5} className="mt-3 text-base md:text-sm" placeholder="Tests stay green on every pull request." />
      {manage && <div className="mt-3 flex justify-end"><Button className="h-10" disabled={!dirty || busy} onClick={save}>Save</Button></div>}
    </section>
  );
}

function ProjectSettings({ project, manage, teams }: { project: Project; manage: boolean; teams: string[] }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const form = (p: Project) => ({ name: p.name, description: p.description ?? "", repo_url: p.repo_url ?? "", default_branch: p.default_branch ?? "", team: p.team ?? "",
    retention: p.artifact_retention_days ? String(p.artifact_retention_days) : "" });
  const [f, setF] = useState(() => form(project));
  const [busy, setBusy] = useState(false);
  useEffect(() => setF(form(project)), [project]);
  // artifact-retention: Core's ceiling (FACTORY_ARTIFACT_RETENTION_MAX_DAYS); 0 is none.
  const ceiling = useQuery({ queryKey: ["artifact-retention"], queryFn: () => core<{ max_days: number }>("/settings/artifact_retention"), staleTime: Infinity }).data?.max_days ?? 0;
  const retention = Number(f.retention || 0);
  const retentionValid = Number.isInteger(retention) && retention >= 0 && (!ceiling || retention <= ceiling);
  const patch = async (body: Record<string, unknown>, done: string) => {
    setBusy(true);
    try {
      await core(`/projects/${encodeURIComponent(project.id)}`, { method: "PATCH", body: JSON.stringify({ version: project.version, name: project.name, description: project.description, ...body }) });
      await Promise.all([qc.invalidateQueries({ queryKey: ["overview"] }), qc.invalidateQueries({ queryKey: ["projects"] })]);
      toast.success(done);
    } catch (e) { fail(e); } finally { setBusy(false); }
  };
  const save = (e: React.FormEvent) => {
    e.preventDefault();
    const body: Record<string, unknown> = { name: f.name.trim(), description: f.description.trim(), repo_url: f.repo_url.trim(), default_branch: f.default_branch.trim(), artifact_retention_days: retention };
    if (f.team && f.team !== (project.team ?? "")) body.team = f.team;
    void patch(body, "Project saved");
  };
  const archive = async () => {
    const to = !project.archived;
    if (!(await confirm({ title: to ? "Archive this project?" : "Restore this project?", description: to ? "It leaves the lists; its tasks, history and files stay. Tasks in work must be finished, cancelled or moved to the backlog first." : "It is back in the lists.", confirm: to ? "Archive" : "Restore" }))) return;
    void patch({ archived: to }, to ? "Archived" : "Restored");
  };
  const field = (k: keyof typeof f, label: string, hint?: string) => (
    <label className="block space-y-1.5 text-sm"><span className="font-medium">{label}</span>
      <Input value={f[k]} disabled={!manage} onChange={(e) => setF({ ...f, [k]: e.target.value })} className="h-10" />
      {hint && <span className="block text-xs text-muted-foreground">{hint}</span>}
    </label>
  );
  return (
    <section aria-label="Settings" className="rounded-2xl border bg-card p-4 sm:p-5">
      <h2 className="font-medium">Settings</h2>
      <form onSubmit={save} className="mt-3 space-y-4">
        {field("name", "Name")}
        {field("description", "Description")}
        {field("repo_url", "Repository", "The git URL agents clone and push to.")}
        {field("default_branch", "Default branch", "Pull requests go into it.")}
        <label className="block space-y-1.5 text-sm"><span className="font-medium">Team</span>
          <select className="h-10 w-full rounded-md border bg-background px-2" value={f.team} disabled={!manage} onChange={(e) => setF({ ...f, team: e.target.value })}>
            {!f.team && <option value="">Choose a team</option>}
            {teams.map((t) => <option key={t} value={t}>{t}</option>)}
          </select>
          <span className="block text-xs text-muted-foreground">Moving a project to another team works only while it has no tasks in progress.</span>
        </label>
        <label className="block space-y-1.5 text-sm"><span className="font-medium">Keep old artifact versions (days)</span>
          <Input type="number" inputMode="numeric" min={0} max={ceiling || undefined} step={1} value={f.retention} disabled={!manage} aria-invalid={!retentionValid}
            placeholder={ceiling ? String(ceiling) : "Forever"} onChange={(e) => setF({ ...f, retention: e.target.value })} className="h-10" />
          <span className={cn("block text-xs", retentionValid ? "text-muted-foreground" : "text-red-700 dark:text-red-300")}>
            Older versions that are neither the latest nor the latest approved one are archived: their record stays, their content is deleted.
            {ceiling ? ` This Factory keeps them at most ${ceiling} days; empty uses ${ceiling}.` : " Empty keeps them forever."}
          </span>
        </label>
        {manage && <Button type="submit" className="h-10 w-full" disabled={busy || !f.name.trim() || !retentionValid}>Save settings</Button>}
      </form>
      {manage && (
        <Button variant="outline" className="mt-4 h-10 w-full" disabled={busy} onClick={archive}>
          {project.archived ? <><Undo2 />Restore project</> : <><Archive />Archive project</>}
        </Button>
      )}
    </section>
  );
}

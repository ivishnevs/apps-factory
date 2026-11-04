"use client";

import { Check, Circle, FolderGit2, KeyRound, Plus, Server, Users } from "lucide-react";
import { useState } from "react";
import Link from "@/components/app/link";
import { ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { NewTeamSheet } from "@/components/app/team/builder";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useSetup, useTeams } from "@/lib/api/client";
import { cn } from "@/lib/utils";

// The setup wizard (team-builder 5.1, saas-web 3.1): a company's way from a team to its first running work — the
// team (from a preset), its agents, the secrets they need, a node, the first project. Each step says whether it is
// done and leads to the form that does it; nothing here blocks work, runs simply wait with their reason.
function Step({ n, title, done, children, icon: Icon }: { n: number; title: string; done: boolean; children: React.ReactNode; icon: React.ElementType }) {
  return (
    <li className="flex gap-4 rounded-2xl border bg-card p-4 md:p-5">
      <span className={cn("flex size-9 shrink-0 items-center justify-center rounded-full text-sm font-medium", done ? "bg-emerald-600 text-white" : "bg-muted")}>
        {done ? <Check className="size-4" /> : n}
      </span>
      <div className="min-w-0 flex-1 space-y-2">
        <p className="flex items-center gap-2 font-medium"><Icon className="size-4 text-muted-foreground" />{title}</p>
        <div className="text-sm text-muted-foreground">{children}</div>
      </div>
    </li>
  );
}

export default function Page() {
  const setup = useSetup();
  const teams = useTeams();
  const [newTeam, setNewTeam] = useState(false);
  if (setup.error || teams.error) return <><PageHeader title="Set up" /><ErrorState error={setup.error ?? teams.error} /></>;
  if (!setup.data || !teams.data) return <><PageHeader title="Set up" /><LoadingRows /></>;
  const s = setup.data;
  const list = teams.data.teams;
  const agents = list.reduce((n, t) => n + t.agents.length, 0);
  return (
    <>
      <PageHeader title="Set up your company" description={s.ready ? "Everything is in place: your agents can work." : "A few steps until your agents can start working."} />
      <ol className="space-y-3">
        <Step n={1} title="A team" done={list.length > 0} icon={Users}>
          {list.length > 0 ? (
            <p>Your teams: {list.map((t) => <Link key={t.name} href={`/teams/${encodeURIComponent(t.name)}`} className="mr-2 font-medium text-foreground underline">{t.name}</Link>)}</p>
          ) : <p>Start from a preset: a solo builder, a product team, a full team, a game studio or a SaaS team.</p>}
          <Button variant={list.length ? "outline" : "default"} className="h-10" onClick={() => setNewTeam(true)}><Plus className="size-4" />{list.length ? "Another team" : "Choose a team"}</Button>
        </Step>
        <Step n={2} title="Its agents" done={agents > 0} icon={Users}>
          <p>{agents} {agents === 1 ? "agent" : "agents"}. Review their prompts and models, or add one from the agent presets on the team&apos;s page.</p>
          <div className="flex flex-wrap gap-1.5">{list.flatMap((t) => t.agents.map((a) => <Badge key={a.name} variant="secondary" className="font-normal">{a.name} · {a.provider}</Badge>))}</div>
        </Step>
        <Step n={3} title="Secrets they need" done={s.missing_secrets.length === 0} icon={KeyRound}>
          {s.missing_secrets.length === 0 ? <p>No missing secrets.</p> : (
            <>
              <ul className="space-y-1">{s.missing_secrets.map((x) => <li key={x.name}><span className="font-mono text-foreground">{x.name}</span> ({x.kind}) for {x.agents.join(", ")}</li>)}</ul>
              <Button asChild className="h-10"><Link href="/secrets">Set secrets</Link></Button>
            </>
          )}
        </Step>
        <Step n={4} title="A node to run on" done={!s.no_node} icon={Server}>
          {s.no_node ? (
            <>
              <p>Agents run on your machine (or one you rent). Enroll it with a one-time code and approve its key.</p>
              <Button asChild className="h-10"><Link href="/nodes">Connect a node</Link></Button>
            </>
          ) : <p>A node is connected.</p>}
        </Step>
        <Step n={5} title="The first project" done={s.teams_without_projects.length === 0 && list.length > 0} icon={FolderGit2}>
          {s.teams_without_projects.length > 0 ? (
            <>
              <p>Teams without a project: {s.teams_without_projects.join(", ")}. A project is a repository the team works in.</p>
              <Button asChild className="h-10"><Link href="/tasks">Create a project</Link></Button>
            </>
          ) : <p>Every team has a project. Give its lead a first task on the board or in chat.</p>}
        </Step>
      </ol>
      {s.ready && <p className="mt-5 flex items-center gap-2 text-sm text-emerald-700 dark:text-emerald-400"><Circle className="size-3 fill-current" />Ready. <Link href="/tasks" className="underline">Open the board</Link></p>}
      <NewTeamSheet open={newTeam} onClose={() => setNewTeam(false)} />
    </>
  );
}

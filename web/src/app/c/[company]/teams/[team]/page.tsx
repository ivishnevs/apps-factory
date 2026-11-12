"use client";

import { ArrowLeft, FolderGit2, Layers, MessageSquare, Plus } from "lucide-react";
import { useParams } from "next/navigation";
import { useState } from "react";
import Link from "@/components/app/link";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { Editor, MemberCard } from "@/components/app/team/catalog";
import { SkillPicker } from "@/components/app/team/skill-picker";
import { HowTeamRuns, WhoDoesWhat } from "@/components/app/team/team-runs";
import { RenameTeam } from "@/components/app/team/rename";
import { ApprovalModesView } from "@/components/app/project/approvals";
import { AddAgentSheet, AgentFormSheet, SetupBanner } from "@/components/app/team/builder";
import { Button } from "@/components/ui/button";
import { useAgentRuns, useCatalog, useCompanyInfo, useSetup, useTeams } from "@/lib/api/client";
import { canManage } from "@/lib/api/types-account";

// A team (company-teams 7.1, team-page-clarity): how it runs (its lead, the agent that onboards an added repository
// and the team settings, each explained and changeable), who does what, who approves, its agents and its projects.
export default function Page() {
  const name = decodeURIComponent(String(useParams<{ team: string }>().team ?? ""));
  const teams = useTeams();
  const catalog = useCatalog();
  const [open, setOpen] = useState<string | null>(null);
  const [picking, setPicking] = useState<string | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const setup = useSetup();
  const agentRuns = useAgentRuns().data;
  const role = useCompanyInfo().data?.role;
  const manage = canManage(role);
  const team = teams.data?.teams.find((t) => t.name === name);
  const back = <Link href="/teams" className="-ml-2 mb-3 inline-flex h-10 items-center gap-1.5 rounded-md px-2 text-sm text-muted-foreground transition-colors hover:text-foreground"><ArrowLeft className="size-4" />Teams</Link>;
  if (teams.error) return <>{back}<ErrorState error={teams.error} /></>;
  if (teams.isLoading) return <>{back}<LoadingRows /></>;
  if (!team) return <>{back}<EmptyState title={`No team ${name}`} hint="It may have been removed from the catalog." icon={Layers} /></>;
  return (
    <>
      {back}
      <PageHeader title={team.title || team.name}
        description={`${team.description ? `${team.description.replace(/\.?$/, ".")} ` : ""}${team.agents.length} agent${team.agents.length === 1 ? "" : "s"}, ${team.projects.length} project${team.projects.length === 1 ? "" : "s"}. ${team.lead} leads the team and answers to you.`}
        actions={<>
          {manage && <RenameTeam team={team.name} title={team.title} />}
          <Button asChild variant="outline" className="h-10"><Link href={`/tasks?team=${encodeURIComponent(team.name)}`}>Tasks</Link></Button>
          <Button asChild variant="outline" className="h-10"><Link href={`/chat?team=${encodeURIComponent(team.name)}`}><MessageSquare className="size-4" />Chat with {team.lead}</Link></Button>
          {manage && <Button className="h-10" onClick={() => setAdding(true)}><Plus className="size-4" />Add agent</Button>}
        </>} />
      <SetupBanner setup={setup.data} compact />
      <HowTeamRuns team={team} base={teams.data?.version ?? 0} manage={manage} owner={role === "owner"} />
      <WhoDoesWhat team={team} runs={agentRuns} onEdit={manage ? setEditing : undefined} />
      <section className="mb-8">
        <h2 className="mb-3 text-base font-semibold">Who approves</h2>
        <ApprovalModesView modes={team.approvals} lead={team.lead} />
      </section>
      <section className="mb-8">
        <h2 className="text-base font-semibold">Agents</h2>
        <p className="mb-3 text-sm text-muted-foreground">Each agent&apos;s own settings, instructions and skills; what an agent sets here overrides the team settings.</p>
        {team.agents.length === 0 ? <EmptyState title="No agents" icon={Layers} /> : (
          <ul className="grid gap-3 md:grid-cols-2">{team.agents.map((m) => <MemberCard key={m.name} m={m} runs={agentRuns?.[m.name]} onOpen={setOpen} onSkills={manage ? () => setPicking(m.name) : undefined} onEdit={manage ? () => setEditing(m.name) : undefined} />)}</ul>
        )}
      </section>
      <h2 className="mb-3 text-base font-semibold">Projects</h2>
      {team.projects.length === 0 ? <EmptyState title="No projects yet" hint="Create one from the board; it belongs to the team you pick." icon={FolderGit2} /> : (
        <ul className="divide-y rounded-xl border bg-card">
          {team.projects.map((p) => (
            <li key={p}><Link href={`/tasks?project=${encodeURIComponent(p)}`} className="flex min-h-12 items-center gap-3 px-4 py-3 text-sm hover:bg-muted/40"><FolderGit2 className="size-4 text-muted-foreground" />{p}</Link></li>
          ))}
        </ul>
      )}
      <Editor path={open} base={catalog.data?.version ?? 0} onClose={() => setOpen(null)} />
      {manage && <AddAgentSheet team={team} open={adding} onClose={() => setAdding(false)} />}
      <AgentFormSheet name={editing} team={team} onClose={() => setEditing(null)} />
      {team.agents.filter((m) => m.name === picking).map((m) => (
        <SkillPicker key={m.name} member={m} base={catalog.data?.version ?? 0} open onClose={() => setPicking(null)} />
      ))}
    </>
  );
}

"use client";

import { ArrowLeft, ClipboardCheck, Crown, FolderGit2, Layers, MessageSquare, Plus } from "lucide-react";
import { useParams } from "next/navigation";
import { useState } from "react";
import Link from "@/components/app/link";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { Editor, MemberCard } from "@/components/app/team/catalog";
import { SkillPicker } from "@/components/app/team/skill-picker";
import { AddAgentSheet, AgentFormSheet, SetupBanner } from "@/components/app/team/builder";
import { Button } from "@/components/ui/button";
import { useCatalog, useCompanyInfo, useSetup, useTeams } from "@/lib/api/client";
import { canManage } from "@/lib/api/types-account";

// A team (company-teams 7.1): its lead (the owner's unaddressed messages in its projects), its audit assignee,
// its profile, its agents with their prompts and review rules, and its projects.
export default function Page() {
  const name = decodeURIComponent(String(useParams<{ team: string }>().team ?? ""));
  const teams = useTeams();
  const catalog = useCatalog();
  const [open, setOpen] = useState<string | null>(null);
  const [picking, setPicking] = useState<string | null>(null);
  const [editing, setEditing] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const setup = useSetup();
  const manage = canManage(useCompanyInfo().data?.role);
  const team = teams.data?.teams.find((t) => t.name === name);
  const back = <Link href="/teams" className="-ml-2 mb-3 inline-flex h-10 items-center gap-1.5 rounded-md px-2 text-sm text-muted-foreground transition-colors hover:text-foreground"><ArrowLeft className="size-4" />Teams</Link>;
  if (teams.error) return <>{back}<ErrorState error={teams.error} /></>;
  if (teams.isLoading) return <>{back}<LoadingRows /></>;
  if (!team) return <>{back}<EmptyState title={`No team ${name}`} hint="It may have been removed from the catalog." icon={Layers} /></>;
  return (
    <>
      {back}
      <PageHeader title={team.name} description={team.description}
        actions={<>
          <Button asChild variant="outline" className="h-10"><Link href={`/tasks?team=${encodeURIComponent(team.name)}`}>Tasks</Link></Button>
          <Button asChild variant="outline" className="h-10"><Link href={`/chat?team=${encodeURIComponent(team.name)}`}><MessageSquare className="size-4" />Chat with {team.lead}</Link></Button>
          {manage && <Button className="h-10" onClick={() => setAdding(true)}><Plus className="size-4" />Add agent</Button>}
        </>} />
      <SetupBanner setup={setup.data} compact />
      <dl className="mb-6 grid gap-3 sm:grid-cols-3">
        <div className="rounded-xl border bg-card p-4">
          <dt className="flex items-center gap-1.5 text-xs text-muted-foreground"><Crown className="size-3.5" />Lead</dt>
          <dd className="mt-1 font-medium">{team.lead}</dd>
          <dd className="text-xs text-muted-foreground">Gets your messages in this team&apos;s projects</dd>
        </div>
        <div className="rounded-xl border bg-card p-4">
          <dt className="flex items-center gap-1.5 text-xs text-muted-foreground"><ClipboardCheck className="size-3.5" />Audits</dt>
          <dd className="mt-1 font-medium">{team.audit_assignee}</dd>
          <dd className="text-xs text-muted-foreground">Onboards existing repositories</dd>
        </div>
        <div className="rounded-xl border bg-card p-4">
          <dt className="flex items-center gap-1.5 text-xs text-muted-foreground"><Layers className="size-3.5" />Profile</dt>
          <dd className="mt-1 font-medium">{team.profile || "—"}</dd>
          <dd className="text-xs text-muted-foreground">Default settings of its agents</dd>
        </div>
      </dl>
      <h2 className="mb-2 text-sm font-medium text-muted-foreground">Agents</h2>
      {team.agents.length === 0 ? <EmptyState title="No agents" icon={Layers} /> : (
        <ul className="mb-6 grid gap-3 md:grid-cols-2">{team.agents.map((m) => <MemberCard key={m.name} m={m} onOpen={setOpen} onSkills={manage ? () => setPicking(m.name) : undefined} onEdit={manage ? () => setEditing(m.name) : undefined} />)}</ul>
      )}
      <h2 className="mb-2 text-sm font-medium text-muted-foreground">Projects</h2>
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

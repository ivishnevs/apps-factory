"use client";

import { useQueryClient } from "@tanstack/react-query";
import { ArrowRight, Building2, Check, FolderGit2, KeyRound, MessageSquare, Plus, Server, Users } from "lucide-react";
import { useSearchParams } from "next/navigation";
import { Suspense, useEffect, useState } from "react";
import { toast } from "sonner";
import Link, { useRouter } from "@/components/app/link";
import { InstallConnector } from "@/components/app/nodes/enrollment";
import { ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { NewProject } from "@/components/app/project/new-project";
import { NewTeamSheet } from "@/components/app/team/builder";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { core, useCompanyInfo, useMe, useOverview, useSetup, useTeams } from "@/lib/api/client";
import { canManage } from "@/lib/api/types-account";
import { welcomeSeenKey } from "@/lib/company";
import { cn } from "@/lib/utils";

// The setup wizard (team-builder 5.1, saas-web-ui D7): a company's way from sign-up to its first running work —
// its name, a team (from a preset), its agents, a connector, the first project, the secrets the team needs and the
// first task. Every step says whether it is done and does its part here; nothing blocks work, and skipping keeps the
// sign-up company as it is. After the first sign-in it opens with ?welcome=1.
function Step({ n, title, done, children, icon: Icon }: { n: number; title: string; done: boolean; children: React.ReactNode; icon: React.ElementType }) {
  return (
    <li className="flex gap-4 rounded-2xl border bg-card p-4 md:p-5">
      <span className={cn("flex size-9 shrink-0 items-center justify-center rounded-full text-sm font-medium", done ? "bg-emerald-600 text-white" : "bg-muted")}>
        {done ? <Check className="size-4" /> : n}
      </span>
      <div className="min-w-0 flex-1 space-y-3">
        <p className="flex items-center gap-2 font-medium"><Icon className="size-4 text-muted-foreground" />{title}</p>
        <div className="space-y-3 text-sm text-muted-foreground">{children}</div>
      </div>
    </li>
  );
}

function CompanyName({ editable }: { editable: boolean }) {
  const info = useCompanyInfo();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (info.data && !name) setName(info.data.name); }, [info.data, name]);
  if (!editable) return <p>{info.data?.name}</p>;
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await core("/company", { method: "PATCH", body: JSON.stringify({ name: name.trim() }) });
      await Promise.all([qc.invalidateQueries({ queryKey: ["company-info"] }), qc.invalidateQueries({ queryKey: ["me"] })]);
      toast.success("Saved");
    } catch (err) { toast.error(err instanceof Error ? err.message : String(err)); }
    finally { setBusy(false); }
  };
  return (
    <form onSubmit={save} className="flex max-w-md gap-2">
      <Input value={name} onChange={(e) => setName(e.target.value)} maxLength={100} aria-label="Company name" />
      <Button type="submit" variant="outline" className="h-10" disabled={busy || !name.trim() || name.trim() === info.data?.name}>Save</Button>
    </form>
  );
}

function Wizard() {
  const welcome = useSearchParams().get("welcome") === "1";
  const setup = useSetup();
  const teams = useTeams();
  const ov = useOverview();
  const me = useMe();
  const info = useCompanyInfo();
  const router = useRouter();
  const [newTeam, setNewTeam] = useState(false);
  const manage = canManage(info.data?.role);
  const owner = info.data?.role === "owner";
  const finish = async () => {
    if (me.data?.user) await core("/me/welcomed", { method: "POST", body: "{}" }).catch(() => undefined);
    else try { localStorage.setItem(welcomeSeenKey(me.data?.kind ?? ""), "1"); } catch { /* storage blocked */ }
    router.push("/");
  };
  const header = welcome
    ? <PageHeader title="Welcome to App Factory" description="Let's set up your company. Every step can wait: skip now and finish later from Home." actions={<Button variant="ghost" className="h-10" onClick={finish}>Skip setup</Button>} />
    : <PageHeader title="Set up your company" description={setup.data?.ready ? "Everything is in place: your agents can work." : "A few steps until your agents can start working."} />;
  if (setup.error || teams.error) return <>{header}<ErrorState error={setup.error ?? teams.error} /></>;
  if (!setup.data || !teams.data) return <>{header}<LoadingRows /></>;
  const s = setup.data;
  const list = teams.data.teams;
  const agents = list.reduce((n, t) => n + t.agents.length, 0);
  const nodes = ov.data?.nodes ?? [];
  const projects = ov.data?.status ?? [];
  const lead = list[0]?.lead;
  return (
    <>
      {header}
      <ol className="space-y-3">
        <Step n={1} title="Your company" done icon={Building2}>
          <CompanyName editable={owner} />
        </Step>
        <Step n={2} title="A team" done={list.length > 0} icon={Users}>
          {list.length > 0 ? (
            <p>Your teams: {list.map((t) => <Link key={t.name} href={`/teams/${encodeURIComponent(t.name)}`} className="mr-2 font-medium text-foreground underline">{t.name}</Link>)}</p>
          ) : <p>Start from a preset: a solo builder, a product team, a full team, a game studio or a SaaS team.</p>}
          <div className="flex flex-wrap gap-1.5">{list.flatMap((t) => t.agents.map((a) => <Badge key={a.name} variant="secondary" className="font-normal">{a.name} · {a.provider}</Badge>))}</div>
          {manage && <Button variant={list.length ? "outline" : "default"} className="h-10" onClick={() => setNewTeam(true)}><Plus className="size-4" />{list.length ? "Add a team from a preset" : "Choose a team"}</Button>}
          {agents > 0 && <p className="text-xs">Review prompts, models and skills on a team&apos;s page.</p>}
        </Step>
        <Step n={3} title="A machine to run on" done={!s.no_node} icon={Server}>
          {!s.no_node ? (
            <p>Connected: {nodes.map((n) => n.name).join(", ") || "a connector"}. <Link href="/nodes" className="underline">Connectors</Link></p>
          ) : manage ? (
            <>
              <p>Agents run on a machine of yours (a Mac or a Linux server), on your Claude and Codex subscriptions. Install the connector there:</p>
              <div className="rounded-xl border p-4 text-foreground"><InstallConnector /></div>
            </>
          ) : <p>An owner or admin connects a machine.</p>}
        </Step>
        <Step n={4} title="The first project" done={projects.length > 0 && s.teams_without_projects.length === 0} icon={FolderGit2}>
          {projects.length === 0 ? <p>A project is a repository your team works in: a new one or one you have.</p>
            : s.teams_without_projects.length > 0 ? <p>Teams without a project: {s.teams_without_projects.join(", ")}.</p>
              : <p>Projects: {projects.map((p) => p.project.name || p.project.id).join(", ")}.</p>}
          {manage && <NewProject overview={ov.data} variant={projects.length ? "outline" : "default"} label={projects.length ? "Another project" : "Create a project"} />}
        </Step>
        <Step n={5} title="Secrets the team needs" done={s.missing_secrets.length === 0} icon={KeyRound}>
          {s.missing_secrets.length === 0 ? <p>No missing secrets.</p> : (
            <>
              <ul className="space-y-1">{s.missing_secrets.map((x) => <li key={x.name}><span className="font-mono text-foreground">{x.name}</span> ({x.kind}) for {x.agents.join(", ")}</li>)}</ul>
              <Button asChild className="h-10"><Link href="/secrets">Set secrets</Link></Button>
            </>
          )}
        </Step>
        <Step n={6} title="The first task" done={false} icon={MessageSquare}>
          <p>Tell {lead ? <span className="font-medium text-foreground">{lead}</span> : "your team's lead"} what to build. The lead plans it, splits the work and asks you when it needs a decision.</p>
          <div className="flex flex-wrap gap-2">
            <Button asChild className="h-10" disabled={!s.ready}><Link href="/chat">Talk to the lead</Link></Button>
            <Button asChild variant="outline" className="h-10"><Link href="/tasks">Open the board</Link></Button>
          </div>
          {!s.ready && <p className="text-xs">Work starts once the steps above are done; tasks you add now wait with their reason.</p>}
        </Step>
      </ol>
      <div className="mt-6 flex justify-end">
        <Button className="h-10" onClick={finish}>{s.ready ? "Go to Home" : "Finish later"}<ArrowRight className="size-4" /></Button>
      </div>
      <NewTeamSheet open={newTeam} onClose={() => setNewTeam(false)} />
    </>
  );
}

export default function Page() {
  return <Suspense fallback={<LoadingRows />}><Wizard /></Suspense>;
}

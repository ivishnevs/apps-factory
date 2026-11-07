"use client";

import { BookOpen, ChevronRight, Crown, FileText, FolderGit2, History, Plus, Users } from "lucide-react";
import { useSearchParams } from "next/navigation";
import { Suspense, useMemo, useState } from "react";
import Link from "@/components/app/link";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { Editor, HistoryList } from "@/components/app/team/catalog";
import { NewTeamSheet, SetupBanner } from "@/components/app/team/builder";
import { CatalogChanges, CatalogFile } from "@/components/app/team/catalog-tools";
import { PresetLibrary } from "@/components/app/team/library";
import { Button } from "@/components/ui/button";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useCatalog, useCompanyInfo, useSetup, useTeams } from "@/lib/api/client";
import { canManage } from "@/lib/api/types-account";
import { documentGroup } from "@/lib/api/types-catalog";
import { ago } from "@/lib/format";

// Teams (company-teams 7.1): the company's teams, each with its lead, agents and projects (a team page each), the
// catalog's documents with an editor and the version history. Nodes, secrets, skills, profiles and policies stay
// company-wide.
function Teams() {
  const teams = useTeams();
  const catalog = useCatalog();
  const welcome = useSearchParams().get("welcome");
  const [open, setOpen] = useState<string | null>(null);
  const [newTeam, setNewTeam] = useState(false);
  const manage = canManage(useCompanyInfo().data?.role);
  const setup = useSetup();
  const version = catalog.data?.version ?? 0;
  const groups = useMemo(() => {
    const g = new Map<string, string[]>();
    for (const d of catalog.data?.documents ?? []) {
      const k = documentGroup(d.path);
      g.set(k, [...(g.get(k) ?? []), d.path]);
    }
    return [...g.entries()];
  }, [catalog.data]);
  const error = teams.error ?? catalog.error;
  return (
    <>
      <PageHeader title="Teams" description={catalog.data ? <>Last changed {ago(catalog.data.created_at)} by {catalog.data.author === "operator" ? "the operator" : catalog.data.author.replace(/^user:/, "a member")} · version {version}</> : "Your company's teams, agents, prompts and policies"}
        actions={manage ? <Button className="h-10" onClick={() => setNewTeam(true)}><Plus className="size-4" />New team</Button> : undefined} />
      <SetupBanner setup={setup.data} />
      {welcome && (
        <Alert className="mb-5"><AlertDescription>Your company is ready with a starter team. Review its agents, connect a machine and create your first project.</AlertDescription></Alert>
      )}
      {error && <ErrorState error={error} />}
      <Tabs defaultValue="teams">
        <TabsList className="mb-4">
          <TabsTrigger value="teams"><Users className="size-4" />Teams</TabsTrigger>
          <TabsTrigger value="library"><BookOpen className="size-4" />Library</TabsTrigger>
          <TabsTrigger value="documents"><FileText className="size-4" />Advanced (YAML)</TabsTrigger>
          <TabsTrigger value="history"><History className="size-4" />History</TabsTrigger>
        </TabsList>
        <TabsContent value="teams">
          {teams.isLoading ? <LoadingRows /> : (teams.data?.teams.length ?? 0) === 0 ? <EmptyState title="No teams yet" hint="Create one from a preset with New team." icon={Users} /> : (
            <ul className="grid gap-3 md:grid-cols-2">
              {teams.data!.teams.map((t) => (
                <li key={t.name}>
                  <Link href={`/teams/${encodeURIComponent(t.name)}`} className="flex h-full flex-col gap-2 rounded-xl border bg-card p-4 transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                    <span className="flex items-center gap-2">
                      <span className="min-w-0 flex-1 truncate font-medium">{t.name}</span>
                      <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
                    </span>
                    {t.description && <span className="text-sm text-muted-foreground">{t.description}</span>}
                    <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
                      <span className="inline-flex items-center gap-1"><Crown className="size-3.5" />{t.lead}</span>
                      <span className="inline-flex items-center gap-1"><Users className="size-3.5" />{t.agents.length} {t.agents.length === 1 ? "agent" : "agents"}</span>
                      <span className="inline-flex items-center gap-1"><FolderGit2 className="size-3.5" />{t.projects.length} {t.projects.length === 1 ? "project" : "projects"}</span>
                    </span>
                    <span className="flex flex-wrap gap-1.5">{t.agents.map((a) => <Badge key={a.name} variant="secondary" className="font-normal">{a.name}</Badge>)}</span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </TabsContent>
        <TabsContent value="documents">
          {catalog.isLoading ? <LoadingRows /> : (
            <div className="space-y-4">
              {groups.map(([name, paths]) => (
                <section key={name}>
                  <h2 className="mb-2 text-sm font-medium text-muted-foreground">{name}</h2>
                  <ul className="divide-y rounded-xl border bg-card">
                    {paths.map((p) => (
                      <li key={p}><button className="flex w-full items-center gap-3 px-4 py-3 text-left hover:bg-muted/40" onClick={() => setOpen(p)}>
                        <FileText className="size-4 shrink-0 text-muted-foreground" /><span className="min-w-0 flex-1 truncate font-mono text-sm">{p}</span>
                      </button></li>
                    ))}
                  </ul>
                </section>
              ))}
            </div>
          )}
        </TabsContent>
        <TabsContent value="library"><PresetLibrary manage={manage} /></TabsContent>
        <TabsContent value="history">
          <div className="space-y-6">
            <section><h2 className="mb-2 text-sm font-medium text-muted-foreground">Changes</h2><CatalogChanges manage={manage} /></section>
            <section><h2 className="mb-2 text-sm font-medium text-muted-foreground">The whole configuration as a file</h2><CatalogFile manage={manage} /></section>
            <section><h2 className="mb-2 text-sm font-medium text-muted-foreground">Versions</h2><HistoryList current={version} /></section>
          </div>
        </TabsContent>
      </Tabs>
      <Editor path={open} base={version} onClose={() => setOpen(null)} />
      {manage && <NewTeamSheet open={newTeam} onClose={() => setNewTeam(false)} />}
    </>
  );
}

export default function Page() {
  return <Suspense><Teams /></Suspense>;
}


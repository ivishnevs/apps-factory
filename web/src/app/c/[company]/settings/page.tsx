"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Building2, Clock, Copy, FolderGit2, KeyRound, LogOut, Pencil, Trash2, UserRound, Users } from "lucide-react";
import { usePathname, useRouter as useNextRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { core, signOut, useCompanyInfo, useMe, useTeams } from "@/lib/api/client";
import { canManage, type Role } from "@/lib/api/types-account";
import type { Project } from "@/lib/api/types";
import { ago } from "@/lib/format";

// Settings of this company (saas-web-ui 2.3): General (name, time zone), Members (roles), Projects (name,
// repository, branch, team), Access tokens (for the CLI and scripts) and your Account. Switching and adding
// companies is in the company menu. The tab is in the address (?tab=members) so links and reloads land on it.
const roleWords: Record<Role, string> = {
  owner: "Everything, including members and the company's name",
  admin: "Teams, skills, secrets, connectors, projects and tokens",
  member: "Tasks, chats and approvals",
};

const fail = (e: unknown) => toast.error(e instanceof Error ? e.message : String(e));

function Section({ title, description, children, icon: Icon }: { title: string; description?: string; children: React.ReactNode; icon: React.ElementType }) {
  return (
    <section className="rounded-2xl border bg-card p-4 sm:p-5">
      <h2 className="flex items-center gap-2 font-medium"><Icon className="size-4 text-muted-foreground" />{title}</h2>
      {description && <p className="mt-1 text-sm text-muted-foreground">{description}</p>}
      <div className="mt-4">{children}</div>
    </section>
  );
}

function General({ role }: { role: Role }) {
  const info = useCompanyInfo();
  const qc = useQueryClient();
  const tz = useQuery({ queryKey: ["time-zone"], queryFn: () => core<{ time_zone: string }>("/settings/company/time_zone") });
  const [name, setName] = useState("");
  const [zone, setZone] = useState("");
  useEffect(() => { if (info.data) setName(info.data.name); }, [info.data]);
  useEffect(() => { if (tz.data) setZone(tz.data.time_zone); }, [tz.data]);
  const owner = role === "owner";
  const manage = canManage(role);
  const zones = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
  const rename = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      await core("/company", { method: "PATCH", body: JSON.stringify({ name: name.trim() }) });
      await Promise.all([qc.invalidateQueries({ queryKey: ["company-info"] }), qc.invalidateQueries({ queryKey: ["me"] })]);
      toast.success("Name saved");
    } catch (err) { fail(err); }
  };
  const saveZone = async (e: React.FormEvent) => {
    e.preventDefault();
    try { await core("/settings/company/time_zone", { method: "PUT", body: JSON.stringify({ time_zone: zone }) }); await tz.refetch(); toast.success("Time zone saved"); }
    catch (err) { fail(err); }
  };
  if (!info.data) return <LoadingRows />;
  return (
    <div className="space-y-4">
      <Section title="Company" icon={Building2} description={`Its address stays /c/${info.data.id} when you rename it.`}>
        <form onSubmit={rename} className="flex flex-col gap-3 sm:flex-row sm:items-end">
          <label className="flex-1 space-y-1.5 text-sm"><span className="font-medium">Name</span>
            <Input value={name} onChange={(e) => setName(e.target.value)} disabled={!owner} maxLength={200} required />
          </label>
          {owner && <Button type="submit" className="h-10" disabled={!name.trim() || name.trim() === info.data.name}>Save</Button>}
        </form>
        {!owner && <p className="mt-2 text-xs text-muted-foreground">Only an owner renames the company.</p>}
      </Section>
      <Section title="Time zone" icon={Clock} description="Daily digests, schedules and timers of the company use it.">
        <form onSubmit={saveZone} className="flex flex-col gap-3 sm:flex-row sm:items-end">
          <label className="flex-1 space-y-1.5 text-sm"><span className="font-medium">Time zone</span>
            <select className="h-10 w-full rounded-md border bg-background px-2 text-sm disabled:opacity-60" value={zone} onChange={(e) => setZone(e.target.value)} disabled={!manage || !tz.data}>
              {zone && !zones.includes(zone) && <option value={zone}>{zone}</option>}
              {zones.map((z) => <option key={z} value={z}>{z}</option>)}
            </select>
          </label>
          {manage && <Button type="submit" className="h-10" disabled={!tz.data || zone === tz.data.time_zone}>Save</Button>}
        </form>
        {tz.error ? <div className="mt-3"><ErrorState error={tz.error} /></div> : null}
      </Section>
    </div>
  );
}

interface Member { user: string; email: string; name: string; role: Role; created_at: number }

function Members({ role }: { role: Role }) {
  const me = useMe();
  const members = useQuery({ queryKey: ["members"], queryFn: () => core<Member[]>("/memberships") });
  const confirm = useConfirm();
  const owner = role === "owner";
  const setRole = async (m: Member, next: Role) => {
    try { await core(`/memberships/${encodeURIComponent(m.user)}`, { method: "PATCH", body: JSON.stringify({ role: next }) }); await members.refetch(); toast.success(`${m.email} is now ${next}`); }
    catch (err) { fail(err); }
  };
  const remove = async (m: Member) => {
    const self = m.user === me.data?.user?.id;
    if (!(await confirm({ title: self ? "Leave this company?" : `Remove ${m.email}?`, description: self ? "You lose access to it at once." : "They lose access to this company at once. Their work stays.", confirm: self ? "Leave" : "Remove", destructive: true }))) return;
    try {
      await core(`/memberships/${encodeURIComponent(m.user)}`, { method: "DELETE" });
      if (self) { window.location.assign("/welcome"); return; }
      await members.refetch(); toast.success(`${m.email} removed`);
    } catch (err) { fail(err); }
  };
  return (
    <Section title="Members" icon={Users} description="People of this company and what their role lets them do.">
      {members.error ? <ErrorState error={members.error} /> : !members.data ? <LoadingRows rows={2} /> : (
        <ul className="divide-y rounded-xl border">
          {members.data.map((m) => (
            <li key={m.user} className="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center">
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">{m.name || m.email}{m.user === me.data?.user?.id && <span className="ml-2 text-xs font-normal text-muted-foreground">you</span>}</p>
                <p className="truncate text-xs text-muted-foreground">{m.email} · {roleWords[m.role]}</p>
              </div>
              {owner ? (
                <div className="flex gap-2">
                  <select aria-label={`Role of ${m.email}`} className="h-9 rounded-md border bg-background px-2 text-sm" value={m.role} onChange={(e) => setRole(m, e.target.value as Role)}>
                    <option value="owner">Owner</option><option value="admin">Admin</option><option value="member">Member</option>
                  </select>
                  <Button variant="outline" size="sm" className="h-9" onClick={() => remove(m)} aria-label={`Remove ${m.email}`}><Trash2 className="size-4" /></Button>
                </div>
              ) : <Badge variant="secondary" className="self-start capitalize sm:self-center">{m.role}</Badge>}
            </li>
          ))}
          {members.data.length === 0 && <li className="px-4 py-3 text-sm text-muted-foreground">No personal accounts here yet: you are using an access token.</li>}
        </ul>
      )}
      <p className="mt-3 text-xs text-muted-foreground">Inviting people by email is not available yet.</p>
    </Section>
  );
}

function ProjectSheet({ project, onClose }: { project: Project; onClose: () => void }) {
  const teams = useTeams();
  const qc = useQueryClient();
  const [f, setF] = useState({ name: project.name, description: project.description ?? "", repo_url: project.repo_url ?? "", default_branch: project.default_branch ?? "", team: project.team ?? "" });
  const [busy, setBusy] = useState(false);
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const body: Record<string, unknown> = { version: project.version, name: f.name.trim(), description: f.description.trim(), repo_url: f.repo_url.trim(), default_branch: f.default_branch.trim() };
      if (f.team && f.team !== (project.team ?? "")) body.team = f.team;
      await core(`/projects/${encodeURIComponent(project.id)}`, { method: "PATCH", body: JSON.stringify(body) });
      await Promise.all([qc.invalidateQueries({ queryKey: ["projects"] }), qc.invalidateQueries({ queryKey: ["overview"] })]);
      toast.success("Project saved");
      onClose();
    } catch (err) { fail(err); setBusy(false); }
  };
  const field = (k: keyof typeof f, label: string, hint?: string) => (
    <label className="block space-y-1.5 text-sm"><span className="font-medium">{label}</span>
      <Input value={f[k]} onChange={(e) => setF({ ...f, [k]: e.target.value })} />
      {hint && <span className="block text-xs text-muted-foreground">{hint}</span>}
    </label>
  );
  return (
    <Sheet open onOpenChange={(o) => !o && onClose()}>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-md">
        <SheetHeader className="border-b px-5 py-4"><SheetTitle>{project.name}</SheetTitle><SheetDescription>Project <span className="font-mono">{project.id}</span></SheetDescription></SheetHeader>
        <form onSubmit={save} className="space-y-4 overflow-y-auto px-5 py-5">
          {field("name", "Name")}
          {field("description", "Description")}
          {field("repo_url", "Repository", "The git URL agents clone and push to.")}
          {field("default_branch", "Default branch", "Pull requests go into it.")}
          <label className="block space-y-1.5 text-sm"><span className="font-medium">Team</span>
            <select className="h-10 w-full rounded-md border bg-background px-2" value={f.team} onChange={(e) => setF({ ...f, team: e.target.value })}>
              {!f.team && <option value="">Choose a team</option>}
              {(teams.data?.teams ?? []).map((t) => <option key={t.name} value={t.name}>{t.name}</option>)}
            </select>
            <span className="block text-xs text-muted-foreground">Moving a project to another team works only while it has no tasks in progress.</span>
          </label>
          <div className="flex gap-2 pt-2">
            <Button type="button" variant="outline" className="h-10 flex-1" onClick={onClose}>Cancel</Button>
            <Button type="submit" className="h-10 flex-1" disabled={busy || !f.name.trim()}>Save</Button>
          </div>
        </form>
      </SheetContent>
    </Sheet>
  );
}

function Projects({ role }: { role: Role }) {
  const projects = useQuery({ queryKey: ["projects"], queryFn: () => core<Project[]>("/projects") });
  const [editing, setEditing] = useState<Project | null>(null);
  const manage = canManage(role);
  return (
    <Section title="Projects" icon={FolderGit2} description="Each project is a repository a team works in. Create new ones from the board.">
      {projects.error ? <ErrorState error={projects.error} /> : !projects.data ? <LoadingRows rows={2} /> : projects.data.length === 0 ? (
        <EmptyState icon={FolderGit2} title="No projects yet" hint="Create the first one from the board or the setup steps." className="py-8" />
      ) : (
        <ul className="divide-y rounded-xl border">
          {projects.data.map((p) => (
            <li key={p.id} className="flex items-center gap-3 px-4 py-3">
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">{p.name}{p.team && <span className="ml-2 text-xs font-normal text-muted-foreground">team {p.team}</span>}</p>
                <p className="truncate text-xs text-muted-foreground">{p.repo_url || "No repository yet"}{p.default_branch ? ` · ${p.default_branch}` : ""}</p>
              </div>
              {manage && <Button variant="outline" size="sm" className="h-9" onClick={() => setEditing(p)}><Pencil className="size-4" />Edit</Button>}
            </li>
          ))}
        </ul>
      )}
      {editing && <ProjectSheet project={editing} onClose={() => setEditing(null)} />}
    </Section>
  );
}

interface OwnerToken { id: string; name: string; created_by?: string; created_at: number; revoked_at?: number; last_used_at?: number }

function Tokens() {
  const tokens = useQuery({ queryKey: ["owner-tokens"], queryFn: () => core<OwnerToken[]>("/owner-tokens") });
  const confirm = useConfirm();
  const [name, setName] = useState("");
  const [issued, setIssued] = useState<{ name: string; token: string } | null>(null);
  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    try { const t = await core<OwnerToken & { token: string }>("/owner-tokens", { method: "POST", body: JSON.stringify({ name: name.trim() }) }); setIssued(t); setName(""); await tokens.refetch(); }
    catch (err) { fail(err); }
  };
  const revoke = async (t: OwnerToken) => {
    if (!(await confirm({ title: `Revoke ${t.name || "this token"}?`, description: "Scripts and CLIs using it stop working at once.", confirm: "Revoke", destructive: true }))) return;
    try { await core(`/owner-tokens/${encodeURIComponent(t.id)}/revoke`, { method: "POST", body: "{}" }); await tokens.refetch(); toast.success("Token revoked"); }
    catch (err) { fail(err); }
  };
  const live = (tokens.data ?? []).filter((t) => !t.revoked_at);
  return (
    <Section title="Access tokens" icon={KeyRound} description="For the factory CLI and scripts. A token acts as an admin of this company.">
      <form onSubmit={create} className="flex flex-col gap-3 sm:flex-row sm:items-end">
        <label className="flex-1 space-y-1.5 text-sm"><span className="font-medium">Name</span>
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="My laptop CLI" maxLength={200} required />
        </label>
        <Button type="submit" className="h-10" disabled={!name.trim()}>Create token</Button>
      </form>
      {issued && (
        <div className="mt-4 space-y-2 rounded-xl border border-amber-500/40 bg-amber-500/5 p-4 text-sm">
          <p className="font-medium">Copy “{issued.name}” now — it is shown only once.</p>
          <div className="flex items-start gap-2">
            <code className="min-w-0 flex-1 break-all rounded-md border bg-background px-2 py-2 font-mono text-xs">{issued.token}</code>
            <Button variant="outline" size="sm" className="h-9 shrink-0" onClick={() => navigator.clipboard.writeText(issued.token).then(() => toast.success("Copied"))}><Copy className="size-4" />Copy</Button>
          </div>
          <p className="text-xs text-muted-foreground">Use it as <span className="font-mono">FACTORY_TOKEN</span> with <span className="font-mono">FACTORY_URL={typeof window !== "undefined" ? window.location.origin : ""}</span>.</p>
          <Button variant="ghost" size="sm" onClick={() => setIssued(null)}>Done</Button>
        </div>
      )}
      <div className="mt-4">
        {tokens.error ? <ErrorState error={tokens.error} /> : !tokens.data ? <LoadingRows rows={1} /> : live.length === 0 ? (
          <p className="text-sm text-muted-foreground">No tokens.</p>
        ) : (
          <ul className="divide-y rounded-xl border">
            {live.map((t) => (
              <li key={t.id} className="flex items-center gap-3 px-4 py-3">
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">{t.name || "Unnamed"}</p>
                  <p className="truncate text-xs text-muted-foreground">Created {ago(t.created_at)}{t.last_used_at ? ` · used ${ago(t.last_used_at)}` : " · never used"}</p>
                </div>
                <Button variant="outline" size="sm" className="h-9" onClick={() => revoke(t)}>Revoke</Button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Section>
  );
}

function Account() {
  const me = useMe();
  if (me.data?.kind !== "session") return <Section title="Account" icon={UserRound}><p className="text-sm text-muted-foreground">You are using an access token, not a personal sign-in.</p></Section>;
  return (
    <Section title="Account" icon={UserRound}>
      <p className="text-sm">{me.data.user?.name && <span className="font-medium">{me.data.user.name} · </span>}<span className="text-muted-foreground">{me.data.user?.email}</span></p>
      <Button variant="outline" className="mt-4 h-10" onClick={signOut}><LogOut className="size-4" />Sign out</Button>
    </Section>
  );
}

const tabs = ["general", "members", "projects", "tokens", "account"] as const;
type Tab = (typeof tabs)[number];

function SettingsPage() {
  const info = useCompanyInfo();
  const params = useSearchParams();
  const router = useNextRouter();
  const path = usePathname();
  const tab = (tabs as readonly string[]).includes(params.get("tab") ?? "") ? (params.get("tab") as Tab) : "general";
  if (info.error) return <><PageHeader title="Settings" /><ErrorState error={info.error} /></>;
  if (!info.data) return <><PageHeader title="Settings" /><LoadingRows /></>;
  const role = info.data.role;
  const manage = canManage(role);
  const visible = tabs.filter((t) => t !== "tokens" || manage);
  const words: Record<Tab, string> = { general: "General", members: "Members", projects: "Projects", tokens: "Access tokens", account: "Account" };
  return (
    <>
      <PageHeader title="Settings" description={<>{info.data.name} · your role: <span className="font-medium capitalize">{role}</span> ({roleWords[role].toLowerCase()})</>} />
      <Tabs value={tab} onValueChange={(v) => router.replace(`${path}?tab=${v}`, { scroll: false })}>
        <TabsList className="mb-4 h-auto flex-wrap">
          {visible.map((t) => <TabsTrigger key={t} value={t}>{words[t]}</TabsTrigger>)}
        </TabsList>
        <TabsContent value="general"><General role={role} /></TabsContent>
        <TabsContent value="members"><Members role={role} /></TabsContent>
        <TabsContent value="projects"><Projects role={role} /></TabsContent>
        {manage && <TabsContent value="tokens"><Tokens /></TabsContent>}
        <TabsContent value="account"><Account /></TabsContent>
      </Tabs>
    </>
  );
}

export default function Page() {
  return <Suspense fallback={<LoadingRows />}><SettingsPage /></Suspense>;
}

"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Building2, ChevronRight, Clock, Copy, FolderGit2, KeyRound, LogOut, MessageCircle, ScrollText, Timer, Trash2, UserRound, Users } from "lucide-react";
import Link from "@/components/app/link";
import { usePathname, useRouter as useNextRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { CompanyRules } from "@/components/app/settings/company-rules";
import { SessionSettings } from "@/components/app/settings/sessions";
import { TelegramSettings } from "@/components/app/settings/telegram";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { core, signOut, useCompanyInfo, useMe } from "@/lib/api/client";
import { canManage, type Role } from "@/lib/api/types-account";
import type { Project } from "@/lib/api/types";
import { ago } from "@/lib/format";

// Settings of this company (saas-web-ui 2.3): General (name, time zone), Members (roles), Projects (name,
// repository, branch, team), Integrations (Telegram), Access tokens (for the CLI and scripts) and your Account. Switching and adding
// companies is in the company menu. The tab is in the address (?tab=members) so links and reloads land on it.
const roleWords: Record<Role, string> = {
  owner: "Everything, including members and the company's name",
  admin: "Teams, skills, secrets, nodes, projects and tokens",
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
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (info.data) setName(info.data.name); }, [info.data]);
  useEffect(() => { if (tz.data) setZone(tz.data.time_zone); }, [tz.data]);
  const owner = role === "owner";
  const manage = canManage(role);
  const zones = typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
  const rename = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      await core("/company", { method: "PATCH", body: JSON.stringify({ name: name.trim() }) });
      await Promise.all([qc.invalidateQueries({ queryKey: ["company-info"] }), qc.invalidateQueries({ queryKey: ["me"] })]);
      toast.success("Name saved");
    } catch (err) { fail(err); }
    finally { setBusy(false); }
  };
  const saveZone = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try { await core("/settings/company/time_zone", { method: "PUT", body: JSON.stringify({ time_zone: zone }) }); await tz.refetch(); toast.success("Time zone saved"); }
    catch (err) { fail(err); }
    finally { setBusy(false); }
  };
  if (!info.data) return <LoadingRows />;
  return (
    <div className="space-y-4">
      <Section title="Company" icon={Building2} description={`Its address stays /c/${info.data.id} when you rename it.`}>
        <form onSubmit={rename} className="flex flex-col gap-3 sm:flex-row sm:items-end">
          <label className="flex-1 space-y-1.5 text-sm"><span className="font-medium">Name</span>
            <Input value={name} onChange={(e) => setName(e.target.value)} disabled={!owner} maxLength={200} required />
          </label>
          {owner && <Button type="submit" className="h-10" disabled={busy || !name.trim() || name.trim() === info.data.name}>Save</Button>}
        </form>
        {!owner && <p className="mt-2 text-xs text-muted-foreground">Only an owner renames the company.</p>}
      </Section>
      <Section title="Time zone" icon={Clock} description="The daily budget and the daily digest count days in it.">
        <form onSubmit={saveZone} className="flex flex-col gap-3 sm:flex-row sm:items-end">
          <label className="flex-1 space-y-1.5 text-sm"><span className="font-medium">Time zone</span>
            <select className="h-10 w-full rounded-md border bg-background px-2 text-sm disabled:opacity-60" value={zone} onChange={(e) => setZone(e.target.value)} disabled={!manage || !tz.data}>
              {!zone && <option value="">Not set (UTC)</option>}
              {zone && !zones.includes(zone) && <option value={zone}>{zone}</option>}
              {zones.map((z) => <option key={z} value={z}>{z}</option>)}
            </select>
          </label>
          {manage && <Button type="submit" className="h-10" disabled={busy || !tz.data || zone === tz.data.time_zone}>Save</Button>}
        </form>
        {tz.error ? <div className="mt-3"><ErrorState error={tz.error} /></div> : null}
      </Section>
      <Section title="Sessions" icon={Timer} description="How long runs wait for the machine that holds their agent's session.">
        <SessionSettings manage={manage} />
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
      {owner && <Invitations />}
    </Section>
  );
}

type Invitation = { id: string; role: Role; created_at: number; expires_at: number };

// Invitation links (team-invitations): an owner makes a single-use link for a role and sends it however they like;
// whoever opens it and signs in joins this company. Open links are listed and can be revoked.
function Invitations() {
  const links = useQuery({ queryKey: ["invitations"], queryFn: () => core<Invitation[]>("/invitations") });
  const [role, setRole] = useState<Role>("member");
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const copy = async (text: string) => {
    try { await navigator.clipboard.writeText(text); toast.success("Link copied"); } catch { toast.error("Copy failed: select the link and copy it"); }
  };
  const create = async () => {
    setBusy(true);
    try {
      const out = await core<{ url: string }>("/invitations", { method: "POST", body: JSON.stringify({ role }) });
      setUrl(out.url);
      await links.refetch();
      await copy(out.url);
    } catch (err) { fail(err); }
    finally { setBusy(false); }
  };
  const revoke = async (id: string) => {
    try { await core(`/invitations/${encodeURIComponent(id)}`, { method: "DELETE" }); await links.refetch(); setUrl(""); toast.success("Link revoked"); }
    catch (err) { fail(err); }
  };
  const days = (t: number) => Math.max(1, Math.ceil((t * 1000 - Date.now()) / 86400000));
  return (
    <div className="mt-5 space-y-3 border-t pt-4">
      <div>
        <h3 className="text-sm font-medium">Invite people</h3>
        <p className="mt-1 text-sm text-muted-foreground">Create a link and send it to one person. It works once and for 7 days; they sign in with Google or GitHub and join with the role you choose.</p>
      </div>
      <div className="flex flex-wrap gap-2">
        <select aria-label="Role for the invited person" className="h-10 rounded-md border bg-background px-2 text-sm" value={role} onChange={(e) => setRole(e.target.value as Role)}>
          <option value="member">Member</option><option value="admin">Admin</option>
        </select>
        <Button className="h-10" onClick={create} disabled={busy}>Create invite link</Button>
      </div>
      <p className="text-xs text-muted-foreground">{role === "admin" ? "An admin manages" : "A member works with"}: {roleWords[role].toLowerCase()}. To make someone an owner, change their role after they join.</p>
      {url && (
        <div className="flex gap-2">
          <Input readOnly value={url} aria-label="Invite link" onFocus={(e) => e.target.select()} className="font-mono text-xs" />
          <Button variant="outline" className="h-10" onClick={() => copy(url)} aria-label="Copy invite link"><Copy className="size-4" /></Button>
        </div>
      )}
      {url && <p className="text-xs text-muted-foreground">The link is shown only now. Lost it? Revoke it and create a new one.</p>}
      {links.error ? <ErrorState error={links.error} /> : links.data && links.data.length > 0 && (
        <ul className="divide-y rounded-xl border">
          {links.data.map((l) => (
            <li key={l.id} className="flex items-center gap-3 px-4 py-2.5 text-sm">
              <span className="min-w-0 flex-1 truncate">Link for a <span className="font-medium">{l.role}</span> · created {ago(l.created_at)} · expires in {days(l.expires_at)} {days(l.expires_at) === 1 ? "day" : "days"}</span>
              <Button variant="outline" size="sm" className="h-9" onClick={() => revoke(l.id)}>Revoke</Button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// Projects moved to their own pages (projects-page 1.3): Settings lists them and links there.
function Projects() {
  const projects = useQuery({ queryKey: ["projects"], queryFn: () => core<Project[]>("/projects") });
  return (
    <Section title="Projects" icon={FolderGit2} description="Each project has its own page: goal and direction, rules, repository, team and archiving.">
      {projects.error ? <ErrorState error={projects.error} /> : !projects.data ? <LoadingRows rows={2} /> : projects.data.length === 0 ? (
        <EmptyState icon={FolderGit2} title="No projects yet" hint="Create the first one on the Projects page." className="py-8" />
      ) : (
        <ul className="divide-y rounded-xl border">
          {projects.data.map((p) => (
            <li key={p.id}>
              <Link href={`/projects/${encodeURIComponent(p.id)}`} className="flex items-center gap-3 px-4 py-3 hover:bg-accent/50">
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium">{p.name}{p.team && <span className="ml-2 text-xs font-normal text-muted-foreground">team {p.team}</span>}{p.archived && <span className="ml-2 text-xs font-normal text-muted-foreground">archived</span>}</p>
                  <p className="truncate text-xs text-muted-foreground">{p.repo_url || "No repository yet"}{p.default_branch ? ` · ${p.default_branch}` : ""}</p>
                </div>
                <ChevronRight className="size-4 text-muted-foreground" />
              </Link>
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

interface OwnerToken { id: string; name: string; created_by?: string; created_at: number; revoked_at?: number; last_used_at?: number }

function Tokens() {
  const tokens = useQuery({ queryKey: ["owner-tokens"], queryFn: () => core<OwnerToken[]>("/owner-tokens") });
  const confirm = useConfirm();
  const [name, setName] = useState("");
  const [issued, setIssued] = useState<{ name: string; token: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try { const t = await core<OwnerToken & { token: string }>("/owner-tokens", { method: "POST", body: JSON.stringify({ name: name.trim() }) }); setIssued(t); setName(""); await tokens.refetch(); }
    catch (err) { fail(err); }
    finally { setBusy(false); }
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
        <Button type="submit" className="h-10" disabled={busy || !name.trim()}>Create token</Button>
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

const tabs = ["general", "rules", "members", "projects", "integrations", "tokens", "account"] as const;
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
  const words: Record<Tab, string> = { general: "General", rules: "Company rules", members: "Members", projects: "Projects", integrations: "Integrations", tokens: "Tokens", account: "Account" };
  return (
    <>
      <PageHeader title="Settings" description={<>{info.data.name} · your role: <span className="font-medium capitalize">{role}</span> ({roleWords[role].toLowerCase()})</>} />
      <Tabs value={tab} onValueChange={(v) => router.replace(`${path}?tab=${v}`, { scroll: false })}>
        <TabsList className="mb-4 h-auto max-w-full justify-start overflow-x-auto [scrollbar-width:none]">
          {visible.map((t) => <TabsTrigger key={t} value={t}>{words[t]}</TabsTrigger>)}
        </TabsList>
        <TabsContent value="general"><General role={role} /></TabsContent>
        <TabsContent value="rules">
          <Section title="Company rules" icon={ScrollText} description="Standing rules for every agent of the company: each agent's brief carries them in every run. A role's own work belongs in its prompt (Teams & agents); a project's facts in its rules on the project page.">
            <CompanyRules manage={manage} />
          </Section>
        </TabsContent>
        <TabsContent value="members"><Members role={role} /></TabsContent>
        <TabsContent value="projects"><Projects /></TabsContent>
        <TabsContent value="integrations">
          <Section title="Telegram" icon={MessageCircle} description="Questions, alerts and the daily digest in Telegram, answered from there.">
            <TelegramSettings manage={manage} />
          </Section>
        </TabsContent>
        {manage && <TabsContent value="tokens"><Tokens /></TabsContent>}
        <TabsContent value="account"><Account /></TabsContent>
      </Tabs>
    </>
  );
}

export default function Page() {
  return <Suspense fallback={<LoadingRows />}><SettingsPage /></Suspense>;
}

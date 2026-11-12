"use client";

import { useQueryClient } from "@tanstack/react-query";
import { ClipboardCheck, Crown, SlidersHorizontal } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { LeadAuthority } from "@/components/app/team/authority";
import { Field, fieldError } from "@/components/app/team/builder";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ApiError, core } from "@/lib/api/client";
import type { AgentRuns } from "@/lib/api/types";
import type { TeamMember, TeamSettings, TeamView } from "@/lib/api/types-catalog";

// The team page's explanation of how the team runs (team-page-clarity): the lead (required, one), the agent that
// onboards an added repository (optional, the lead by default) and the team settings (the team profile every agent
// starts from), each with what it does and how to change it; then who does what. Changes go to Core's
// PUT /catalog/teams/{name} on the catalog version the page read; Core validates them like any catalog change.

const providerName = (p?: string) => (p === "claude" ? "Claude" : p === "codex" ? "Codex" : p || "not set");
const you = (n: string) => (n === "owner" ? "you" : n);

// duration turns a Go duration ("30m", "2h", "1h30m") into words.
export function duration(d?: string) {
  if (!d) return "not set";
  const m = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/.exec(d);
  if (!m || !d) return d;
  const parts = [m[1] && `${m[1]} h`, m[2] && `${m[2]} min`, m[3] && `${m[3]} s`].filter(Boolean);
  return parts.length ? parts.join(" ") : d;
}

// tasksAtOnce is the settings' work-run limit, or the role defaults of the team's roles.
function tasksAtOnce(s: TeamSettings, agents: TeamMember[]) {
  if (s.max_concurrent_runs) return `${s.max_concurrent_runs} per agent`;
  const limit = (role: string) => s.role_limits[role] ?? s.role_limits.default ?? 1;
  const roles = [...new Set(agents.map((a) => a.role).filter(Boolean))].sort();
  const listed = roles.filter((r) => r in s.role_limits);
  const others = roles.filter((r) => !(r in s.role_limits));
  const parts = listed.map((r) => `${r} ${limit(r)}`);
  if (others.length) parts.push(`${listed.length ? "others" : "each"} ${limit("default")}`);
  return parts.length ? `by role: ${parts.join(", ")}` : "by role";
}

// codeReview sums up the review rules of the team's agents.
export function codeReview(agents: TeamMember[]) {
  const required = agents.filter((a) => a.review_required);
  if (!required.length) return "not required";
  const by = [...new Set(required.map((a) => a.reviewer).filter(Boolean))];
  return `${required.length === agents.length ? "required" : `required for ${required.length} of ${agents.length} agents`}${by.length ? `, by ${by.join(", ")}` : ""}`;
}

function subscription(s: TeamSettings) {
  const key = s.quota_keys.find((k) => k.name === s.quota_key);
  if (!s.provider) return "not set";
  return `${providerName(s.provider)} · ${key?.description || s.quota_key || "the runtime's default subscription"}`;
}

function useTeamEdit(team: string, base: number) {
  const qc = useQueryClient();
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const save = async (body: object, done: string) => {
    setError(null);
    setBusy(true);
    try {
      await core(`/catalog/teams/${encodeURIComponent(team)}`, { method: "PUT", body: JSON.stringify({ base, ...body }) });
      await qc.invalidateQueries({ queryKey: ["catalog"] });
      toast.success(done);
      return true;
    } catch (e) { setError(e); return false; } finally { setBusy(false); }
  };
  return { error, setError, busy, save };
}

// otherError is a refusal not about one of the dialog's own fields.
function otherError(e: unknown, fields: string[]) {
  if (!e) return null;
  if (e instanceof ApiError) return e.field && fields.includes(e.field) ? null : e.message;
  return String(e);
}

function Card({ icon: Icon, title, badge, children }: { icon: React.ElementType; title: string; badge: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col rounded-xl border bg-card p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="flex items-center gap-1.5 text-sm font-medium"><Icon className="size-4 text-muted-foreground" />{title}</span>
        <Badge variant="secondary" className="font-normal">{badge}</Badge>
      </div>
      {children}
    </div>
  );
}

function ChangeLead({ team, base, open, onClose }: { team: TeamView; base: number; open: boolean; onClose: () => void }) {
  const [to, setTo] = useState("");
  const { error, busy, save, setError } = useTeamEdit(team.name, base);
  const others = team.agents.filter((a) => a.name !== team.lead);
  const auditFollows = team.audit_assignee === team.lead;
  const submit = async () => { if (await save({ lead: to }, `${to} leads ${team.title || team.name}`)) onClose(); };
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) { onClose(); setError(null); setTo(""); } }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Change lead</DialogTitle>
          <DialogDescription>The lead gets your messages that name no one, plans the work and hands it to the team.</DialogDescription>
        </DialogHeader>
        <Field label="New lead" error={fieldError(error, "lead")}>
          <Select value={to} onValueChange={setTo}>
            <SelectTrigger className="w-full"><SelectValue placeholder="Pick an agent" /></SelectTrigger>
            <SelectContent>{others.map((a) => <SelectItem key={a.name} value={a.name}>{a.name}{a.description ? ` — ${a.description}` : ""}</SelectItem>)}</SelectContent>
          </Select>
        </Field>
        {to && (
          <ul className="list-disc space-y-1 pl-5 text-sm text-muted-foreground">
            <li>{to} reports to you and takes {team.lead}&apos;s place: the agents reporting to {team.lead} report to {to}.</li>
            <li>{team.lead} stays in the team and reports to {to}.</li>
            <li>{auditFollows ? `Onboarding a repository moves to ${to} with the lead.` : `Onboarding a repository stays with ${team.audit_assignee}.`}</li>
          </ul>
        )}
        {otherError(error, ["lead"]) && <p className="text-sm text-red-600 dark:text-red-400">{otherError(error, ["lead"])}</p>}
        <DialogFooter className="gap-2">
          <Button variant="outline" className="h-10" onClick={onClose}>Cancel</Button>
          <Button className="h-10" disabled={busy || !to} onClick={submit}>Make {to || "it"} the lead</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PickOnboarding({ team, base, open, onClose }: { team: TeamView; base: number; open: boolean; onClose: () => void }) {
  const [to, setTo] = useState(team.audit_assignee === team.lead ? "_" : team.audit_assignee);
  const { error, busy, save, setError } = useTeamEdit(team.name, base);
  const submit = async () => {
    const v = to === "_" ? "" : to;
    if (await save({ audit_assignee: v }, v ? `${v} onboards repositories` : "The lead onboards repositories")) onClose();
  };
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) { onClose(); setError(null); } }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Onboarding a repository</DialogTitle>
          <DialogDescription>When you add an existing repository to this team, this agent reads it first and writes what the team needs to know.</DialogDescription>
        </DialogHeader>
        <Field label="Agent" error={fieldError(error, "audit_assignee")}>
          <Select value={to} onValueChange={setTo}>
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="_">The lead ({team.lead})</SelectItem>
              {team.agents.filter((a) => a.name !== team.lead).map((a) => <SelectItem key={a.name} value={a.name}>{a.name}</SelectItem>)}
            </SelectContent>
          </Select>
        </Field>
        <p className="text-sm text-muted-foreground">&quot;The lead&quot; follows the lead when you change it.</p>
        {otherError(error, ["audit_assignee"]) && <p className="text-sm text-red-600 dark:text-red-400">{otherError(error, ["audit_assignee"])}</p>}
        <DialogFooter className="gap-2">
          <Button variant="outline" className="h-10" onClick={onClose}>Cancel</Button>
          <Button className="h-10" disabled={busy} onClick={submit}>Save</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

const settingsFields = ["provider", "model", "quota_key", "max_concurrent_runs", "timeout", "settings"];

function EditSettings({ team, base, open, onClose }: { team: TeamView; base: number; open: boolean; onClose: () => void }) {
  const s = team.settings!;
  const [provider, setProvider] = useState(s.provider ?? "");
  const [quota, setQuota] = useState(s.quota_key ?? "");
  const [model, setModel] = useState(s.model && s.model !== "default" ? s.model : "");
  const [runs, setRuns] = useState(s.max_concurrent_runs ? String(s.max_concurrent_runs) : "");
  const [timeout, setRunTime] = useState(s.timeout ?? "");
  const { error, busy, save, setError } = useTeamEdit(team.name, base);
  const keys = s.quota_keys.filter((k) => k.provider === provider);
  const runsError = runs.trim() && !/^[1-8]$/.test(runs.trim()) ? "A number from 1 to 8, or empty for the role defaults" : null;
  const submit = async () => {
    if (runsError) return;
    const settings = { provider, quota_key: keys.some((k) => k.name === quota) ? quota : "", model: model.trim(), max_concurrent_runs: Number(runs.trim() || 0), timeout: timeout.trim() };
    if (await save({ settings }, "Team settings saved")) onClose();
  };
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) { onClose(); setError(null); } }}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Team settings</DialogTitle>
          <DialogDescription>Every agent of {team.title || team.name} starts from these; an agent can override any of them on its own card.</DialogDescription>
        </DialogHeader>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Runtime" error={fieldError(error, "provider")}>
            <Select value={provider || "_"} onValueChange={(v) => setProvider(v === "_" ? "" : v)}>
              <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="_">Not set</SelectItem>
                <SelectItem value="claude">Claude</SelectItem>
                <SelectItem value="codex">Codex</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field label="Subscription" error={fieldError(error, "quota_key")}>
            <Select value={keys.some((k) => k.name === quota) ? quota : "_"} onValueChange={(v) => setQuota(v === "_" ? "" : v)}>
              <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="_">The runtime&apos;s default</SelectItem>
                {keys.map((k) => <SelectItem key={k.name} value={k.name}>{k.description || k.name}</SelectItem>)}
              </SelectContent>
            </Select>
          </Field>
          <Field label="Model" error={fieldError(error, "model")} hint="Empty: the runtime's default model">
            <Input value={model} onChange={(e) => setModel(e.target.value)} placeholder="default" />
          </Field>
          <Field label="Tasks at once" error={runsError ?? fieldError(error, "max_concurrent_runs")} hint="Per agent; empty: by role (company concurrency)">
            <Input inputMode="numeric" value={runs} onChange={(e) => setRuns(e.target.value)} placeholder="by role" />
          </Field>
          <Field label="Run time limit" error={fieldError(error, "timeout")} hint="How long one run may take: 30m, 2h (at most 24h)">
            <Input value={timeout} onChange={(e) => setRunTime(e.target.value)} placeholder="30m" />
          </Field>
        </div>
        <p className="text-sm text-muted-foreground">Code review ({codeReview(team.agents)}) comes from the team&apos;s review rules; change those in the YAML editor.</p>
        {otherError(error, settingsFields) && <p className="text-sm text-red-600 dark:text-red-400">{otherError(error, settingsFields)}</p>}
        {fieldError(error, "settings") && <p className="text-sm text-red-600 dark:text-red-400">{fieldError(error, "settings")}</p>}
        <DialogFooter className="gap-2">
          <Button variant="outline" className="h-10" onClick={onClose}>Cancel</Button>
          <Button className="h-10" disabled={busy || !!runsError} onClick={submit}>Save</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// HowTeamRuns: the three things that decide how work flows; only the lead is required.
export function HowTeamRuns({ team, base, manage, owner }: { team: TeamView; base: number; manage: boolean; owner: boolean }) {
  const [dialog, setDialog] = useState<"" | "lead" | "onboarding" | "settings">("");
  const s = team.settings;
  const overrides = s?.overrides ?? [];
  return (
    <section className="mb-8">
      <h2 className="text-base font-semibold">How this team runs</h2>
      <p className="mb-3 text-sm text-muted-foreground">Three things decide how work flows. Only the lead is required.</p>
      <div className="grid gap-3 lg:grid-cols-3">
        <Card icon={Crown} title="Lead" badge="Required · one">
          <p className="mt-2 text-lg font-semibold">{team.lead}</p>
          <p className="mt-1 text-sm text-muted-foreground">Gets your messages that name no one, plans the work, hands it to the team and reports back to you. Questions it can&apos;t decide come to your Inbox.</p>
          <LeadAuthority lead={team.lead} team={team.name} owner={owner} />
          {manage && <Button variant="outline" className="mt-3 h-10 self-start" onClick={() => setDialog("lead")}>Change lead</Button>}
        </Card>
        <Card icon={ClipboardCheck} title="Onboarding a repository" badge="Optional">
          <p className="mt-2 text-lg font-semibold">
            {team.audit_assignee === team.lead ? <>The lead <span className="text-sm font-normal text-muted-foreground">({team.lead})</span></> : team.audit_assignee}
          </p>
          <p className="mt-1 text-sm text-muted-foreground">When you add an existing repository to this team, one agent reads it first and writes what the team needs to know. Nothing happens here until you add a repository.</p>
          {manage && <Button variant="outline" className="mt-auto h-10 self-start" onClick={() => setDialog("onboarding")}>Pick another agent</Button>}
        </Card>
        <Card icon={SlidersHorizontal} title="Team settings" badge="Every agent starts here">
          {s && (
            <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">
              <dt className="text-muted-foreground">Subscription</dt><dd>{subscription(s)}</dd>
              <dt className="text-muted-foreground">Model</dt><dd>{!s.model || s.model === "default" ? "Default" : s.model}</dd>
              <dt className="text-muted-foreground">Tasks at once</dt><dd>{tasksAtOnce(s, team.agents)}</dd>
              <dt className="text-muted-foreground">Run time limit</dt><dd>{duration(s.timeout)}</dd>
              <dt className="text-muted-foreground">Code review</dt><dd>{codeReview(team.agents)}</dd>
            </dl>
          )}
          <p className="mt-2 text-sm text-muted-foreground" title={overrides.join(", ")}>
            An agent can override any of these on its own card. {overrides.length === 0 ? "None does." : overrides.length === 1 ? `1 agent does (${overrides[0]}).` : `${overrides.length} agents do (${overrides.join(", ")}).`}
          </p>
          {manage && s && <Button variant="outline" className="mt-3 h-10 self-start" onClick={() => setDialog("settings")}>Edit team settings</Button>}
        </Card>
      </div>
      {manage && dialog === "lead" && <ChangeLead team={team} base={base} open onClose={() => setDialog("")} />}
      {manage && dialog === "onboarding" && <PickOnboarding team={team} base={base} open onClose={() => setDialog("")} />}
      {manage && dialog === "settings" && s && <EditSettings team={team} base={base} open onClose={() => setDialog("")} />}
    </section>
  );
}

// mayMessage says whom an agent may message: always its team, you when it may bring questions to people (owner in
// can_message, people-topics D17), plus agents of other teams it lists.
function mayMessage(m: TeamMember, team: TeamView) {
  const own = new Set(team.agents.map((a) => a.name));
  const others = (m.can_message ?? []).filter((n) => n !== "owner" && !own.has(n));
  return [team.agents.length > 1 ? "its team" : "", (m.can_message ?? []).includes("owner") ? "you" : "", ...others].filter(Boolean).join(", ") || "its manager";
}

function givesTasksTo(m: TeamMember) {
  const to = m.assigns_to ?? [];
  switch (m.assign_scope) {
    case "none": return "no one";
    case "direct_reports": return to.length ? `its reports (${to.length})` : "no one yet (no reports)";
    case "subtree": return to.length ? `everyone below it (${to.length})` : "no one yet (no reports)";
  }
  return to.length ? to.join(", ") : "no one";
}

function now(r?: AgentRuns) {
  if (!r) return "idle";
  const parts = [
    r.work_live ? `${r.work_live} task${r.work_live === 1 ? "" : "s"} running` : "",
    r.conversation_live ? "chatting" : "",
    r.work_queued ? `${r.work_queued} queued` : "",
  ].filter(Boolean);
  return parts.length ? parts.join(", ") : "idle";
}

// WhoDoesWhat: each agent's job, manager, reviewer, whom it may message and give tasks to (Core enforces the last
// two), and what it is doing now.
export function WhoDoesWhat({ team, runs, onEdit }: { team: TeamView; runs?: Record<string, AgentRuns> | null; onEdit?: (name: string) => void }) {
  return (
    <section className="mb-8">
      <h2 className="text-base font-semibold">Who does what</h2>
      <p className="mb-3 text-sm text-muted-foreground">Who reports to whom, who checks whose work, and who may message or give tasks to whom. Core enforces the last two; change them on an agent&apos;s card.</p>
      <div className="rounded-xl border bg-card">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="pl-4">Agent</TableHead>
              <TableHead>Does</TableHead>
              <TableHead>Reports to</TableHead>
              <TableHead>Work checked by</TableHead>
              <TableHead>May message</TableHead>
              <TableHead>Gives tasks to</TableHead>
              <TableHead className="pr-4">Now</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {team.agents.map((m) => (
              <TableRow key={m.name}>
                <TableCell className="pl-4 font-medium">
                  {onEdit ? <button type="button" className="underline-offset-4 hover:underline" onClick={() => onEdit(m.name)}>{m.name}</button> : m.name}
                  {m.name === team.lead && <Badge variant="outline" className="ml-2 font-normal">lead</Badge>}
                </TableCell>
                <TableCell className="max-w-64 whitespace-normal text-muted-foreground">{m.description || m.role}</TableCell>
                <TableCell>{you(m.reports_to)}</TableCell>
                <TableCell>{m.review_required ? m.reviewer || "required, no reviewer" : "no review"}</TableCell>
                <TableCell className="max-w-56 whitespace-normal">{mayMessage(m, team)}</TableCell>
                <TableCell className="max-w-56 whitespace-normal" title={(m.assigns_to ?? []).join(", ")}>{givesTasksTo(m)}</TableCell>
                <TableCell className="pr-4 text-muted-foreground">{now(runs?.[m.name])}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </section>
  );
}

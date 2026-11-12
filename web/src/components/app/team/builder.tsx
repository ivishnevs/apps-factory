"use client";

import { AlertTriangle, ArrowRight, Bot, KeyRound, Plus, Server, Sparkles, Trash2, Users } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { AgentSecrets } from "@/components/app/team/agent-secrets";
import Link from "@/components/app/link";
import { ErrorState, LoadingRows } from "@/components/app/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, core, useAgentDoc, useAgentPresets, useCatalog, useSetup, useTeamPresets } from "@/lib/api/client";
import type { TeamView } from "@/lib/api/types-catalog";
import type { AgentDoc, AgentOverrides, AgentPreset, AgentSpec, Setup } from "@/lib/api/types-presets";
import { runsError, windowError } from "@/lib/run-kinds";
import { cn } from "@/lib/utils";
import { useQueryClient } from "@tanstack/react-query";

// The team builder's pieces (team-builder 5.x, saas-web 3.x): every write goes to Core's team builder routes on the
// catalog version the page read; a newer version (409) asks to reload; a refusal names its field.

const fieldError = (e: unknown, field: string) => (e instanceof ApiError && e.field === field ? e.message : null);
const formError = (e: unknown) => (e instanceof ApiError && !e.field ? e.message : e && !(e instanceof ApiError) ? String(e) : null);

function Field({ label, error, children, hint }: { label: string; error?: string | null; children: React.ReactNode; hint?: string }) {
  return (
    <label className="block space-y-1.5 text-sm">
      <span className="font-medium">{label}</span>
      {children}
      {hint && !error && <span className="block text-xs text-muted-foreground">{hint}</span>}
      {error && <span className="block text-xs text-red-600 dark:text-red-400">{error}</span>}
    </label>
  );
}

function useRefresh() {
  const qc = useQueryClient();
  return () => Promise.all([qc.invalidateQueries({ queryKey: ["catalog"] }), qc.invalidateQueries({ queryKey: ["company-info"] })]);
}

// SetupBanner names what keeps work from running (GET /setup); nothing is refused because of it.
export function SetupBanner({ setup, compact }: { setup?: Setup; compact?: boolean }) {
  if (!setup || setup.ready) return null;
  const items: React.ReactNode[] = [];
  for (const s of setup.missing_secrets) {
    items.push(<li key={`s-${s.name}`} className="flex items-center gap-2"><KeyRound className="size-4 shrink-0" />Secret <span className="font-mono">{s.name}</span>{s.wrong_kind ? " has the wrong kind" : " is not set"} — {s.agents.join(", ")} wait for it.<Link href="/secrets" className="ml-auto shrink-0 underline">Set</Link></li>);
  }
  if (setup.no_node) items.push(<li key="node" className="flex items-center gap-2"><Server className="size-4 shrink-0" />No machine is connected: runs stay queued.<Link href="/nodes" className="ml-auto shrink-0 underline">Connect</Link></li>);
  for (const t of setup.teams_without_projects) items.push(<li key={`t-${t}`} className="flex items-center gap-2"><Users className="size-4 shrink-0" />Team {t} has no project yet.<Link href="/setup" className="ml-auto shrink-0 underline">Create</Link></li>);
  return (
    <section className={cn("mb-5 rounded-2xl border border-amber-500/40 bg-amber-500/5 p-4 text-sm", compact && "p-3")}>
      <p className="mb-2 flex items-center gap-2 font-medium"><AlertTriangle className="size-4 text-amber-600" />Finish setting up: before work can run<Link href="/setup" className="ml-auto text-xs font-normal underline">Setup steps</Link></p>
      <ul className="space-y-1.5">{items}</ul>
    </section>
  );
}

export function useSetupBanner() {
  return useSetup().data;
}

// NewTeamSheet: a team from the preset gallery (its agents are named <team>-<key>).
export function NewTeamSheet({ open, onClose, initialPreset }: { open: boolean; onClose: () => void; initialPreset?: string }) {
  const presets = useTeamPresets();
  const catalog = useCatalog();
  const refresh = useRefresh();
  const [preset, setPreset] = useState(initialPreset ?? "");
  useEffect(() => { if (initialPreset) setPreset(initialPreset); }, [initialPreset]);
  const [team, setTeam] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const chosen = presets.data?.find((p) => p.metadata.name === preset);
  useEffect(() => { if (chosen && !team) setTeam(chosen.spec.team); }, [chosen, team]);
  const submit = async () => {
    setError(null);
    setBusy(true);
    try {
      await core("/catalog/teams", { method: "POST", body: JSON.stringify({ base: catalog.data?.version ?? 0, from_preset: preset, team: team.trim() }) });
      toast.success(`Team ${team} created`);
      await refresh();
      onClose();
    } catch (e) { setError(e); } finally { setBusy(false); }
  };
  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()}>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-2xl">
        <SheetHeader><SheetTitle>New team</SheetTitle><SheetDescription>A copy of a preset: its agents, prompts, profile and policies become your team&apos;s documents.</SheetDescription></SheetHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-4">
          {presets.isLoading ? <LoadingRows /> : presets.error ? <ErrorState error={presets.error} /> : (
            <ul className="grid gap-2 sm:grid-cols-2">
              {presets.data!.map((p) => (
                <li key={p.metadata.name}>
                  <button type="button" onClick={() => { setPreset(p.metadata.name); setTeam(p.spec.team); }}
                    className={cn("flex h-full w-full flex-col gap-1.5 rounded-xl border p-3 text-left text-sm transition-colors hover:bg-muted/40", preset === p.metadata.name && "border-foreground/70 bg-accent/50")}>
                    <span className="font-medium">{p.metadata.title}</span>
                    <span className="text-xs text-muted-foreground">{p.metadata.description}</span>
                    <span className="flex flex-wrap gap-1">{p.spec.agents.map((a) => <Badge key={a.key} variant="secondary" className="font-normal">{a.key}</Badge>)}</span>
                    {(p.secrets ?? []).length > 0 && <span className="text-xs text-amber-700 dark:text-amber-400">Needs secrets: {(p.secrets ?? []).map((s) => s.name).join(", ")}</span>}
                  </button>
                </li>
              ))}
            </ul>
          )}
          <Field label="Team name" error={fieldError(error, "team")} hint="Lowercase letters, digits and '-'; agents are named <team>-<role>.">
            <Input value={team} onChange={(e) => setTeam(e.target.value)} placeholder="product" />
          </Field>
          {formError(error) && <ErrorState error={formError(error)} />}
          <Button className="h-10 self-start" disabled={!preset || !team.trim() || busy} onClick={submit}><Plus className="size-4" />Create team</Button>
        </div>
      </SheetContent>
    </Sheet>
  );
}

const providers = [{ v: "", l: "Team default" }, { v: "claude", l: "Claude" }, { v: "codex", l: "Codex" }];

function OverridesFields({ value, onChange, team, error, presetPrompt }: { value: AgentOverrides; onChange: (v: AgentOverrides) => void; team: TeamView; error: unknown; presetPrompt?: string }) {
  const managers = ["owner", ...team.agents.map((a) => a.name)];
  return (
    <>
      <Field label="Description" error={fieldError(error, "description")}><Input value={value.description ?? ""} onChange={(e) => onChange({ ...value, description: e.target.value })} /></Field>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Reports to" error={fieldError(error, "reports_to")}>
          <Select value={value.reports_to || team.lead} onValueChange={(v) => onChange({ ...value, reports_to: v })}>
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>{managers.map((m) => <SelectItem key={m} value={m}>{m}</SelectItem>)}</SelectContent>
          </Select>
        </Field>
        <Field label="May message" error={fieldError(error, "can_message")} hint="Comma separated agents (or owner)">
          <Input value={(value.can_message ?? []).join(", ")} onChange={(e) => onChange({ ...value, can_message: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) })} />
        </Field>
      </div>
      <div className="grid gap-3 sm:grid-cols-3">
        <Field label="Runtime" error={fieldError(error, "provider")}>
          <Select value={value.provider ?? ""} onValueChange={(v) => onChange({ ...value, provider: v === "_" ? "" : v })}>
            <SelectTrigger className="w-full"><SelectValue placeholder="Team default" /></SelectTrigger>
            <SelectContent>{providers.map((p) => <SelectItem key={p.l} value={p.v || "_"}>{p.l}</SelectItem>)}</SelectContent>
          </Select>
        </Field>
        <Field label="Model" error={fieldError(error, "model")}><Input value={value.model ?? ""} onChange={(e) => onChange({ ...value, model: e.target.value })} placeholder="default" /></Field>
        <Field label="Quota key" error={fieldError(error, "quota_key")}><Input value={value.quota_key ?? ""} onChange={(e) => onChange({ ...value, quota_key: e.target.value })} placeholder="claude-subscription" /></Field>
      </div>
      <Field label="Prompt" error={fieldError(error, "prompt")} hint="Markdown; empty keeps the preset's prompt">
        <Textarea value={value.prompt ?? ""} onChange={(e) => onChange({ ...value, prompt: e.target.value })} placeholder={presetPrompt?.slice(0, 400)} className="min-h-40 font-mono text-xs" spellCheck={false} />
      </Field>
    </>
  );
}

// AddAgentSheet: an agent from an agent preset (with overrides) or a custom one, in team.
export function AddAgentSheet({ team, open, onClose, initialPreset }: { team: TeamView; open: boolean; onClose: () => void; initialPreset?: string }) {
  const presets = useAgentPresets();
  const catalog = useCatalog();
  const refresh = useRefresh();
  const [preset, setPreset] = useState<AgentPreset | null>(null);
  useEffect(() => {
    const p = initialPreset ? presets.data?.find((x) => x.metadata.name === initialPreset) : undefined;
    if (p) { setPreset(p); setName((n) => n || p.metadata.name); }
  }, [initialPreset, presets.data]);
  const [name, setName] = useState("");
  const [ov, setOv] = useState<AgentOverrides>({});
  const [role, setRole] = useState("developer");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [mode, setMode] = useState("preset");
  const [runs, setRuns] = useState<RunKindInputs>(runKindInputs());
  const submit = async () => {
    setError(null);
    if (Object.values(runKindErrors(runs)).some(Boolean)) return; // shown next to the fields
    setBusy(true);
    const base = catalog.data?.version ?? 0;
    // work-and-conversation-runs: the limits and windows the form sets, written the same way for both paths.
    const kinds: AgentSpec = {};
    applyRunKinds(kinds, runs);
    try {
      if (mode === "preset") {
        const overrides: AgentOverrides = { ...ov, context_window: kinds.runtime?.context_window, max_concurrent_runs: kinds.execution?.max_concurrent_runs, conversation_max_runs: kinds.execution?.conversation?.max_concurrent_runs };
        await core("/catalog/agents", { method: "POST", body: JSON.stringify({ base, team: team.name, from_preset: preset?.metadata.name, name: name.trim(), overrides }) });
      } else {
        const agent = {
          metadata: { name: name.trim(), description: ov.description ?? "" },
          spec: { team: team.name, role, reports_to: ov.reports_to || team.lead, communication: { can_message: ov.can_message?.length ? ov.can_message : [ov.reports_to || team.lead] },
            ...(ov.provider || kinds.runtime?.context_window ? { runtime: { ...(ov.provider ? { provider: ov.provider, model: ov.model || "default" } : {}), ...(ov.quota_key ? { quota_key: ov.quota_key } : {}), ...kinds.runtime } } : {}),
            ...(Object.keys(kinds.execution ?? {}).length ? { execution: kinds.execution } : {}),
            ...(team.profile ? { extends: team.profile } : {}) },
        };
        await core("/catalog/agents", { method: "POST", body: JSON.stringify({ base, team: team.name, name: name.trim(), agent, prompt: ov.prompt || `You are ${name.trim()}.` }) });
      }
      toast.success("Agent added");
      await refresh();
      onClose();
    } catch (e) { setError(e); } finally { setBusy(false); }
  };
  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()}>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-2xl">
        <SheetHeader><SheetTitle>Add an agent to {team.name}</SheetTitle><SheetDescription>From a preset (a copy you can change) or your own.</SheetDescription></SheetHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-4">
          <Tabs value={mode} onValueChange={setMode}>
            <TabsList><TabsTrigger value="preset"><Sparkles className="size-4" />Preset</TabsTrigger><TabsTrigger value="custom"><Bot className="size-4" />Custom</TabsTrigger></TabsList>
            <TabsContent value="preset" className="mt-3">
              {presets.isLoading ? <LoadingRows /> : presets.error ? <ErrorState error={presets.error} /> : (
                <ul className="grid gap-2 sm:grid-cols-2">
                  {presets.data!.map((p) => (
                    <li key={p.metadata.name}>
                      <button type="button" onClick={() => { setPreset(p); if (!name) setName(`${team.name}-${p.metadata.name}`); }}
                        className={cn("flex h-full w-full flex-col gap-1 rounded-xl border p-3 text-left text-sm hover:bg-muted/40", preset?.metadata.name === p.metadata.name && "border-foreground/70 bg-accent/50")}>
                        <span className="font-medium">{p.metadata.title} <span className="text-xs font-normal text-muted-foreground">{p.spec.role}</span></span>
                        <span className="text-xs text-muted-foreground">{p.metadata.description}</span>
                        {(p.spec.skills ?? []).length > 0 && <span className="text-xs text-muted-foreground">Skills: {(p.spec.skills ?? []).join(", ")}</span>}
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </TabsContent>
            <TabsContent value="custom" className="mt-3">
              <Field label="Role" error={fieldError(error, "role")}>
                <Select value={role} onValueChange={setRole}>
                  <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent>{["developer", "manager", "reviewer", "designer", "qa", "researcher"].map((r) => <SelectItem key={r} value={r}>{r}</SelectItem>)}</SelectContent>
                </Select>
              </Field>
            </TabsContent>
          </Tabs>
          <Field label="Name" error={fieldError(error, "name")} hint="Unique in the company"><Input value={name} onChange={(e) => setName(e.target.value)} /></Field>
          <OverridesFields value={ov} onChange={setOv} team={team} error={error} presetPrompt={preset?.prompt} />
          <RunKindFields value={runs} onChange={setRuns} error={error} />
          {formError(error) && <ErrorState error={formError(error)} />}
          <Button className="h-10 self-start" disabled={busy || !name.trim() || mode === "preset" && !preset} onClick={submit}><Plus className="size-4" />Add agent</Button>
        </div>
      </SheetContent>
    </Sheet>
  );
}

// RunKindFields (work-and-conversation-runs): the work and conversation limits and the context window per run kind.
// Empty inherits the profile (work limit), means no cap (conversation limit) or the runtime's default (window).
export interface RunKindInputs { work: string; conversation: string; windowWork: string; windowConversation: string; split: boolean }

export function runKindInputs(spec?: AgentSpec): RunKindInputs {
  const w = spec?.runtime?.context_window ?? {};
  const work = spec?.execution?.max_concurrent_runs, conv = spec?.execution?.conversation?.max_concurrent_runs;
  return { work: work ? String(work) : "", conversation: conv ? String(conv) : "", windowWork: w.work ?? "", windowConversation: w.conversation ?? "", split: (w.work ?? "") !== (w.conversation ?? "") };
}

export function runKindErrors(v: RunKindInputs): Record<string, string | null> {
  return {
    max_concurrent_runs: runsError(v.work, 1),
    conversation_max_runs: runsError(v.conversation, 1),
    window_work: windowError(v.windowWork),
    window_conversation: v.split ? windowError(v.windowConversation) : null,
  };
}

// applyRunKinds writes valid inputs into the agent spec (dropping what is empty).
export function applyRunKinds(spec: AgentSpec, v: RunKindInputs) {
  const execution = { ...(spec.execution ?? {}) };
  if (v.work.trim()) execution.max_concurrent_runs = Number(v.work); else delete execution.max_concurrent_runs;
  const conversation = { ...(execution.conversation ?? {}) };
  if (v.conversation.trim()) conversation.max_concurrent_runs = Number(v.conversation); else delete conversation.max_concurrent_runs;
  if (Object.keys(conversation).length) execution.conversation = conversation; else delete execution.conversation;
  spec.execution = execution;
  const work = v.windowWork.trim(), conv = (v.split ? v.windowConversation : v.windowWork).trim();
  const runtime = { ...(spec.runtime ?? {}) };
  if (work || conv) runtime.context_window = { ...(work ? { work } : {}), ...(conv ? { conversation: conv } : {}) }; else delete runtime.context_window;
  spec.runtime = runtime;
}

export function RunKindFields({ value, onChange, error }: { value: RunKindInputs; onChange: (v: RunKindInputs) => void; error: unknown }) {
  const errs = runKindErrors(value);
  const err = (k: string, server?: string) => errs[k] ?? fieldError(error, server ?? k);
  return (
    <section className="space-y-3 rounded-xl border p-3">
      <p className="text-sm font-medium">Runs</p>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Work runs at a time" error={err("max_concurrent_runs")} hint="Tasks it works on in parallel; empty keeps its profile's">
          <Input inputMode="numeric" value={value.work} placeholder="profile" onChange={(e) => onChange({ ...value, work: e.target.value })} />
        </Field>
        <Field label="Conversation runs at a time" error={err("conversation_max_runs")} hint="Across projects; empty: no cap (one per project). Conversations never wait for work.">
          <Input inputMode="numeric" value={value.conversation} placeholder="no cap" onChange={(e) => onChange({ ...value, conversation: e.target.value })} />
        </Field>
      </div>
      <div className={cn("grid gap-3", value.split && "sm:grid-cols-2")}>
        <Field label={value.split ? "Context window: work" : "Context window"} error={err("window_work", "context_window")} hint="Where the session compacts: 200k, 1M or max; empty keeps the runtime default">
          <Input value={value.windowWork} placeholder="runtime default" onChange={(e) => onChange({ ...value, windowWork: e.target.value })} />
        </Field>
        {value.split && (
          <Field label="Context window: conversation" error={err("window_conversation", "context_window")} hint="Conversations are long-lived: 200k keeps them fast">
            <Input value={value.windowConversation} placeholder="runtime default" onChange={(e) => onChange({ ...value, windowConversation: e.target.value })} />
          </Field>
        )}
      </div>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" className="size-4" checked={value.split} onChange={(e) => onChange({ ...value, split: e.target.checked, windowConversation: e.target.checked ? value.windowConversation || value.windowWork : value.windowConversation })} />
        Separate windows for work and conversations
      </label>
    </section>
  );
}

// AgentFormSheet edits an agent as fields (and its prompt), shows its preset's update and deletes it.
export function AgentFormSheet({ name, team, onClose }: { name: string | null; team: TeamView; onClose: () => void }) {
  const doc = useAgentDoc(name);
  const refresh = useRefresh();
  const confirm = useConfirm();
  const [draft, setDraft] = useState<AgentDoc | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [runs, setRuns] = useState<RunKindInputs>(runKindInputs());
  useEffect(() => { setDraft(doc.data ?? null); setRuns(runKindInputs(doc.data?.agent.spec)); setError(null); }, [doc.data]);
  if (!name) return null;
  const set = (f: (d: AgentDoc) => void) => setDraft((d) => { if (!d) return d; const c = structuredClone(d); f(c); return c; });
  const save = async () => {
    if (!draft) return;
    setError(null);
    // agent-capabilities: required, so leads know what the agent does, needs, produces and who reviews it.
    if (!(draft.agent.spec.capabilities ?? []).some((c) => c.trim())) {
      setError(new Error("Capabilities are required: what it does, what it needs, what it produces and who reviews it"));
      return;
    }
    if (Object.values(runKindErrors(runs)).some(Boolean)) return; // shown next to the fields
    setBusy(true);
    try {
      const agent = { ...draft.agent, spec: { ...draft.agent.spec, capabilities: (draft.agent.spec.capabilities ?? []).map((c) => c.trim()).filter(Boolean) } };
      applyRunKinds(agent.spec, runs);
      await core(`/catalog/agents/${encodeURIComponent(name)}`, { method: "PUT", body: JSON.stringify({ base: draft.base, agent, prompt: draft.prompt }) });
      toast.success(`${name} saved`);
      await refresh();
      onClose();
    } catch (e) { setError(e); } finally { setBusy(false); }
  };
  const remove = async () => {
    if (!draft || !(await confirm({ title: `Delete ${name}?`, description: "Its document and prompt go in a new catalog version; the history keeps them.", confirm: "Delete", destructive: true }))) return;
    try {
      await core(`/catalog/agents/${encodeURIComponent(name)}?base=${draft.base}`, { method: "DELETE" });
      toast.success(`${name} deleted`);
      await refresh();
      onClose();
    } catch (e) { setError(e); }
  };
  const spec = draft?.agent.spec;
  const managers = ["owner", ...team.agents.map((a) => a.name).filter((a) => a !== name)];
  return (
    <Sheet open={!!name} onOpenChange={(o) => !o && onClose()}>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-2xl">
        <SheetHeader><SheetTitle>{name}</SheetTitle><SheetDescription>{draft?.agent.metadata.preset ? `From preset ${draft.agent.metadata.preset}` : "A custom agent"} · saved on top of version {draft?.base}</SheetDescription></SheetHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-4">
          {doc.isLoading && <LoadingRows />}
          {doc.error && <ErrorState error={doc.error} />}
          {draft?.update && (
            <section className="rounded-xl border border-sky-500/40 bg-sky-500/5 p-3 text-sm">
              <p className="font-medium">Preset {draft.update.preset} has a newer version ({draft.update.from.slice(0, 7)} → {draft.update.to.slice(0, 7)})</p>
              {draft.update.changes?.length ? <pre className="mt-2 max-h-40 overflow-auto whitespace-pre-wrap font-mono text-xs">{draft.update.changes.join("\n")}</pre> : null}
              <Button variant="outline" className="mt-2 h-9" onClick={() => set((d) => { d.prompt = d.update!.prompt; d.agent.metadata.preset = `${d.update!.preset}@${d.update!.to}`; d.update = undefined; })}>
                <ArrowRight className="size-4" />Take the new prompt (save to apply)
              </Button>
            </section>
          )}
          {draft && spec && (
            <>
              <Field label="Description" error={fieldError(error, "description")}><Input value={draft.agent.metadata.description} onChange={(e) => set((d) => { d.agent.metadata.description = e.target.value; })} /></Field>
              <Field label="Capabilities" error={fieldError(error, "capabilities")}>
                <Textarea value={(spec.capabilities ?? []).join("\n")} rows={4} className="text-base md:text-sm"
                  placeholder={"Does: implements specified tasks with tests\nNeeds: a task with acceptance criteria\nProduces: pull requests with passing checks\nReviewed by: QA"}
                  onChange={(e) => set((d) => { d.agent.spec.capabilities = e.target.value.split("\n").map((l) => l.trimEnd()).filter((l, i, a) => l.trim() || i < a.length - 1); })} />
                <span className="block text-xs text-muted-foreground">One line each: what it does, what it needs (and from whom), what it produces, who reviews it. Leads plan with it.</span>
              </Field>
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label="Reports to" error={fieldError(error, "reports_to")}>
                  <Select value={spec.reports_to ?? ""} onValueChange={(v) => set((d) => { d.agent.spec.reports_to = v; })}>
                    <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                    <SelectContent>{managers.map((m) => <SelectItem key={m} value={m}>{m}</SelectItem>)}</SelectContent>
                  </Select>
                </Field>
                <Field label="May message" error={fieldError(error, "can_message")}>
                  <Input value={(spec.communication?.can_message ?? []).join(", ")} onChange={(e) => set((d) => { d.agent.spec.communication = { ...d.agent.spec.communication, can_message: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) }; })} />
                </Field>
              </div>
              <div className="grid gap-3 sm:grid-cols-3">
                <Field label="Runtime" error={fieldError(error, "provider")}>
                  <Select value={spec.runtime?.provider || "_"} onValueChange={(v) => set((d) => { d.agent.spec.runtime = { ...d.agent.spec.runtime, provider: v === "_" ? undefined : v }; })}>
                    <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
                    <SelectContent>{providers.map((p) => <SelectItem key={p.l} value={p.v || "_"}>{p.l}</SelectItem>)}</SelectContent>
                  </Select>
                </Field>
                <Field label="Model" error={fieldError(error, "model")}><Input value={spec.runtime?.model ?? ""} placeholder="default" onChange={(e) => set((d) => { d.agent.spec.runtime = { ...d.agent.spec.runtime, model: e.target.value }; })} /></Field>
                <Field label="Quota key" error={fieldError(error, "quota_key")}><Input value={spec.runtime?.quota_key ?? ""} onChange={(e) => set((d) => { d.agent.spec.runtime = { ...d.agent.spec.runtime, quota_key: e.target.value }; })} /></Field>
              </div>
              <RunKindFields value={runs} onChange={setRuns} error={error} />
              <Field label="Prompt" error={fieldError(error, "prompt")}>
                <Textarea value={draft.prompt} onChange={(e) => set((d) => { d.prompt = e.target.value; })} className="min-h-56 font-mono text-xs" spellCheck={false} />
              </Field>
              <AgentSecrets name={name} onSaved={(version, secrets) => set((d) => { d.base = version; d.agent.spec.secrets = secrets.length ? secrets : undefined; })} />
              <p className="text-xs text-muted-foreground">Skills of this document: {(spec.skills?.include ?? []).join(", ") || "none (its profile may add some)"} — change them with Skills on the agent&apos;s card. Anything else: the document in the YAML editor.</p>
              {formError(error) && <ErrorState error={formError(error)} />}
              <div className="flex flex-wrap gap-2">
                <Button className="h-10" disabled={busy} onClick={save}>Save</Button>
                <Button variant="ghost" className="h-10 text-red-600 hover:text-red-700 dark:text-red-400" onClick={remove}><Trash2 className="size-4" />Delete</Button>
              </div>
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

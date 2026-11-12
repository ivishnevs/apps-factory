"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { FileKey2, KeyRound, Plus, Trash2, Upload, UserPlus } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { ApiError, core, useCompanyInfo, useTeams } from "@/lib/api/client";
import { agentsOf, envError, kindLabel, type Secret, useSecrets } from "@/lib/api/secrets";
import { canManage } from "@/lib/api/types-account";
import { ago } from "@/lib/format";

// Secrets of the company (core-secret-store 1.3): API keys and files the company's agents and MCP servers use.
// Core keeps them encrypted and never shows a value again; a node receives a value only for a run that needs it.
// Owners and admins add, replace and delete; members see the names only. agent-secrets-ui 2.1: each secret shows what
// uses it and can be given to agents (one catalog change); a new secret can name its agents right away; deleting a used
// secret names its agents first.
// A secret an agent's runs need (GET /secrets/needed): runs of those agents wait until it is set with its kind.
interface Needed { name: string; kind: "string" | "file"; set: boolean; wrong_kind?: boolean; agents: string[] }

const namePattern = /^[A-Za-z_][A-Za-z0-9_]{0,127}$/;
const maxFile = 64 << 10;

// AgentPicker lists the company's agents to give a secret to (those that already have it are left out).
function AgentPicker({ value, onChange, except }: { value: string[]; onChange: (v: string[]) => void; except: string[] }) {
  const teams = useTeams();
  const agents = (teams.data?.teams ?? []).flatMap((t) => t.agents.map((a) => ({ name: a.name, team: t.name }))).filter((a) => !except.includes(a.name));
  if (teams.isLoading) return <p className="text-xs text-muted-foreground">Loading agents…</p>;
  if (agents.length === 0) return <p className="text-xs text-muted-foreground">No other agents.</p>;
  return (
    <ul className="max-h-60 divide-y overflow-y-auto rounded-lg border">
      {agents.map((a) => (
        <li key={a.name}>
          <label className="flex min-h-10 cursor-pointer items-center gap-2 px-3 py-2 text-sm">
            <input type="checkbox" className="size-4" checked={value.includes(a.name)} onChange={(e) => onChange(e.target.checked ? [...value, a.name] : value.filter((x) => x !== a.name))} />
            <span className="font-medium">{a.name}</span><span className="text-xs text-muted-foreground">{a.team}</span>
          </label>
        </li>
      ))}
    </ul>
  );
}

// grant gives secret to agents in variable env (default: its name) as one catalog change on the version teams read.
async function grant(secret: string, agents: string[], env: string, base: number) {
  await core(`/secrets/${encodeURIComponent(secret)}/agents`, { method: "POST", body: JSON.stringify({ base, agents, env: env || undefined }) });
}

function GiveSecret({ secret, onClose }: { secret: Secret; onClose: () => void }) {
  const qc = useQueryClient();
  const teams = useTeams();
  const [agents, setAgents] = useState<string[]>([]);
  const [env, setEnv] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const variable = env.trim() || secret.name;
  const varError = envError(variable, []);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!agents.length || varError || !teams.data) return;
    setBusy(true);
    setError("");
    try {
      await grant(secret.name, agents, env.trim(), teams.data.version);
      toast.success(`${secret.name} given to ${agents.join(", ")}`);
      await Promise.all([qc.invalidateQueries({ queryKey: ["secrets"] }), qc.invalidateQueries({ queryKey: ["catalog"] }), qc.invalidateQueries({ queryKey: ["agent-secrets"] })]);
      onClose();
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) await qc.invalidateQueries({ queryKey: ["catalog", "teams"] });
      setError(err instanceof ApiError && err.status === 409 ? "The catalog changed meanwhile; it is reloaded: try again." : err instanceof Error ? err.message : String(err));
    } finally { setBusy(false); }
  };
  return (
    <Sheet open onOpenChange={(o) => !o && onClose()}>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-md">
        <SheetHeader className="border-b px-5 py-4">
          <SheetTitle>Give {secret.name} to agents</SheetTitle>
          <SheetDescription>{secret.kind === "file" ? "Their runs get the file; the variable holds its path." : "Their runs get the value in the variable."} Saved as a catalog change.</SheetDescription>
        </SheetHeader>
        <form onSubmit={submit} className="space-y-4 overflow-y-auto px-5 py-5">
          <AgentPicker value={agents} onChange={setAgents} except={agentsOf(secret.used_by)} />
          <label className="block space-y-1.5 text-sm"><span className="font-medium">Variable</span>
            <Input value={env} onChange={(e) => setEnv(e.target.value)} placeholder={secret.name} className="font-mono" spellCheck={false} />
            {varError && <span className="block text-xs text-red-600 dark:text-red-400">{varError}</span>}
          </label>
          {error && <ErrorState error={error} />}
          <Button type="submit" className="h-10 w-full" disabled={busy || !agents.length || !!varError}>Give</Button>
        </form>
      </SheetContent>
    </Sheet>
  );
}

function SetSecret({ open, initial, onClose }: { open: boolean; initial?: Secret; onClose: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState(initial?.name ?? "");
  const [kind, setKind] = useState<"string" | "file">(initial?.kind ?? "string");
  const [value, setValue] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [agents, setAgents] = useState<string[]>([]);
  const teams = useTeams();
  const fileRef = useRef<HTMLInputElement>(null);
  const replacing = !!initial;
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    const n = name.trim();
    if (!namePattern.test(n)) return setError("Name: letters, digits and _ (as in ${secret:NAME}), e.g. APPSTORE_API_KEY.");
    let payload: string;
    if (kind === "string") {
      if (new TextEncoder().encode(value).length < 8) return setError("A value of at least 8 characters (shorter ones cannot be hidden in logs).");
      payload = value;
    } else {
      if (!file) return setError("Choose a file.");
      if (file.size > maxFile) return setError("A file of at most 64 KiB.");
      const bytes = new Uint8Array(await file.arrayBuffer());
      let bin = "";
      for (const b of bytes) bin += String.fromCharCode(b);
      payload = btoa(bin);
    }
    setBusy(true);
    try {
      await core(`/secrets/${encodeURIComponent(n)}`, { method: "PUT", body: JSON.stringify({ kind, value: payload }) });
      setValue("");
      toast.success(replacing ? `${n} replaced` : `${n} saved`);
      await qc.invalidateQueries({ queryKey: ["secrets"] });
      if (agents.length && teams.data) {
        try {
          await grant(n, agents, "", teams.data.version);
          toast.success(`${n} given to ${agents.join(", ")}`);
          await Promise.all([qc.invalidateQueries({ queryKey: ["secrets"] }), qc.invalidateQueries({ queryKey: ["catalog"] }), qc.invalidateQueries({ queryKey: ["agent-secrets"] })]);
        } catch (err) {
          toast.error(`${n} is saved but not given to the agents: ${err instanceof Error ? err.message : String(err)}`);
        }
      }
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()}>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-md">
        <SheetHeader className="border-b px-5 py-4">
          <SheetTitle>{replacing ? `Replace ${initial.name}` : "Add a secret"}</SheetTitle>
          <SheetDescription>Stored encrypted. Nobody sees the value again, not even you; agents get it only while they run.</SheetDescription>
        </SheetHeader>
        <form onSubmit={submit} className="space-y-4 overflow-y-auto px-5 py-5" autoComplete="off">
          <label className="block space-y-1.5 text-sm"><span className="font-medium">Name</span>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="APPSTORE_API_KEY" disabled={replacing} className="font-mono" spellCheck={false} required />
          </label>
          <div role="radiogroup" aria-label="Kind" className="grid grid-cols-2 gap-2">
            {(["string", "file"] as const).map((k) => (
              <button key={k} type="button" role="radio" aria-checked={kind === k} onClick={() => setKind(k)}
                className={`flex h-11 items-center justify-center gap-2 rounded-lg border text-sm ${kind === k ? "border-foreground/80 bg-accent/60" : "hover:bg-accent/40"}`}>
                {k === "string" ? <KeyRound className="size-4" /> : <FileKey2 className="size-4" />}{k === "string" ? "Text value" : "File"}
              </button>
            ))}
          </div>
          {kind === "string" ? (
            <label className="block space-y-1.5 text-sm"><span className="font-medium">Value</span>
              <Input type="password" value={value} onChange={(e) => setValue(e.target.value)} placeholder="••••••••" autoComplete="new-password" className="font-mono" required />
            </label>
          ) : (
            <div className="space-y-1.5 text-sm">
              <span className="font-medium">File</span>
              <input ref={fileRef} type="file" className="hidden" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
              <Button type="button" variant="outline" className="h-10 w-full justify-start" onClick={() => fileRef.current?.click()}>
                <Upload className="size-4" /><span className="truncate">{file ? `${file.name} (${Math.ceil(file.size / 1024)} KiB)` : "Choose a file (at most 64 KiB)"}</span>
              </Button>
            </div>
          )}
          {!initial && (
            <div className="space-y-1.5 text-sm">
              <span className="font-medium">Give to agents <span className="font-normal text-muted-foreground">(optional; the variable is its name)</span></span>
              <AgentPicker value={agents} onChange={setAgents} except={[]} />
            </div>
          )}
          {error && <ErrorState error={error} />}
          <Button type="submit" className="h-10 w-full" disabled={busy}>{replacing ? "Replace" : "Save"}</Button>
        </form>
      </SheetContent>
    </Sheet>
  );
}

export default function Page() {
  const role = useCompanyInfo().data?.role;
  const manage = canManage(role);
  const secrets = useSecrets();
  const needed = useQuery({ queryKey: ["secrets", "needed"], queryFn: async () => (await core<Needed[]>("/secrets/needed")) ?? [], enabled: manage });
  const waiting = (needed.data ?? []).filter((n) => Array.isArray(n.agents) && !n.set);
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [editing, setEditing] = useState<{ open: boolean; secret?: Secret }>({ open: false });
  const [giving, setGiving] = useState<Secret | null>(null);
  const remove = async (s: Secret) => {
    const agents = agentsOf(s.used_by);
    const servers = (s.used_by ?? []).filter((u) => u.startsWith("mcp ")).map((u) => u.slice(4));
    const users = agents.length || servers.length
      ? `\n\n${[agents.length ? `Agents ${agents.join(", ")}` : "", servers.length ? `MCP servers ${servers.join(", ")}` : ""].filter(Boolean).join(" and ")} use it: their runs wait until it is set again.`
      : "";
    if (!(await confirm({ title: `Delete ${s.name}?`, description: `The value is gone for good.${users}`, confirm: "Delete", destructive: true }))) return;
    try { await core(`/secrets/${encodeURIComponent(s.name)}`, { method: "DELETE" }); toast.success(`${s.name} deleted`); await qc.invalidateQueries({ queryKey: ["secrets"] }); }
    catch (e) { toast.error(e instanceof Error ? e.message : String(e)); }
  };
  const missing = secrets.error instanceof ApiError && (secrets.error.status === 404 || secrets.error.status === 501);
  return (
    <>
      <PageHeader title="Secrets" description="API keys and files your agents and MCP servers use. Values are never shown."
        actions={manage ? <Button className="h-10" onClick={() => setEditing({ open: true })}><Plus className="size-4" />Add a secret</Button> : undefined} />
      {!manage && role && <p className="mb-4 text-sm text-muted-foreground">You see the names only; owners and admins manage secrets.</p>}
      {waiting.length > 0 && (
        <section className="mb-5 rounded-2xl border border-amber-500/40 bg-amber-500/5 p-4">
          <p className="mb-2 text-sm font-medium">Agents are waiting for these secrets</p>
          <ul className="space-y-2">
            {waiting.map((n) => (
              <li key={n.name} className="flex flex-col gap-2 sm:flex-row sm:items-center">
                <p className="min-w-0 flex-1 text-sm">
                  <span className="font-mono font-medium">{n.name}</span>{" "}
                  <span className="text-muted-foreground">({n.kind === "file" ? "file" : "text"}{n.wrong_kind ? ", set with the wrong kind" : ""}) for {n.agents.join(", ")}</span>
                </p>
                <Button variant="outline" className="h-10" onClick={() => setEditing({ open: true, secret: { name: n.name, kind: n.kind } })}>Set</Button>
              </li>
            ))}
          </ul>
        </section>
      )}
      {secrets.isLoading ? <LoadingRows /> : missing ? (
        <EmptyState icon={KeyRound} title="Secrets are not available on this server yet" hint="This App Factory does not store secrets yet." />
      ) : secrets.error ? <ErrorState error={secrets.error} /> : (secrets.data ?? []).length === 0 ? (
        <EmptyState icon={KeyRound} title="No secrets yet" hint={manage ? "Add the API keys your agents need, e.g. for an MCP server." : undefined} />
      ) : (
        <ul className="divide-y overflow-hidden rounded-2xl border bg-card shadow-xs">
          {secrets.data!.map((s) => (
            <li key={s.name} className="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center md:px-5">
              <div className="min-w-0 flex-1">
                <p className="flex flex-wrap items-center gap-2">
                  <span className="truncate font-mono text-sm font-medium">{s.name}</span>
                  {s.kind && <Badge variant="secondary" className="font-normal">{kindLabel(s.kind)}</Badge>}
                  {s.version !== undefined && <span className="text-xs text-muted-foreground">v{s.version}</span>}
                </p>
                {(s.updated_at || s.updated_by) && <p className="text-xs text-muted-foreground">{s.updated_by ? `${s.updated_by.replace(/^user:/, "")} · ` : ""}{s.updated_at ? ago(s.updated_at) : ""}</p>}
                {s.used_by && <p className="truncate text-xs text-muted-foreground">{s.used_by.length ? `Used by ${s.used_by.join(", ")}` : "Not used by any agent or MCP server"}</p>}
              </div>
              {manage && (
                <div className="flex gap-2">
                  <Button variant="outline" className="h-10" onClick={() => setGiving(s)}><UserPlus className="size-4" />Give to an agent</Button>
                  <Button variant="outline" className="h-10" onClick={() => setEditing({ open: true, secret: s })}>Replace</Button>
                  <Button variant="ghost" className="h-10 text-red-600 hover:text-red-700 dark:text-red-400" onClick={() => remove(s)} aria-label={`Delete ${s.name}`}><Trash2 className="size-4" /></Button>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      {editing.open && <SetSecret open initial={editing.secret} onClose={() => setEditing({ open: false })} />}
      {giving && <GiveSecret secret={giving} onClose={() => setGiving(null)} />}
    </>
  );
}

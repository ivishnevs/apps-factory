"use client";

import { useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, FileKey2, KeyRound, Plus, X } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import Link from "@/components/app/link";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { ApiError, core } from "@/lib/api/client";
import { type AgentSecretUse, envError, kindLabel, problemOf, type SecretUse, useAgentSecrets, useSecrets } from "@/lib/api/secrets";

// AgentSecrets (agent-secrets-ui 1.1): the agent's secret uses in its editor. Its own uses can be added from the
// company's secrets (the kind decides text variable or file) and removed; each change is saved at once as its own
// catalog change with a note. Uses inherited from its profile and its MCP servers' header secrets are read-only.
// Values are never shown. onSaved gets the new catalog version and own uses so the editor's draft stays current.
export function AgentSecrets({ name, onSaved }: { name: string; onSaved: (version: number, secrets: SecretUse[]) => void }) {
  const view = useAgentSecrets(name);
  const company = useSecrets();
  const qc = useQueryClient();
  const [secret, setSecret] = useState("");
  const [env, setEnv] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const uses = view.data?.uses ?? [];
  const own = uses.filter((u) => !u.profile && !u.server);
  const ownUses: SecretUse[] = own.map((u) => ({ name: u.name, env: u.env ?? u.name, file: u.kind === "file" || undefined }));
  const available = (company.data ?? []).filter((s) => !own.some((u) => u.name === s.name));
  const picked = available.find((s) => s.name === secret);
  const variable = env.trim() || secret;
  const varError = secret ? envError(variable, ownUses.map((u) => u.env)) : null;

  const save = async (next: SecretUse[], done: string) => {
    if (!view.data) return;
    setBusy(true);
    setError(null);
    try {
      const v = await core<{ version: number }>(`/catalog/agents/${encodeURIComponent(name)}/secrets`, { method: "PUT", body: JSON.stringify({ base: view.data.base, secrets: next }) });
      onSaved(v.version, next);
      setSecret("");
      setEnv("");
      toast.success(done);
      await Promise.all([qc.invalidateQueries({ queryKey: ["agent-secrets"] }), qc.invalidateQueries({ queryKey: ["secrets"] }), qc.invalidateQueries({ queryKey: ["catalog", "setup"] })]);
    } catch (e) {
      setError(e instanceof ApiError && e.status === 409 ? "The catalog changed meanwhile: close and reopen the agent." : e instanceof Error ? e.message : String(e));
    } finally { setBusy(false); }
  };
  const add = () => {
    if (!picked || varError) return;
    save([...ownUses, { name: picked.name, env: variable, file: picked.kind === "file" || undefined }], `${picked.name} given to ${name}`);
  };
  const remove = (u: AgentSecretUse) => save(ownUses.filter((x) => x.name !== u.name), `${u.name} taken from ${name}`);

  return (
    <section className="space-y-2 text-sm">
      <p className="font-medium">Secrets</p>
      <p className="text-xs text-muted-foreground">What this agent&apos;s runs get: a text secret in its variable, a file at the path in its variable. Each change is saved at once.</p>
      {view.error && <p className="text-xs text-red-600 dark:text-red-400">{view.error instanceof Error ? view.error.message : String(view.error)}</p>}
      {uses.length > 0 && (
        <ul className="divide-y rounded-lg border">
          {uses.map((u) => {
            const problem = problemOf(u);
            const from = u.profile ? `from profile ${u.profile}` : u.server ? `for MCP server ${u.server}` : null;
            return (
              <li key={`${u.name}:${u.profile ?? ""}:${u.server ?? ""}`} className="flex items-start gap-2 px-3 py-2">
                {u.kind === "file" ? <FileKey2 className="mt-0.5 size-4 shrink-0 text-muted-foreground" /> : <KeyRound className="mt-0.5 size-4 shrink-0 text-muted-foreground" />}
                <div className="min-w-0 flex-1">
                  <p className="flex flex-wrap items-center gap-1.5">
                    <span className="font-mono text-xs font-medium">{u.name}</span>
                    {u.env && <span className="font-mono text-xs text-muted-foreground">→ ${u.env}{u.kind === "file" ? " (path)" : ""}</span>}
                    <Badge variant="secondary" className="font-normal">{kindLabel(u.kind)}</Badge>
                    {from && <span className="text-xs text-muted-foreground">{from}</span>}
                  </p>
                  {problem && <p className="flex items-center gap-1 text-xs text-amber-700 dark:text-amber-400"><AlertTriangle className="size-3.5" />{problem}</p>}
                </div>
                {!from && <Button variant="ghost" size="sm" className="h-8" disabled={busy} onClick={() => remove(u)} aria-label={`Take ${u.name} from ${name}`}><X className="size-4" /></Button>}
              </li>
            );
          })}
        </ul>
      )}
      {!view.isLoading && uses.length === 0 && <p className="text-xs text-muted-foreground">No secrets.</p>}
      {available.length > 0 ? (
        <div className="grid gap-2 sm:grid-cols-[1fr_1fr_auto]">
          <Select value={secret} onValueChange={(v) => { setSecret(v); setEnv(""); }}>
            <SelectTrigger className="w-full" aria-label="Secret"><SelectValue placeholder="Give a secret…" /></SelectTrigger>
            <SelectContent>{available.map((s) => <SelectItem key={s.name} value={s.name}>{s.name} ({kindLabel(s.kind)})</SelectItem>)}</SelectContent>
          </Select>
          <Input value={env} onChange={(e) => setEnv(e.target.value)} placeholder={secret || "VARIABLE"} aria-label="Variable name" className="font-mono" spellCheck={false} disabled={!secret} />
          <Button type="button" variant="outline" className="h-9" disabled={busy || !picked || !!varError} onClick={add}><Plus className="size-4" />Give</Button>
        </div>
      ) : !company.isLoading && <p className="text-xs text-muted-foreground">Add secrets on the <Link href="/secrets" className="underline">Secrets page</Link> to give them to agents.</p>}
      {picked && !varError && <p className="text-xs text-muted-foreground">{picked.kind === "file" ? `The run gets the file; $${variable} holds its path.` : `The run gets the value in $${variable}.`}</p>}
      {varError && <p className="text-xs text-red-600 dark:text-red-400">{varError}</p>}
      {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}
    </section>
  );
}

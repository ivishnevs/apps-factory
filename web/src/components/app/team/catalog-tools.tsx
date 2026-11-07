"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, Undo2, Upload } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { Button } from "@/components/ui/button";
import { core } from "@/lib/api/client";
import { ago } from "@/lib/format";

// Structured catalog (structured-catalog 1.3–1.4): the company's configuration changes entity by entity, with undo,
// and the whole configuration as one YAML file to download or load.
interface Change { seq: number; at: number; author: string; person?: string; note?: string; changes: { path: string; before: string; after: string }[] }

const entityWord = (p: string) => {
  const name = (p.split("/").pop() ?? p).replace(/\.(ya?ml|md)$/, "");
  if (p.startsWith("prompts/")) return `prompt of ${name}`;
  if (p.includes("/agents/")) return `agent ${name}`;
  if (p.includes("/teams/")) return `team ${name}`;
  if (p.includes("/profiles/")) return `shared settings ${name}`;
  if (p.includes("/policies/")) return `rules ${name}`;
  return name;
};

export function CatalogChanges({ manage }: { manage: boolean }) {
  const qc = useQueryClient();
  const confirm = useConfirm();
  const changes = useQuery({ queryKey: ["catalog-changes"], queryFn: () => core<Change[]>("/catalog/changes") });
  const undo = async (c: Change) => {
    if (!(await confirm({ title: "Undo this change?", description: `${c.note ?? ""}\n${c.changes.map((x) => entityWord(x.path)).join(", ")} go back to what they were before.`, confirm: "Undo" }))) return;
    try {
      await core(`/catalog/changes/${c.seq}/undo`, { method: "POST", body: "{}" });
      await qc.invalidateQueries();
      toast.success("Undone");
    } catch (e) { toast.error(e instanceof Error ? e.message : String(e)); }
  };
  if (!changes.data?.length) return <p className="text-sm text-muted-foreground">No changes recorded yet.</p>;
  return (
    <ul className="divide-y rounded-xl border bg-card text-sm">
      {changes.data.map((c) => (
        <li key={c.seq} className="flex items-start gap-3 px-4 py-3">
          <div className="min-w-0 flex-1">
            <p className="truncate font-medium">{c.note || "Change"}</p>
            <p className="truncate text-xs text-muted-foreground">{c.changes.map((x) => `${x.after ? (x.before ? "changed" : "added") : "deleted"} ${entityWord(x.path)}`).join(", ")}</p>
            <p className="text-xs text-muted-foreground">{c.person || c.author} · {ago(c.at)}</p>
          </div>
          {manage && <Button variant="outline" size="sm" className="h-9 shrink-0" onClick={() => undo(c)}><Undo2 className="size-4" />Undo</Button>}
        </li>
      ))}
    </ul>
  );
}

export function CatalogFile({ manage }: { manage: boolean }) {
  const qc = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const load = async (f: File) => {
    setBusy(true);
    try {
      await core("/catalog/import", { method: "POST", body: JSON.stringify({ yaml: await f.text(), note: `YAML import (${f.name})` }) });
      await qc.invalidateQueries();
      toast.success("Configuration loaded");
    } catch (e) { toast.error(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); }
  };
  return (
    <div className="flex flex-wrap gap-2">
      <Button variant="outline" className="h-10" asChild><a href="/api/core/catalog/export" download="company-configuration.yaml"><Download className="size-4" />Download as YAML</a></Button>
      {manage && (
        <>
          <Button variant="outline" className="h-10" disabled={busy} onClick={() => input.current?.click()}><Upload className="size-4" />Load a YAML file</Button>
          <input ref={input} type="file" accept=".yaml,.yml,text/yaml" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) void load(f); e.target.value = ""; }} />
        </>
      )}
    </div>
  );
}

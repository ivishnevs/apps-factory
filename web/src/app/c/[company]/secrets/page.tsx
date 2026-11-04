"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { FileKey2, KeyRound, Plus, Trash2, Upload } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { ApiError, core, useCompanyInfo } from "@/lib/api/client";
import { canManage } from "@/lib/api/types-account";
import { ago } from "@/lib/format";

// Secrets of the company (core-secret-store 1.3): API keys and files the company's agents and MCP servers use.
// Core keeps them encrypted and never shows a value again; a node receives a value only for a run that needs it.
// Owners and admins add, replace and delete; members see the names only.

interface Secret { name: string; kind?: "string" | "file"; version?: number; updated_by?: string; updated_at?: number; used_by?: string[] }

const namePattern = /^[A-Za-z_][A-Za-z0-9_]{0,127}$/;
const maxFile = 64 << 10;

function list(v: unknown): Secret[] {
  if (Array.isArray(v)) return v as Secret[];
  const o = v as { secrets?: Secret[]; items?: Secret[] } | null;
  return o?.secrets ?? o?.items ?? [];
}

function SetSecret({ open, initial, onClose }: { open: boolean; initial?: Secret; onClose: () => void }) {
  const qc = useQueryClient();
  const [name, setName] = useState(initial?.name ?? "");
  const [kind, setKind] = useState<"string" | "file">(initial?.kind ?? "string");
  const [value, setValue] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
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
  const secrets = useQuery({ queryKey: ["secrets"], queryFn: async () => list(await core<unknown>("/secrets")) });
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [editing, setEditing] = useState<{ open: boolean; secret?: Secret }>({ open: false });
  const remove = async (s: Secret) => {
    const users = s.used_by?.length ? `\n\nUsed by: ${s.used_by.join(", ")}. Their runs wait until it is set again.` : "";
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
                  {s.kind && <Badge variant="secondary" className="font-normal">{s.kind === "file" ? "file" : "text"}</Badge>}
                  {s.version !== undefined && <span className="text-xs text-muted-foreground">v{s.version}</span>}
                </p>
                {(s.updated_at || s.updated_by) && <p className="text-xs text-muted-foreground">{s.updated_by ? `${s.updated_by.replace(/^user:/, "")} · ` : ""}{s.updated_at ? ago(s.updated_at) : ""}</p>}
                {s.used_by && <p className="truncate text-xs text-muted-foreground">{s.used_by.length ? `Used by ${s.used_by.join(", ")}` : "Not used by any agent or MCP server"}</p>}
              </div>
              {manage && (
                <div className="flex gap-2">
                  <Button variant="outline" className="h-10" onClick={() => setEditing({ open: true, secret: s })}>Replace</Button>
                  <Button variant="ghost" className="h-10 text-red-600 hover:text-red-700 dark:text-red-400" onClick={() => remove(s)} aria-label={`Delete ${s.name}`}><Trash2 className="size-4" /></Button>
                </div>
              )}
            </li>
          ))}
        </ul>
      )}
      {editing.open && <SetSecret open initial={editing.secret} onClose={() => setEditing({ open: false })} />}
    </>
  );
}

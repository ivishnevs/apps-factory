"use client";

import { useQuery } from "@tanstack/react-query";
import { Copy, KeyRound, Plus, ShieldCheck, X } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { ErrorState } from "@/components/app/page";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { core, useCore } from "@/lib/api/client";
import { ago } from "@/lib/format";

// Connectors (the UI's word for nodes). Enrolling a node (node-identity D3, 4.3): an owner or admin issues a one-time code, runs factory-node enroll CODE
// on the machine, which prints its key fingerprint and waits; the owner approves after comparing the fingerprint
// shown here with the one on the machine (a stolen code then enrolls nothing). Revoking cuts a node off.

interface Issued { id: string; name: string; code: string; expires_at: number }
export interface NodeKey { node_id: string; name: string; fingerprint: string; status: string; created_at: number; next_fingerprint?: string }

const copy = (text: string) => navigator.clipboard.writeText(text).then(() => toast.success("Copied"), () => toast.error("Copy failed"));
const plain = (f: string) => f.toLowerCase().replace(/[^0-9a-z]/g, "");

interface NodeInstall { core_url: string; command: string; available: string[] }

// platformOf guesses the visitor's machine from the browser; the install command is the same for every platform.
function platformOf(): string {
  if (typeof navigator === "undefined") return "";
  const ua = navigator.userAgent.toLowerCase();
  if (ua.includes("mac")) return "darwin";
  if (ua.includes("linux") && !ua.includes("android")) return "linux";
  return "";
}

const platformWords: Record<string, string> = { "darwin-arm64": "macOS · Apple silicon", "darwin-amd64": "macOS · Intel", "linux-arm64": "Linux · arm64", "linux-amd64": "Linux · x86-64" };

function CommandLine({ command }: { command: string }) {
  return (
    <div className="flex items-start gap-2">
      <code className="min-w-0 flex-1 break-all rounded-md border bg-muted/40 px-2 py-2 font-mono text-xs">{command}</code>
      <Button variant="outline" size="sm" className="h-9 shrink-0" onClick={() => copy(command)}><Copy className="size-4" />Copy</Button>
    </div>
  );
}

// InstallConnector (node-install, saas-web-ui D8): one command installs the connector, connects it by sign-in
// (the terminal opens /connect/<id>) and starts it; "with a code" is for machines without a browser.
export function InstallConnector() {
  const info = useQuery({ queryKey: ["node-install"], queryFn: () => core<NodeInstall>("/node-install"), staleTime: 60_000 });
  const [mode, setMode] = useState<"signin" | "code">("signin");
  const [name, setName] = useState("");
  const [issued, setIssued] = useState<Issued | null>(null);
  const issue = useCore<Issued>("POST", "/node-enrollments");
  const platform = platformOf();
  if (info.error) return <ErrorState error={info.error} />;
  if (!info.data) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const d = info.data;
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    try { setIssued(await issue.mutateAsync({ name: name.trim() })); }
    catch (err) { toast.error(err instanceof Error ? err.message : String(err)); }
  };
  return (
    <div className="space-y-5 text-sm">
      {platform === "" && <p className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-amber-800 dark:text-amber-300">Connectors run on macOS and Linux. Run the command on such a machine.</p>}
      <div className="flex gap-1 rounded-lg border p-1">
        {(["signin", "code"] as const).map((m) => (
          <button key={m} type="button" onClick={() => setMode(m)} className={`flex-1 rounded-md px-2 py-1.5 text-sm ${mode === m ? "bg-accent font-medium" : "text-muted-foreground"}`}>
            {m === "signin" ? "Sign in (recommended)" : "With a code"}
          </button>
        ))}
      </div>
      {mode === "signin" ? (
        <ol className="list-decimal space-y-3 pl-5">
          <li><p className="mb-2">On the machine, in a terminal:</p><CommandLine command={d.command} /></li>
          <li>It installs the connector, then opens a page here to approve the machine. Compare the code it shows with the terminal and connect it.</li>
          <li>The connector starts on its own and appears below. No browser on that machine? End the command with <span className="font-mono">sh -s -- --no-browser</span> and open the printed address anywhere you are signed in.</li>
        </ol>
      ) : issued ? (
        <ol className="list-decimal space-y-3 pl-5">
          <li><p className="mb-2">On the machine:</p><CommandLine command={`${d.command} -s -- --code ${issued.code}`} />
            <p className="mt-1.5 text-xs text-muted-foreground">The code works once and expires {new Date(issued.expires_at * 1000).toLocaleString([], { hour: "2-digit", minute: "2-digit" })}. It is shown only now.</p></li>
          <li>It prints the connector&apos;s fingerprint and waits: approve <span className="font-medium">{issued.name}</span> under Waiting for approval after comparing it.</li>
        </ol>
      ) : (
        <form onSubmit={submit} className="space-y-3">
          <label className="block space-y-1.5"><span className="font-medium">Name of the machine</span>
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="mac-mini-1" required maxLength={100} />
          </label>
          <Button type="submit" className="h-10 w-full" disabled={issue.isPending || !name.trim()}><KeyRound className="size-4" />Issue a code</Button>
        </form>
      )}
      <details className="rounded-lg border px-3 py-2">
        <summary className="cursor-pointer font-medium">What the machine needs · manual download</summary>
        <ul className="mt-2 list-disc space-y-1 pl-5 text-muted-foreground">
          <li>macOS or Linux, <span className="font-mono">git</span>, Node.js 22+ (Claude and Codex runtimes are installed next to the connector).</li>
          <li>Signed in to Claude (<span className="font-mono">claude</span>) and/or Codex (<span className="font-mono">codex login</span>) with your subscription.</li>
        </ul>
        {d.available.length > 0 ? (
          <ul className="mt-2 space-y-1">
            {d.available.map((p) => (
              <li key={p}><a className="underline" href={`${d.core_url}/downloads/node/factory-node-${p}.tar.gz`}>{platformWords[p] ?? p}</a>{p.startsWith(platform) && platform ? <span className="ml-2 text-xs text-muted-foreground">this machine?</span> : null}</li>
            ))}
            <li><a className="underline" href={`${d.core_url}/downloads/node/SHA256SUMS`}>SHA256SUMS</a></li>
          </ul>
        ) : <p className="mt-2 text-muted-foreground">This server has no connector packages.</p>}
      </details>
    </div>
  );
}

export function AddNode() {
  const [open, setOpen] = useState(false);
  return (
    <Sheet open={open} onOpenChange={setOpen}>
      <SheetTrigger asChild><Button className="h-10"><Plus className="size-4" />Add a connector</Button></SheetTrigger>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-md">
        <SheetHeader className="border-b px-5 py-4">
          <SheetTitle>Add a connector</SheetTitle>
          <SheetDescription>A machine of yours that runs your agents. It connects out to App Factory; it needs no open port.</SheetDescription>
        </SheetHeader>
        <div className="overflow-y-auto px-5 py-5"><InstallConnector /></div>
      </SheetContent>
    </Sheet>
  );
}

// PendingNodes are enrolled machines (and key rotations) waiting for an owner's approval.
export function PendingNodes() {
  const keys = useQuery({ queryKey: ["node-keys"], queryFn: () => core<NodeKey[]>("/node-keys"), refetchInterval: 5000 });
  const [approving, setApproving] = useState<NodeKey | null>(null);
  const confirm = useConfirm();
  const waiting = (keys.data ?? []).filter((k) => k.status === "pending" || !!k.next_fingerprint);
  if (keys.error) return <ErrorState error={keys.error} />;
  if (waiting.length === 0) return null;
  const reject = async (k: NodeKey) => {
    if (!(await confirm({ title: `Reject ${k.name || k.node_id}?`, description: "Its keys are dropped; the machine has to enroll again with a new code.", confirm: "Reject", destructive: true }))) return;
    try { await core(`/node-keys/${encodeURIComponent(k.node_id)}/reject`, { method: "POST", body: "{}" }); toast.success("Rejected"); await keys.refetch(); }
    catch (e) { toast.error(e instanceof Error ? e.message : String(e)); }
  };
  return (
    <section aria-labelledby="pending-title">
      <h2 id="pending-title" className="mb-1 text-base font-semibold">Waiting for approval <span className="ml-1 text-sm font-normal tabular-nums text-muted-foreground">{waiting.length}</span></h2>
      <p className="mb-3 text-sm text-muted-foreground">Approve only a fingerprint you see on your own machine.</p>
      <ul className="divide-y overflow-hidden rounded-2xl border bg-card shadow-xs">
        {waiting.map((k) => (
          <li key={k.node_id} className="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center md:px-5">
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium">{k.name || k.node_id} {k.next_fingerprint && <span className="font-normal text-muted-foreground">· new keys</span>}</p>
              <p className="truncate font-mono text-xs text-muted-foreground">{k.next_fingerprint || k.fingerprint}</p>
              <p className="text-xs text-muted-foreground">{k.node_id} · {ago(k.created_at)}</p>
            </div>
            <div className="flex gap-2">
              <Button variant="outline" className="h-10" onClick={() => reject(k)}><X className="size-4" />Reject</Button>
              <Button className="h-10" onClick={() => setApproving(k)}><ShieldCheck className="size-4" />Approve…</Button>
            </div>
          </li>
        ))}
      </ul>
      {approving && <ApproveDialog k={approving} onClose={() => { setApproving(null); void keys.refetch(); }} />}
    </section>
  );
}

function ApproveDialog({ k, onClose }: { k: NodeKey; onClose: () => void }) {
  const want = k.next_fingerprint || k.fingerprint;
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const matches = plain(typed) !== "" && plain(typed) === plain(want);
  const approve = async () => {
    setBusy(true);
    try {
      await core(`/node-keys/${encodeURIComponent(k.node_id)}/approve`, { method: "POST", body: JSON.stringify({ fingerprint: want }) });
      toast.success(`${k.name || k.node_id} approved`);
      onClose();
    } catch (e) { toast.error(e instanceof Error ? e.message : String(e)); setBusy(false); }
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Approve {k.name || k.node_id}</DialogTitle>
          <DialogDescription>Enter the fingerprint <span className="font-mono">factory-node</span> printed on the machine. It must be this one:</DialogDescription>
        </DialogHeader>
        <code className="block break-all rounded-md border bg-muted/40 px-2 py-2 font-mono text-xs">{want}</code>
        <Input value={typed} onChange={(e) => setTyped(e.target.value)} placeholder="Fingerprint from the machine" className="font-mono" aria-label="Fingerprint from the machine" autoFocus />
        {typed && !matches && <p className="text-sm text-red-600 dark:text-red-400">It does not match: do not approve a machine you do not know.</p>}
        <DialogFooter className="gap-2">
          <Button variant="outline" onClick={onClose}>Cancel</Button>
          <Button onClick={approve} disabled={!matches || busy}><ShieldCheck className="size-4" />Approve</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

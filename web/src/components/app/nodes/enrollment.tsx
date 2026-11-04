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

// Enrolling a node (node-identity D3, 4.3): an owner or admin issues a one-time code, runs factory-node enroll CODE
// on the machine, which prints its key fingerprint and waits; the owner approves after comparing the fingerprint
// shown here with the one on the machine (a stolen code then enrolls nothing). Revoking cuts a node off.

interface Issued { id: string; name: string; code: string; expires_at: number }
export interface NodeKey { node_id: string; name: string; fingerprint: string; status: string; created_at: number; next_fingerprint?: string }

const copy = (text: string) => navigator.clipboard.writeText(text).then(() => toast.success("Copied"), () => toast.error("Copy failed"));
const plain = (f: string) => f.toLowerCase().replace(/[^0-9a-z]/g, "");

export function AddNode() {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [issued, setIssued] = useState<Issued | null>(null);
  const issue = useCore<Issued>("POST", "/node-enrollments");
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    try { setIssued(await issue.mutateAsync({ name: name.trim() })); }
    catch (err) { toast.error(err instanceof Error ? err.message : String(err)); }
  };
  const command = issued ? `factory-node enroll ${issued.code}` : "";
  return (
    <Sheet open={open} onOpenChange={(o) => { setOpen(o); if (!o) { setIssued(null); setName(""); } }}>
      <SheetTrigger asChild><Button className="h-10"><Plus className="size-4" />Add a node</Button></SheetTrigger>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-md">
        <SheetHeader className="border-b px-5 py-4">
          <SheetTitle>Add a node</SheetTitle>
          <SheetDescription>A machine of yours that runs your agents. It connects out to App Factory; it needs no open port.</SheetDescription>
        </SheetHeader>
        <div className="space-y-5 overflow-y-auto px-5 py-5">
          {!issued ? (
            <form onSubmit={submit} className="space-y-3">
              <label className="block space-y-1.5 text-sm"><span className="font-medium">Name</span>
                <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="mac-mini-1" autoFocus required maxLength={100} />
              </label>
              <Button type="submit" className="h-10 w-full" disabled={issue.isPending || !name.trim()}><KeyRound className="size-4" />Issue a code</Button>
            </form>
          ) : (
            <>
              <ol className="list-decimal space-y-4 pl-5 text-sm">
                <li>
                  <p className="mb-2">On the machine, with <span className="font-mono">factory-node</span> installed, run:</p>
                  <div className="flex items-center gap-2">
                    <code className="min-w-0 flex-1 truncate rounded-md border bg-muted/40 px-2 py-2 font-mono text-xs">{command}</code>
                    <Button variant="outline" size="sm" className="h-9" onClick={() => copy(command)}><Copy className="size-4" />Copy</Button>
                  </div>
                  <p className="mt-1.5 text-xs text-muted-foreground">The code works once and expires {new Date(issued.expires_at * 1000).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}. It is shown only now.</p>
                </li>
                <li>It prints the node&apos;s fingerprint and waits.</li>
                <li>Approve <span className="font-medium">{issued.name}</span> under Waiting for approval after comparing the fingerprints.</li>
              </ol>
              <Button variant="outline" className="h-10 w-full" onClick={() => setOpen(false)}>Done</Button>
            </>
          )}
        </div>
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

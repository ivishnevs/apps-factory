"use client";

import { Download, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { core } from "@/lib/api/client";

// Your data (saas-release-readiness 4.2): an owner downloads or deletes the company, a person downloads their data
// or deletes their account. Core checks the role and the typed confirmation; deleting cannot be undone.

function TypedDelete({ open, onClose, title, description, word, label, action }: {
  open: boolean; onClose: () => void; title: string; description: string; word: string; label: string; action: () => Promise<void>;
}) {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try { await action(); } catch (err) { toast.error(err instanceof Error ? err.message : String(err)); setBusy(false); }
  };
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) { onClose(); setTyped(""); } }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription className="whitespace-pre-wrap">{description}</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <label className="block space-y-1.5 text-sm"><span>Type <span className="font-mono font-medium">{word}</span> to confirm</span>
            <Input value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" autoFocus aria-label={label} />
          </label>
          <DialogFooter className="gap-2">
            <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" variant="destructive" disabled={busy || typed.trim().toLowerCase() !== word.toLowerCase()}><Trash2 className="size-4" />Delete for good</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function CompanyData({ company, name }: { company: string; name: string }) {
  const [deleting, setDeleting] = useState(false);
  const remove = async () => {
    await core("/company", { method: "DELETE", body: JSON.stringify({ confirm: company }) });
    window.location.assign("/welcome");
  };
  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        <p className="flex-1 text-sm text-muted-foreground">A zip of everything the company keeps: tasks, chats, runs, projects, teams, materials and files. Secret values and tokens are left out.</p>
        <Button asChild variant="outline" className="h-10"><a href={`/api/core/company/export?company=${encodeURIComponent(company)}`} download><Download className="size-4" />Export company</a></Button>
      </div>
      <div className="flex flex-col gap-3 border-t pt-4 sm:flex-row sm:items-center">
        <p className="flex-1 text-sm text-muted-foreground">Deletes {name} with all its data for everyone in it. Machines connected to it stop getting work. This cannot be undone.</p>
        <Button variant="destructive" className="h-10" onClick={() => setDeleting(true)}><Trash2 className="size-4" />Delete company</Button>
      </div>
      <TypedDelete open={deleting} onClose={() => setDeleting(false)} title={`Delete ${name}?`} word={company} label="Company id"
        description={"Every task, chat, run, project, team, secret, file and member list of this company is deleted at once. Export it first if you want a copy."}
        action={remove} />
    </div>
  );
}

export function AccountData({ email }: { email: string }) {
  const [deleting, setDeleting] = useState(false);
  const remove = async () => {
    await core("/me", { method: "DELETE", body: JSON.stringify({ confirm: email }) });
    window.location.assign("/");
  };
  return (
    <div className="mt-5 space-y-4 border-t pt-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        <p className="flex-1 text-sm text-muted-foreground">A zip of your profile, sign-ins, memberships, subscription and the companies of your account.</p>
        <Button asChild variant="outline" className="h-10"><a href="/api/core/me/export" download><Download className="size-4" />Export my data</a></Button>
      </div>
      <div className="flex flex-col gap-3 border-t pt-4 sm:flex-row sm:items-center">
        <p className="flex-1 text-sm text-muted-foreground">Deletes your account, the companies it owns with all their data, and your place in other companies. A paid plan must be cancelled on the Billing page first.</p>
        <Button variant="destructive" className="h-10" onClick={() => setDeleting(true)}><Trash2 className="size-4" />Delete account</Button>
      </div>
      <TypedDelete open={deleting} onClose={() => setDeleting(false)} title="Delete your account?" word={email} label="Your email"
        description={"Your account and every company it owns are deleted with all their data, for everyone in them. Companies of other people stay; you leave them. This cannot be undone."}
        action={remove} />
    </div>
  );
}

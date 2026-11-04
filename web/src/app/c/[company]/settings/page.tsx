"use client";

import { Building2, LogOut } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { signOut, useCompanyInfo, useCore, useMe } from "@/lib/api/client";

// Company settings (user-accounts 6.2): the company's name (owners rename it; its address stays), your role
// and account, and — only with the multi-company setting on — a new company.
const roleWords = { owner: "Owner: everything, including renaming the company", admin: "Admin: team, skills, secrets, nodes and projects", member: "Member: tasks, chats and approvals" };

export default function Page() {
  const info = useCompanyInfo();
  const me = useMe();
  const rename = useCore<{ name: string }>("PATCH", "/company", [["company-info"], ["me"]]);
  const [name, setName] = useState("");
  useEffect(() => { if (info.data) setName(info.data.name); }, [info.data]);
  if (info.error) return <><PageHeader title="Settings" /><ErrorState error={info.error} /></>;
  if (!info.data) return <><PageHeader title="Settings" /><LoadingRows /></>;
  const owner = info.data.role === "owner";
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    try { await rename.mutateAsync({ name: name.trim() }); toast.success("Company renamed"); }
    catch (err) { toast.error(err instanceof Error ? err.message : String(err)); }
  };
  return (
    <>
      <PageHeader title="Settings" description="This company and your account. Switch or add companies in the menu at the top." />
      <section className="mb-6 rounded-xl border bg-card p-4 sm:p-5">
        <h2 className="mb-3 flex items-center gap-2 font-medium"><Building2 className="size-4" />Company</h2>
        <form onSubmit={save} className="flex flex-col gap-3 sm:flex-row sm:items-end">
          <label className="flex-1 space-y-1.5 text-sm"><span className="font-medium">Name</span>
            <Input value={name} onChange={(e) => setName(e.target.value)} disabled={!owner} maxLength={200} required />
          </label>
          {owner && <Button type="submit" className="h-10" disabled={rename.isPending || !name.trim() || name.trim() === info.data.name}>Rename</Button>}
        </form>
        <p className="mt-2 text-xs text-muted-foreground">Address: <span className="font-mono">/c/{info.data.id}</span> (stays when you rename).{!owner && " Only the owner renames the company."}</p>
        <p className="mt-4 flex flex-wrap items-center gap-2 text-sm">Your role <Badge variant="secondary">{info.data.role}</Badge><span className="text-muted-foreground">{roleWords[info.data.role]}</span></p>
      </section>
      {me.data?.kind === "session" && (
        <section className="mb-6 rounded-xl border bg-card p-4 sm:p-5">
          <h2 className="mb-1 font-medium">Account</h2>
          <p className="text-sm text-muted-foreground">{me.data.user?.name ? `${me.data.user.name} · ` : ""}{me.data.user?.email}</p>
          <Button variant="outline" className="mt-3 h-10" onClick={signOut}><LogOut className="size-4" />Sign out</Button>
        </section>
      )}
      {loading(me.isLoading)}
    </>
  );
}

const loading = (on: boolean) => (on ? <LoadingRows /> : null);

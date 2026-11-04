"use client";

import { Building2, Copy, KeyRound, Plus } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useCompanies, usePost, useTemplates } from "@/lib/api/client";
import type { CompanyCreated } from "@/lib/api/types-catalog";
import { ago } from "@/lib/format";

// Companies (company-self-service 4.1, 6.1): the operator creates a company from a starter team template and
// hands its owner the first owner token, shown once here. An owner token cannot use this page.
const templateWords: Record<string, string> = {
  solo: "Solo builder: one agent plans, builds and ships",
  "product-team": "Product owner, developer and reviewer",
  "full-team": "Product owner, designer, developer and QA",
};

export default function Page() {
  const companies = useCompanies();
  const templates = useTemplates();
  const create = usePost<CompanyCreated>("/companies");
  const [id, setId] = useState("");
  const [name, setName] = useState("");
  const [template, setTemplate] = useState("product-team");
  const [created, setCreated] = useState<CompanyCreated | null>(null);
  const [refusal, setRefusal] = useState("");
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setRefusal("");
    try { setCreated(await create.mutateAsync({ id: id.trim(), name: name.trim(), template })); setId(""); setName(""); }
    catch (err) { setRefusal(err instanceof Error ? err.message : String(err)); }
  };
  if (companies.error) {
    return (<><PageHeader title="Companies" /><EmptyState title="Only the operator manages companies" hint="This App Factory is connected with a company owner token." icon={Building2} /></>);
  }
  return (
    <>
      <PageHeader title="Companies" description="Create a company from a starter team; its owner gets their own token." />
      {created && (
        <div className="mb-5 rounded-xl border border-emerald-500/30 bg-emerald-500/5 p-4 text-sm">
          <p className="flex items-center gap-2 font-medium"><KeyRound className="size-4" />Company {created.company} is ready (catalog version {created.catalog_version})</p>
          <p className="mt-1 text-muted-foreground">Give this owner token to the company&apos;s owner. It is shown only now.</p>
          <div className="mt-3 flex items-center gap-2">
            <code className="min-w-0 flex-1 truncate rounded-md border bg-background px-2 py-1.5 font-mono text-xs">{created.owner_token.token}</code>
            <Button variant="outline" className="h-9" onClick={() => navigator.clipboard.writeText(created.owner_token.token).then(() => toast.success("Copied"))}><Copy className="size-4" />Copy</Button>
          </div>
        </div>
      )}
      <form onSubmit={submit} className="mb-6 grid gap-3 rounded-xl border bg-card p-4 md:grid-cols-[1fr_1fr_1.4fr_auto] md:items-end">
        <label className="space-y-1.5 text-sm"><span className="font-medium">ID</span><Input value={id} onChange={(e) => setId(e.target.value)} placeholder="acme" required /></label>
        <label className="space-y-1.5 text-sm"><span className="font-medium">Name</span><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Acme Inc." /></label>
        <label className="space-y-1.5 text-sm"><span className="font-medium">Starter team</span>
          <Select value={template} onValueChange={setTemplate}>
            <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>{(templates.data ?? []).map((t) => <SelectItem key={t} value={t}>{templateWords[t] ?? t}</SelectItem>)}</SelectContent>
          </Select>
        </label>
        <Button type="submit" className="h-10" disabled={!id.trim() || create.isPending}><Plus className="size-4" />Create</Button>
        {refusal && <div className="md:col-span-4"><ErrorState error={refusal} /></div>}
      </form>
      {companies.isLoading ? <LoadingRows /> : (
        <ul className="divide-y rounded-xl border bg-card">
          {(companies.data ?? []).map((c) => (
            <li key={c.id} className="flex items-center gap-3 px-4 py-3">
              <Building2 className="size-5 shrink-0 text-muted-foreground" />
              <div className="min-w-0 flex-1"><p className="truncate font-medium">{c.name}</p><p className="text-xs text-muted-foreground">{c.id}{c.created_at ? ` · created ${ago(c.created_at)}` : ""}</p></div>
              <span className="text-xs text-muted-foreground">{c.catalog_version ? `catalog v${c.catalog_version}` : "no catalog"}</span>
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Sparkles } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { parseDocument } from "yaml";
import Link from "@/components/app/link";
import { EmptyState, ErrorState, LoadingRows } from "@/components/app/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { api, ApiError, core } from "@/lib/api/client";
import type { CatalogDocument, TeamMember } from "@/lib/api/types-catalog";
import { scopeLabel, type InstalledSkill } from "@/lib/api/types-skills";
import { cn } from "@/lib/utils";

// The agent's skill picker (skill-registry 6.2, design D6): the skills installed for the company that the agent's
// team may use (Factory's and the company's own), with their categories; no URLs here, sources are managed on the
// Skills page. Saving writes the agent's spec.skills.include and makes a new catalog version on top of base.
export function SkillPicker({ member, base, open, onClose }: { member: TeamMember; base: number; open: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const team = member.team ?? "";
  const installed = useQuery({
    queryKey: ["skills", "installed", team], enabled: open,
    queryFn: () => core<InstalledSkill[]>(`/skills/installed${team ? `?team=${encodeURIComponent(team)}` : ""}`),
  });
  const [chosen, setChosen] = useState<Set<string>>(new Set(member.skills ?? []));
  const [q, setQ] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (open) setChosen(new Set(member.skills ?? [])); }, [open, member.skills]);
  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (installed.data ?? []).filter((s) => !needle || [s.name, s.description, ...(s.categories ?? [])].some((f) => f?.toLowerCase().includes(needle)));
  }, [installed.data, q]);
  const toggle = (name: string) => setChosen((c) => { const n = new Set(c); if (n.has(name)) n.delete(name); else n.add(name); return n; });
  const save = async () => {
    if (!member.document) return;
    setBusy(true);
    try {
      const doc = await api<CatalogDocument>(`/catalog/document?path=${encodeURIComponent(member.document)}&version=${base}`);
      const y = parseDocument(doc.content);
      const include = [...chosen].sort();
      if (include.length) y.setIn(["spec", "skills", "include"], include);
      else y.deleteIn(["spec", "skills", "include"]);
      await core("/catalog", { method: "POST", body: JSON.stringify({ base, put: { [member.document]: y.toString() }, note: `skills of ${member.name}: ${include.join(", ") || "none"}` }) });
      toast.success(`Skills of ${member.name} saved`);
      await qc.invalidateQueries({ queryKey: ["catalog"] });
      onClose();
    } catch (e) {
      toast.error(e instanceof ApiError && e.status === 409 ? "The catalog changed meanwhile: reopen and try again." : e instanceof Error ? e.message : String(e));
    } finally { setBusy(false); }
  };
  const unavailable = installed.error instanceof ApiError && (installed.error.status === 404 || installed.error.status === 501);
  return (
    <Sheet open={open} onOpenChange={(o) => !o && onClose()}>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-lg">
        <SheetHeader className="border-b px-5 py-4">
          <SheetTitle>Skills of {member.name}</SheetTitle>
          <SheetDescription>
            Skills installed for {team ? `team ${team}` : "the company"}. Add more on the <Link href="/skills" className="underline underline-offset-4">Skills page</Link>.
            Skills its profile gives stay.
          </SheetDescription>
        </SheetHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-5 py-4">
          <Input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search skills" aria-label="Search skills" className="h-10" />
          {installed.isLoading ? <LoadingRows rows={3} /> : unavailable ? (
            <EmptyState icon={Sparkles} title="Skills are not available on this server yet" />
          ) : installed.error ? <ErrorState error={installed.error} /> : shown.length === 0 ? (
            <EmptyState icon={Sparkles} title="No installed skill fits" hint="Install skills on the Skills page." />
          ) : (
            <ul className="space-y-2">
              {shown.map((s) => {
                const on = chosen.has(s.name);
                return (
                  <li key={s.name}>
                    <button type="button" role="checkbox" aria-checked={on} onClick={() => toggle(s.name)}
                      className={cn("flex w-full items-start gap-3 rounded-xl border p-3 text-left transition-colors", on ? "border-foreground/70 bg-accent/60" : "hover:bg-accent/40")}>
                      <span className={cn("mt-0.5 flex size-5 shrink-0 items-center justify-center rounded border", on && "border-foreground bg-foreground text-background")}>{on && <Check className="size-3.5" />}</span>
                      <span className="min-w-0 flex-1">
                        <span className="flex flex-wrap items-center gap-1.5">
                          <span className="font-mono text-sm font-medium">{s.name}</span>
                          <Badge variant="secondary" className="font-normal">{s.source === "factory" ? "Factory" : s.source}</Badge>
                          {s.scope && <Badge variant="outline" className="font-normal">{scopeLabel(s.scope)}</Badge>}
                        </span>
                        {s.description && <span className="mt-0.5 block text-sm text-muted-foreground">{s.description}</span>}
                        {s.categories?.length ? <span className="mt-1 block text-xs text-muted-foreground">{s.categories.join(" · ")}</span> : null}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
        <div className="border-t px-5 py-3">
          <Button className="h-10 w-full" onClick={save} disabled={busy || !member.document || unavailable}>Save ({chosen.size})</Button>
        </div>
      </SheetContent>
    </Sheet>
  );
}

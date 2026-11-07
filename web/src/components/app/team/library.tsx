"use client";

import { Plus, Search, Users } from "lucide-react";
import { useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useAgentPresets, useTeamPresets, useTeams } from "@/lib/api/client";
import type { TeamView } from "@/lib/api/types-catalog";
import { cn } from "@/lib/utils";
import { AddAgentSheet, NewTeamSheet } from "./builder";

// The library (default-agent-library 4.1): ready teams and agents by field, searchable; a team is created from its
// preset in one step, an agent is added to one of your teams.
const fields = [
  { key: "", label: "All" }, { key: "software", label: "Software" }, { key: "games", label: "Games" },
  { key: "growth", label: "Growth" }, { key: "content", label: "Content" }, { key: "business", label: "Business" },
];

export function PresetLibrary({ manage }: { manage: boolean }) {
  const agents = useAgentPresets();
  const teamsPresets = useTeamPresets();
  const myTeams = useTeams().data?.teams ?? [];
  const [field, setField] = useState("");
  const [q, setQ] = useState("");
  const [newTeam, setNewTeam] = useState<string | null>(null);
  const [adding, setAdding] = useState<{ preset: string; team: TeamView } | null>(null);
  const [pick, setPick] = useState<string | null>(null);
  const match = (m: { title: string; description: string; category?: string; tags?: string[]; examples?: string[] }) =>
    (!field || m.category === field) && (!q.trim() || [m.title, m.description, ...(m.tags ?? []), ...(m.examples ?? [])].join(" ").toLowerCase().includes(q.trim().toLowerCase()));
  const teamCards = useMemo(() => (teamsPresets.data ?? []).filter((t) => match(t.metadata)), [teamsPresets.data, field, q]); // eslint-disable-line react-hooks/exhaustive-deps
  const agentCards = useMemo(() => (agents.data ?? []).filter((a) => match(a.metadata)), [agents.data, field, q]); // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <div className="space-y-5">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
        <div className="relative flex-1">
          <Search aria-hidden className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input type="search" aria-label="Search the library" placeholder="Search teams and agents" value={q} onChange={(e) => setQ(e.target.value)} className="h-10 pl-9" />
        </div>
        <div role="tablist" aria-label="Field" className="flex flex-wrap gap-1.5">
          {fields.map((f) => (
            <button key={f.key} role="tab" aria-selected={field === f.key} onClick={() => setField(f.key)}
              className={cn("h-9 rounded-full border px-3 text-sm", field === f.key ? "border-foreground bg-foreground text-background" : "text-muted-foreground hover:text-foreground")}>{f.label}</button>
          ))}
        </div>
      </div>
      <section>
        <h2 className="mb-2 text-sm font-medium text-muted-foreground">Ready teams</h2>
        <ul className="grid gap-3 md:grid-cols-2">
          {teamCards.map((t) => (
            <li key={t.metadata.name} className="rounded-2xl border bg-card p-4">
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <p className="font-medium">{t.metadata.title}</p>
                  <p className="text-sm text-muted-foreground">{t.metadata.description}</p>
                </div>
                {t.metadata.category && <Badge variant="outline" className="shrink-0 capitalize">{t.metadata.category}</Badge>}
              </div>
              <p className="mt-2 text-xs text-muted-foreground"><Users className="mr-1 inline size-3.5" />{t.spec.agents.map((a) => a.key).join(", ")}</p>
              {manage && <Button className="mt-3 h-9" onClick={() => setNewTeam(t.metadata.name)}><Plus className="size-4" />Create this team</Button>}
            </li>
          ))}
          {teamCards.length === 0 && <li className="text-sm text-muted-foreground">No team matches.</li>}
        </ul>
      </section>
      <section>
        <h2 className="mb-2 text-sm font-medium text-muted-foreground">Agents</h2>
        <ul className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {agentCards.map((a) => (
            <li key={a.metadata.name} className="flex flex-col rounded-2xl border bg-card p-4">
              <div className="flex items-start justify-between gap-2">
                <p className="font-medium">{a.metadata.title}</p>
                {a.metadata.category && <Badge variant="outline" className="shrink-0 capitalize">{a.metadata.category}</Badge>}
              </div>
              <p className="text-sm text-muted-foreground">{a.metadata.description}</p>
              {!!a.metadata.examples?.length && <p className="mt-2 text-xs text-muted-foreground">For example: {a.metadata.examples[0]}</p>}
              {!!a.spec.skills.length && <p className="mt-1 text-xs text-muted-foreground">Skills: {a.spec.skills.join(", ")}</p>}
              {manage && myTeams.length > 0 && (pick === a.metadata.name ? (
                <select autoFocus aria-label="Add to which team" className="mt-3 h-9 rounded-md border bg-background px-2 text-sm" defaultValue=""
                  onChange={(e) => { const t = myTeams.find((x) => x.name === e.target.value); if (t) { setAdding({ preset: a.metadata.name, team: t }); setPick(null); } }}>
                  <option value="" disabled>Add to which team?</option>
                  {myTeams.map((t) => <option key={t.name} value={t.name}>{t.title || t.name}</option>)}
                </select>
              ) : <Button variant="outline" className="mt-3 h-9 self-start" onClick={() => setPick(a.metadata.name)}><Plus className="size-4" />Add to a team</Button>)}
            </li>
          ))}
        </ul>
      </section>
      <NewTeamSheet open={!!newTeam} initialPreset={newTeam ?? undefined} onClose={() => setNewTeam(null)} />
      {adding && <AddAgentSheet team={adding.team} open initialPreset={adding.preset} onClose={() => setAdding(null)} />}
    </div>
  );
}

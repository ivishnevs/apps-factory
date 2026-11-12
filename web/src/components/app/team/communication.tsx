"use client";

import { Check } from "lucide-react";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { TeamMember, TeamView } from "@/lib/api/types-catalog";
import type { AgentSpec } from "@/lib/api/types-presets";
import { cn } from "@/lib/utils";

// Communication on the agent's form (team-page-clarity 2.4): whom it may message and whom it gives tasks to, as
// controls instead of comma text and YAML. Core enforces both; an agent always may message its own team, its
// managers and you (company-teams D6), so those are shown on and fixed.

type Scope = NonNullable<AgentSpec["communication"]>["can_assign_tasks"];
export type ScopeMode = "" | "none" | "direct_reports" | "subtree" | "list";

function Chip({ on, fixed, onClick, children, title }: { on: boolean; fixed?: boolean; onClick?: () => void; children: React.ReactNode; title?: string }) {
  return (
    <button type="button" role="checkbox" aria-checked={on} aria-disabled={fixed} disabled={fixed} title={title} onClick={onClick}
      className={cn("inline-flex h-9 items-center gap-1.5 rounded-full border px-3 text-sm transition-colors",
        on ? "border-primary/50 bg-primary/10 text-foreground" : "text-muted-foreground hover:bg-muted/50",
        fixed && "cursor-default opacity-70")}>
      {on && <Check className="size-3.5" />}{children}
    </button>
  );
}

// MessageTargets edits spec.communication.can_message: the always-allowed targets are on and fixed, agents of other
// teams are toggled. Entries the document already lists for the agent's own team are kept as they are.
export function MessageTargets({ value, onChange, self, team, teams }: { value: string[]; onChange: (v: string[]) => void; self?: string; team: TeamView; teams: TeamView[] }) {
  const always = new Set(["owner", ...team.agents.map((a) => a.name).filter((a) => a !== self)]);
  const others = teams.filter((t) => t.name !== team.name).flatMap((t) => t.agents.map((a) => ({ name: a.name, team: t.title || t.name })));
  const toggle = (n: string) => onChange(value.includes(n) ? value.filter((x) => x !== n) : [...value, n]);
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-1.5">
        <Chip on fixed title="Always: an agent may message you">you</Chip>
        {[...always].filter((n) => n !== "owner").map((n) => <Chip key={n} on fixed title={`Always: ${team.title || team.name} is its team`}>{n}</Chip>)}
        {others.map((o) => <Chip key={o.name} on={value.includes(o.name)} onClick={() => toggle(o.name)} title={`Team ${o.team}`}>{o.name} <span className="text-xs text-muted-foreground">{o.team}</span></Chip>)}
      </div>
      <p className="text-xs text-muted-foreground">
        It may always message you and its own team. {others.length ? "Choose agents of other teams it may message too." : "Other teams' agents appear here once the company has more teams."}
      </p>
    </div>
  );
}

// scopeMode reads can_assign_tasks as the API returns it (true and false are direct_reports and none).
export function scopeMode(s: Scope): ScopeMode {
  if (s === undefined || s === null) return "";
  if (s === true) return "direct_reports";
  if (s === false) return "none";
  if (Array.isArray(s)) return "list";
  return s as ScopeMode;
}

// directReports and subtree are computed from the team's reporting lines, to say whom a scope means now.
export function directReports(agents: TeamMember[], name: string) {
  return agents.filter((a) => a.reports_to === name).map((a) => a.name);
}
export function subtree(agents: TeamMember[], name: string): string[] {
  return directReports(agents, name).flatMap((r) => [r, ...subtree(agents, r)]);
}

const modes: { v: ScopeMode; l: string }[] = [
  { v: "", l: "Default: its direct reports, if any" },
  { v: "none", l: "No one" },
  { v: "direct_reports", l: "Its direct reports" },
  { v: "subtree", l: "Everyone below it" },
  { v: "list", l: "Chosen agents" },
];

// AssignScopeField edits spec.communication.can_assign_tasks: none, direct reports, the whole subtree or chosen
// agents of its team; Core refuses a task outside it and names whom to give it to instead.
export function AssignScopeField({ value, onChange, self, team }: { value: Scope; onChange: (v: Scope) => void; self: string; team: TeamView }) {
  const mode = scopeMode(value);
  const list = Array.isArray(value) ? value : [];
  const reports = directReports(team.agents, self);
  const below = subtree(team.agents, self);
  const now = mode === "" ? reports : mode === "direct_reports" ? reports : mode === "subtree" ? below : mode === "list" ? list : [];
  const set = (m: ScopeMode) => onChange(m === "" ? undefined : m === "list" ? list.length ? list : [] : m);
  const toggle = (n: string) => onChange(list.includes(n) ? list.filter((x) => x !== n) : [...list, n]);
  return (
    <div className="space-y-2">
      <Select value={mode || "_"} onValueChange={(v) => set(v === "_" ? "" : v as ScopeMode)}>
        <SelectTrigger className="w-full"><SelectValue /></SelectTrigger>
        <SelectContent>{modes.map((m) => <SelectItem key={m.l} value={m.v || "_"}>{m.l}</SelectItem>)}</SelectContent>
      </Select>
      {mode === "list" && (
        <div className="flex flex-wrap gap-1.5">
          {team.agents.filter((a) => a.name !== self).map((a) => <Chip key={a.name} on={list.includes(a.name)} onClick={() => toggle(a.name)}>{a.name}</Chip>)}
        </div>
      )}
      <p className="text-xs text-muted-foreground">
        {now.length ? `Now: ${now.join(", ")}.` : "Now: no one."} Core refuses a task for anyone else and names whom to give it to.
        {(mode === "direct_reports" || mode === "subtree") && reports.length === 0 && " No agent reports to it yet."}
      </p>
    </div>
  );
}

"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { core } from "@/lib/api/client";
import type { Project } from "@/lib/api/types";
import type { ApprovalMode, ApprovalModes } from "@/lib/api/types-catalog";

// Who approves (approval-delegation): per kind, people only (a hard gate), a named agent, or the team's lead deciding
// from the owner's earlier answers. The project overrides its team; spending and destructive changes always go to
// people.
type Kind = keyof ApprovalModes;
const kinds: { key: Kind; label: string; hint: string }[] = [
  { key: "internal_artifacts", label: "The team's own material", hint: "Concepts, references, textures and other work the team uses itself" },
  { key: "external_artifacts", label: "What leaves the company", hint: "Store material, publications" },
  { key: "decisions", label: "Decisions", hint: "Project decisions; money and destructive changes always come to you" },
];
const defaults: Record<Kind, ApprovalMode> = { internal_artifacts: { mode: "lead" }, external_artifacts: { mode: "owner" }, decisions: { mode: "lead" } };

export function modeWords(m: ApprovalMode | undefined, lead?: string): string {
  switch (m?.mode) {
    case "owner": return "You (people only)";
    case "agent": return `${m.agent}, escalates to you when unsure`;
    case "lead": return `${lead ?? "The lead"}, from your earlier answers; anything new comes to you`;
  }
  return "";
}

// ApprovalModesView: the effective modes in plain words (read-only), e.g. on the team page.
export function ApprovalModesView({ modes, lead, inherited }: { modes?: ApprovalModes; lead?: string; inherited?: ApprovalModes }) {
  return (
    <dl className="divide-y rounded-xl border text-sm">
      {kinds.map((k) => {
        const own = modes?.[k.key];
        const m = own?.mode ? own : inherited?.[k.key]?.mode ? inherited[k.key] : defaults[k.key];
        return (
          <div key={k.key} className="flex flex-col gap-0.5 px-4 py-2.5 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
            <dt className="text-muted-foreground">{k.label}</dt>
            <dd className="font-medium sm:text-right">{modeWords(m, lead)}{!own?.mode && <span className="ml-1 text-xs font-normal text-muted-foreground">({inherited?.[k.key]?.mode ? "team" : "default"})</span>}</dd>
          </div>
        );
      })}
    </dl>
  );
}

// ProjectApprovals edits a project's modes (empty inherits the team's).
export function ProjectApprovals({ project, team, lead, agents, manage }: { project: Project; team?: ApprovalModes; lead?: string; agents: string[]; manage: boolean }) {
  const qc = useQueryClient();
  const [m, setM] = useState<ApprovalModes>(project.approvals ?? {});
  const [busy, setBusy] = useState(false);
  useEffect(() => setM(project.approvals ?? {}), [project.approvals]);
  const value = (k: Kind) => { const x = m[k]; return !x?.mode ? "" : x.mode === "agent" ? `agent:${x.agent}` : x.mode; };
  const set = (k: Kind, v: string) => setM({ ...m, [k]: v === "" ? { mode: "" } : v.startsWith("agent:") ? { mode: "agent", agent: v.slice(6) } : { mode: v as ApprovalMode["mode"] } });
  const save = async () => {
    setBusy(true);
    try {
      const clean = Object.fromEntries(kinds.map((k) => [k.key, m[k.key]?.mode ? m[k.key] : { mode: "" }]));
      await core(`/projects/${encodeURIComponent(project.id)}`, { method: "PATCH", body: JSON.stringify({ version: project.version, name: project.name, description: project.description, approvals: clean }) });
      await qc.invalidateQueries({ queryKey: ["overview"] });
      toast.success("Saved");
    } catch (e) { toast.error(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); }
  };
  return (
    <section aria-label="Who approves" className="rounded-2xl border bg-card p-4 sm:p-5">
      <h2 className="font-medium">Who approves</h2>
      <p className="mt-1 text-sm text-muted-foreground">Who may approve the work of this project. Spending and destructive changes always come to you.</p>
      <div className="mt-3 space-y-3">
        {kinds.map((k) => {
          const inherited = team?.[k.key]?.mode ? team[k.key]! : defaults[k.key];
          return (
            <label key={k.key} className="block space-y-1.5 text-sm">
              <span className="font-medium">{k.label}</span>
              <select className="h-10 w-full rounded-md border bg-background px-2" disabled={!manage} value={value(k.key)} onChange={(e) => set(k.key, e.target.value)}>
                <option value="">As the team: {modeWords(inherited, lead)}</option>
                <option value="owner">You (people only, a hard gate)</option>
                <option value="lead">{lead ?? "The lead"}, from your earlier answers</option>
                {agents.map((a) => <option key={a} value={`agent:${a}`}>{a}, escalates when unsure</option>)}
              </select>
              <span className="block text-xs text-muted-foreground">{k.hint}</span>
            </label>
          );
        })}
      </div>
      {manage && <div className="mt-3 flex justify-end"><Button className="h-10" disabled={busy} onClick={save}>Save</Button></div>}
    </section>
  );
}

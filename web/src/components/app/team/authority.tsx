"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { Button } from "@/components/ui/button";
import { core, useAgentDoc } from "@/lib/api/client";

// LeadAuthority (agent-company-authority 2.2): what the team's lead may change in the company, in plain words, and
// the owner's switch for company-wide changes. Every change still waits for a person's approval.
export function LeadAuthority({ lead, team, owner }: { lead: string; team: string; owner: boolean }) {
  const doc = useAgentDoc(lead);
  const qc = useQueryClient();
  const confirm = useConfirm();
  const [busy, setBusy] = useState(false);
  const company = !!(doc.data?.agent.spec.authority as { company?: boolean } | undefined)?.company;
  const toggle = async () => {
    if (!doc.data) return;
    const to = !company;
    if (!(await confirm({ title: to ? `Let ${lead} change the whole company?` : `Limit ${lead} to team ${team}?`,
      description: to ? `${lead} may then propose changes to other teams and company settings (skills, subscriptions, node hooks). Each change still waits for an owner's or admin's approval; deleting teams or agents waits for an owner.` : `${lead} may then change only team ${team}.`,
      confirm: to ? "Grant" : "Revoke" }))) return;
    setBusy(true);
    try {
      const agent = structuredClone(doc.data.agent);
      agent.spec.authority = { ...(agent.spec.authority as object | undefined), company: to };
      await core(`/catalog/agents/${encodeURIComponent(lead)}`, { method: "PUT", body: JSON.stringify({ base: doc.data.base, agent, prompt: doc.data.prompt }) });
      await qc.invalidateQueries();
      toast.success(to ? `${lead} may change the company` : `${lead} may change its team only`);
    } catch (e) { toast.error(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); }
  };
  return (
    <div className="mt-3 space-y-1 border-t pt-3 text-xs text-muted-foreground">
      <p><span className="font-medium text-foreground">May change:</span> team {team} (its agents, prompts and settings), each change with your approval.</p>
      <p><span className="font-medium text-foreground">Other teams and company settings:</span> {company ? "yes, granted by an owner" : "no"}.</p>
      <p>Deleting a team or an agent always waits for an owner. Other agents ask {lead}.</p>
      {owner && doc.data && <Button variant="outline" size="sm" className="mt-2 h-9" disabled={busy} onClick={toggle}>{company ? "Limit to this team" : "Allow company changes"}</Button>}
    </div>
  );
}

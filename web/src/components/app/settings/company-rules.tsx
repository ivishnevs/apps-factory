"use client";

import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { ErrorState, LoadingRows } from "@/components/app/page";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { core } from "@/lib/api/client";

// Company rules (company-rules-web): the standing rules every agent's brief carries in every run, for what applies
// to all agents of the company. A role's own craft stays in its prompt; a project's facts go to the project's rules.
const MaxBytes = 8 * 1024;

export function CompanyRules({ manage }: { manage: boolean }) {
  const q = useQuery({ queryKey: ["company-rules"], queryFn: () => core<{ rules: string }>("/settings/company") });
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (q.data) setText(q.data.rules ?? ""); }, [q.data]);
  if (q.error) return <ErrorState error={q.error} />;
  if (!q.data) return <LoadingRows rows={2} />;
  const size = new TextEncoder().encode(text).length;
  const dirty = text !== (q.data.rules ?? "");
  const save = async () => {
    setBusy(true);
    try {
      await core("/settings/company", { method: "PUT", body: JSON.stringify({ rules: text }) });
      await q.refetch();
      toast.success("Company rules saved");
    } catch (e) { toast.error(e instanceof Error ? e.message : String(e)); } finally { setBusy(false); }
  };
  return (
    <div className="space-y-3">
      <Textarea aria-label="Company rules" value={text} onChange={(e) => setText(e.target.value)} disabled={!manage} rows={14}
        className="font-mono text-sm" placeholder={"Write to the board in Russian.\nAsk once per task, with options."} />
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className={size > MaxBytes ? "text-xs text-red-700 dark:text-red-300" : "text-xs text-muted-foreground"}>
          {(size / 1024).toFixed(1)} of 8 KB · every agent reads these in every run, so keep them to what applies to all agents
        </p>
        {manage && <Button className="h-10" disabled={!dirty || busy || size > MaxBytes} onClick={save}>Save</Button>}
      </div>
      {!manage && <p className="text-xs text-muted-foreground">Owners and admins change the company rules.</p>}
    </div>
  );
}

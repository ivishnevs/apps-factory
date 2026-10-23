"use client";

import { useEffect, useState } from "react";
import { Segmented } from "@/components/app/costs/segmented";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { scopeName } from "@/lib/api/types-ops";

export type Scope = "project" | "agent" | "node" | "quota_key";
export interface PauseRequest { scope: Scope; target: string; reason: string }

const scopes: { value: Scope; label: string }[] = [
  { value: "project", label: "Project" }, { value: "agent", label: "Agent" }, { value: "node", label: "Node" }, { value: "quota_key", label: "Quota" },
];
const hints: Record<Scope, string> = {
  project: "No new runs start in the project.",
  agent: "The agent starts no new runs.",
  node: "The node claims no new runs.",
  quota_key: "The agents sharing this subscription start no new runs.",
};

// PauseDialog picks what to pause (a project, agent, node or quota key) and why; live runs continue.
export function PauseDialog({ open, onOpenChange, initial, options, onSubmit }: {
  open: boolean; onOpenChange: (o: boolean) => void; initial?: Partial<PauseRequest>;
  options: Record<Exclude<Scope, "quota_key">, { value: string; label: string }[]>; onSubmit: (r: PauseRequest) => void;
}) {
  const [scope, setScope] = useState<Scope>("project");
  const [target, setTarget] = useState("");
  const [reason, setReason] = useState("");
  useEffect(() => {
    if (open) { setScope(initial?.scope ?? "project"); setTarget(initial?.target ?? ""); setReason(""); }
  }, [open, initial?.scope, initial?.target]);
  const list = scope === "quota_key" ? [] : options[scope];
  const fixed = !!initial?.target;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={(e) => { e.preventDefault(); if (target.trim()) onSubmit({ scope, target: target.trim(), reason: reason.trim() }); }}>
          <DialogHeader>
            <DialogTitle>{fixed ? `Pause ${scopeName(scope)} ${list.find((o) => o.value === target)?.label ?? target}` : "Pause"}</DialogTitle>
            <DialogDescription>{hints[scope]} Live runs continue; new runs wait until you resume.</DialogDescription>
          </DialogHeader>
          <div className="mt-5 space-y-4">
            {!fixed && (
              <>
                <Segmented label="What to pause" value={scope} onChange={(s) => { setScope(s); setTarget(""); }} options={scopes} className="w-full [&>button]:flex-1" />
                <div className="space-y-1.5">
                  <label className="text-sm font-medium" htmlFor="pause-target">{scope === "quota_key" ? "Quota key" : `Which ${scope}`}</label>
                  {scope === "quota_key" ? (
                    <Input id="pause-target" className="h-10" value={target} onChange={(e) => setTarget(e.target.value)} placeholder="e.g. claude-max" autoComplete="off" />
                  ) : (
                    <Select value={target} onValueChange={setTarget}>
                      <SelectTrigger id="pause-target" className="h-10 w-full"><SelectValue placeholder={`Choose a ${scope}`} /></SelectTrigger>
                      <SelectContent position="popper">
                        {list.length === 0 && <div className="px-2 py-1.5 text-sm text-muted-foreground">None</div>}
                        {list.map((o) => <SelectItem key={o.value} value={o.value}>{o.label}</SelectItem>)}
                      </SelectContent>
                    </Select>
                  )}
                </div>
              </>
            )}
            <div className="space-y-1.5">
              <label className="text-sm font-medium" htmlFor="pause-reason">Reason <span className="font-normal text-muted-foreground">(shown on the dashboard)</span></label>
              <Input id="pause-reason" className="h-10" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="e.g. waiting for the style guide" autoComplete="off" />
            </div>
          </div>
          <DialogFooter className="mt-6 gap-2">
            <Button type="button" variant="outline" className="h-10" onClick={() => onOpenChange(false)}>Cancel</Button>
            <Button type="submit" className="h-10" disabled={!target.trim()}>Continue</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

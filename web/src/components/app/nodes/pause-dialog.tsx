"use client";

import { useEffect, useState } from "react";
import { Segmented } from "@/components/app/costs/segmented";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { scopeName } from "@/lib/api/types-ops";
import { useQuery } from "@tanstack/react-query";
import { core } from "@/lib/api/client";
import type { NodeSessions } from "@/lib/api/types";

export type Scope = "project" | "agent" | "node" | "quota_key";
// sessions (node pause): what happens to the agent sessions the node holds (node-readiness 4.1).
export interface PauseRequest { scope: Scope; target: string; reason: string; sessions?: "keep" | "release" }

const scopes: { value: Scope; label: string }[] = [
  { value: "project", label: "Project" }, { value: "agent", label: "Agent" }, { value: "node", label: "Node" }, { value: "quota_key", label: "Subscription" },
];
const hints: Record<Scope, string> = {
  project: "No new runs start in the project.",
  agent: "The agent starts no new runs.",
  node: "The node takes no new work.",
  quota_key: "The agents sharing this subscription start no new work.",
};

// PauseDialog picks what to pause (a project, agent, node or quota key) and why; live runs continue.
export function PauseDialog({ open, onOpenChange, initial, options, onSubmit }: {
  open: boolean; onOpenChange: (o: boolean) => void; initial?: Partial<PauseRequest>;
  options: Record<Exclude<Scope, "quota_key">, { value: string; label: string }[]>; onSubmit: (r: PauseRequest) => void;
}) {
  const [scope, setScope] = useState<Scope>("project");
  const [target, setTarget] = useState("");
  const [reason, setReason] = useState("");
  const [sessions, setSessions] = useState<"keep" | "release">("keep");
  useEffect(() => {
    if (open) { setScope(initial?.scope ?? "project"); setTarget(initial?.target ?? ""); setReason(""); setSessions("keep"); }
  }, [open, initial?.scope, initial?.target]);
  const list = scope === "quota_key" ? [] : options[scope];
  const fixed = !!initial?.target;
  // A node's held sessions and the runs waiting for them: pausing asks whether to keep or release them.
  const held = useQuery({ queryKey: ["node-sessions", target], queryFn: () => core<NodeSessions>(`/nodes/${encodeURIComponent(target)}/sessions`),
    enabled: open && scope === "node" && !!target, retry: false });
  const waiting = held.data?.waiting ?? [];
  const keepUntil = held.data ? new Date(Date.now() + held.data.release_seconds * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "";
  const asks = scope === "node" && ((held.data?.held.length ?? 0) > 0 || waiting.length > 0);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={(e) => { e.preventDefault(); if (target.trim()) onSubmit({ scope, target: target.trim(), reason: reason.trim(), sessions: asks ? sessions : undefined }); }}>
          <DialogHeader>
            <DialogTitle>{fixed ? `Pause ${scopeName(scope)} ${list.find((o) => o.value === target)?.label ?? target}` : "Pause"}</DialogTitle>
            <DialogDescription>{hints[scope]} Work in progress continues; new work waits until you resume.</DialogDescription>
          </DialogHeader>
          <div className="mt-5 space-y-4">
            {!fixed && (
              <>
                <Segmented label="What to pause" value={scope} onChange={(s) => { setScope(s); setTarget(""); }} options={scopes} className="w-full [&>button]:flex-1" />
                <div className="space-y-1.5">
                  <label className="text-sm font-medium" htmlFor="pause-target">{scope === "quota_key" ? "Subscription (quota key)" : `Which ${scope === "node" ? "node" : scope}`}</label>
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
            {asks && held.data && (
              <div className="space-y-2 rounded-lg border p-3 text-sm">
                <p className="font-medium">This node holds {held.data.held.length} agent session{held.data.held.length === 1 ? "" : "s"}</p>
                {waiting.length > 0 ? (
                  <ul className="space-y-0.5 text-muted-foreground">
                    {waiting.map((w) => <li key={w.run_id}><span className="font-mono">{w.task_id || w.run_id}</span> · {w.agent} waits for it</li>)}
                  </ul>
                ) : <p className="text-muted-foreground">No run waits for them now.</p>}
                <Segmented label="Sessions" value={sessions} onChange={setSessions} className="w-full [&>button]:flex-1"
                  options={[{ value: "keep", label: `Keep until ${keepUntil}` }, { value: "release", label: "Release now" }]} />
                <p className="text-xs text-muted-foreground">
                  {sessions === "keep" ? "For a short pause: waiting runs stay with this node until then, then go to another node with a fresh session."
                    : "Waiting runs go to another node now and start a fresh session from Factory's context."}
                </p>
              </div>
            )}
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

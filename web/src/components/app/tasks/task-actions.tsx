"use client";

import { ArrowUpDown, Ban, Megaphone, MoreHorizontal, Pause, Play, RotateCcw, UserRoundPen } from "lucide-react";
import { Fragment, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import type { Action, Agent, Task } from "@/lib/api/types";
import type { BoardTask } from "@/lib/api/types-tasks";
import { cn } from "@/lib/utils";
import { Field, touchInput } from "./field";
import { isTerminal } from "./model";
import { useOwnerAction } from "./use-owner-action";

type Kind = "pause" | "resume" | "retry" | "run" | "cancel" | "reassign" | "reprioritize" | "direct";
interface Def { kind: Kind; label: string; icon: React.ElementType; destructive?: boolean; input?: boolean }
const defs: Record<Kind, Def> = {
  run: { kind: "run", label: "Run now", icon: Play },
  retry: { kind: "retry", label: "Retry", icon: RotateCcw },
  pause: { kind: "pause", label: "Pause", icon: Pause },
  resume: { kind: "resume", label: "Resume", icon: Play },
  direct: { kind: "direct", label: "Direct", icon: Megaphone, input: true },
  reassign: { kind: "reassign", label: "Reassign", icon: UserRoundPen, input: true },
  reprioritize: { kind: "reprioritize", label: "Change priority", icon: ArrowUpDown, input: true },
  cancel: { kind: "cancel", label: "Cancel task", icon: Ban, destructive: true, input: true },
};

// The owner's actions on a task, in the order the menu lists them, and the one worth a primary button.
function available(t: BoardTask, failed: boolean): { all: Kind[]; primary?: Kind } {
  if (isTerminal(t.status)) return { all: [] };
  const all: Kind[] = [];
  if (t.status === "BACKLOG" || t.status === "READY") all.push("run");
  if (t.status !== "BACKLOG") all.push("retry");
  all.push(t.paused_by ? "resume" : "pause", "direct", "reassign", "reprioritize", "cancel");
  const primary: Kind | undefined = t.paused_by ? "resume" : t.status === "BACKLOG" ? "run" : t.status === "BLOCKED" || t.status === "REWORK" || failed ? "retry" : undefined;
  return { all, primary };
}

const titles: Record<Kind, string> = {
  run: "Run this task now?", retry: "Retry this task?", pause: "Pause this task?", resume: "Resume this task?", direct: "Send this directive?",
  reassign: "Reassign this task?", reprioritize: "Change the priority?", cancel: "Cancel this task?",
};

export function TaskActions({ task, agents, hasOpenChildren, failed }: { task: BoardTask; agents: Agent[]; hasOpenChildren: boolean; failed: boolean }) {
  const { run, pending } = useOwnerAction();
  const [dialog, setDialog] = useState<Kind | null>(null);
  const { all, primary } = available(task, failed);
  if (all.length === 0) return null;

  const start = (k: Kind) => {
    if (defs[k].input) { setDialog(k); return; }
    const a: Action = { Kind: k, TaskID: task.id, Version: task.version };
    if (k === "pause") a.Text = "paused by owner";
    if (k === "resume") a.Text = "resumed by owner";
    void run(a, { title: titles[k], confirm: defs[k].label });
  };
  const submit = async (a: Action) => {
    setDialog(null);
    await run(a, { title: titles[a.Kind as Kind], confirm: a.Kind === "cancel" ? "Cancel task" : defs[a.Kind as Kind].label, destructive: a.Kind === "cancel" });
  };
  const lead = primary ?? "direct";
  const rest = all.filter((k) => k !== lead);
  const Lead = defs[lead];

  const menu = (trigger: React.ReactNode) => (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>{trigger}</DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        {rest.map((k) => (
          <Fragment key={k}>
            {k === "cancel" && <DropdownMenuSeparator />}
            <DropdownMenuItem onSelect={() => start(k)} variant={defs[k].destructive ? "destructive" : "default"} className="min-h-10">
              {(() => { const I = defs[k].icon; return <I />; })()}{defs[k].label}{defs[k].input && "…"}
            </DropdownMenuItem>
          </Fragment>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );

  return (
    <>
      {/* Desktop: in the header. */}
      <div className="hidden items-center gap-2 md:flex">
        <Button onClick={() => start(lead)} disabled={pending}><Lead.icon />{Lead.label}{Lead.input && "…"}</Button>
        {menu(<Button variant="outline" size="icon" aria-label="More actions"><MoreHorizontal /></Button>)}
      </div>
      {/* Phone: a bar above the tab bar. */}
      <div className="fixed inset-x-0 bottom-[calc(3.5rem+env(safe-area-inset-bottom))] z-30 border-t bg-background/90 px-4 py-2.5 backdrop-blur md:hidden">
        <div className="flex gap-2">
          <Button className="h-11 flex-1" onClick={() => start(lead)} disabled={pending}><Lead.icon />{Lead.label}</Button>
          {menu(<Button variant="outline" className="h-11 px-4" aria-label="More actions"><MoreHorizontal />More</Button>)}
        </div>
      </div>
      <InputDialog kind={dialog} task={task} agents={agents} hasOpenChildren={hasOpenChildren} onClose={() => setDialog(null)} onSubmit={submit} />
    </>
  );
}

function InputDialog({ kind, task, agents, hasOpenChildren, onClose, onSubmit }: {
  kind: Kind | null; task: Task; agents: Agent[]; hasOpenChildren: boolean; onClose: () => void; onSubmit: (a: Action) => void;
}) {
  return (
    <Dialog open={!!kind} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        {kind && <InputForm key={kind} kind={kind} task={task} agents={agents} hasOpenChildren={hasOpenChildren} onClose={onClose} onSubmit={onSubmit} />}
      </DialogContent>
    </Dialog>
  );
}

function InputForm({ kind, task, agents, hasOpenChildren, onClose, onSubmit }: {
  kind: Kind; task: Task; agents: Agent[]; hasOpenChildren: boolean; onClose: () => void; onSubmit: (a: Action) => void;
}) {
  const [text, setText] = useState("");
  const [urgent, setUrgent] = useState(false);
  const [cascade, setCascade] = useState(hasOpenChildren);
  const [assignee, setAssignee] = useState(task.assignee || "");
  const [priority, setPriority] = useState(String(task.priority));
  const [error, setError] = useState("");
  const base: Action = { Kind: kind, TaskID: task.id, Version: task.version };

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (kind === "direct") {
      if (!text.trim()) return setError("Write what the assignee should do");
      onSubmit({ ...base, Text: text.trim(), Urgent: urgent });
    } else if (kind === "reassign") {
      if (!assignee) return setError("Choose an agent");
      if (assignee === task.assignee) return setError(`${task.id} is already assigned to ${assignee}`);
      onSubmit({ ...base, Assignee: assignee });
    } else if (kind === "reprioritize") {
      const p = Number(priority.trim());
      if (!Number.isInteger(p) || priority.trim() === "") return setError("A whole number");
      if (p < -1000 || p > 1000) return setError("Between -1000 and 1000");
      onSubmit({ ...base, Priority: p });
    } else if (kind === "cancel") {
      onSubmit({ ...base, Text: text.trim() || undefined, Cascade: cascade });
    }
  };

  const head: Record<string, [string, string]> = {
    direct: ["Direct the assignee", `A directive reaches ${task.assignee || "the assignee"} on their next run of ${task.id}; urgent ones interrupt the current run.`],
    reassign: ["Reassign", `Who should work on ${task.id}?`],
    reprioritize: ["Change priority", "Higher runs first. The board orders by it."],
    cancel: ["Cancel task", `${task.id} stops for good; a live run is revoked.`],
  };
  return (
    <form onSubmit={submit} className="grid gap-4" noValidate>
      <DialogHeader>
        <DialogTitle>{head[kind][0]}</DialogTitle>
        <DialogDescription>{head[kind][1]}</DialogDescription>
      </DialogHeader>
      {kind === "direct" && (
        <>
          <Field id="act-text" label="Directive" error={error}>
            <Textarea id="act-text" autoFocus rows={4} value={text} onChange={(e) => { setText(e.target.value); setError(""); }}
              placeholder="Keep option A only; skip the export for now." className="text-base md:text-sm" />
          </Field>
          <label htmlFor="act-urgent" className="flex cursor-pointer items-center justify-between gap-4 rounded-lg border p-3">
            <span><span className="block text-sm font-medium">Urgent</span><span className="block text-xs text-muted-foreground">Interrupt the run in progress.</span></span>
            <Switch id="act-urgent" checked={urgent} onCheckedChange={setUrgent} />
          </label>
        </>
      )}
      {kind === "reassign" && (
        <Field id="act-assignee" label="Assignee" error={error}>
          <Select value={assignee} onValueChange={(v) => { setAssignee(v); setError(""); }}>
            <SelectTrigger id="act-assignee" className="w-full data-[size=default]:h-11 md:data-[size=default]:h-9"><SelectValue placeholder="Choose an agent" /></SelectTrigger>
            <SelectContent position="popper">
              {agents.map((a) => <SelectItem key={a.name} value={a.name}>{a.name}{a.role && <span className="text-muted-foreground"> · {a.role}</span>}</SelectItem>)}
            </SelectContent>
          </Select>
        </Field>
      )}
      {kind === "reprioritize" && (
        <Field id="act-priority" label="Priority" hint={`Now ${task.priority}.`} error={error}>
          <Input id="act-priority" autoFocus inputMode="numeric" value={priority} onChange={(e) => { setPriority(e.target.value); setError(""); }} className={touchInput} />
        </Field>
      )}
      {kind === "cancel" && (
        <>
          <Field id="act-reason" label="Reason" hint="Optional; the team sees it.">
            <Input id="act-reason" value={text} onChange={(e) => setText(e.target.value)} placeholder="No longer needed" className={touchInput} />
          </Field>
          <label htmlFor="act-cascade" className={cn("flex cursor-pointer items-center justify-between gap-4 rounded-lg border p-3", !hasOpenChildren && "opacity-60")}>
            <span><span className="block text-sm font-medium">Cancel open subtasks too</span>
              <span className="block text-xs text-muted-foreground">{hasOpenChildren ? "Its unfinished children stop as well." : "It has no open subtasks."}</span></span>
            <Switch id="act-cascade" checked={cascade} onCheckedChange={setCascade} disabled={!hasOpenChildren} />
          </label>
        </>
      )}
      <DialogFooter className="gap-2">
        <Button type="button" variant="outline" className="h-10" onClick={onClose}>Back</Button>
        <Button type="submit" className="h-10" variant={kind === "cancel" ? "destructive" : "default"}>Continue</Button>
      </DialogFooter>
    </form>
  );
}

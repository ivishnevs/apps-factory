"use client";

import { ArrowUpDown, Ban, CheckCheck, Eye, GitPullRequest, Megaphone, MoreHorizontal, Pause, Play, RotateCcw, Undo2, UserRoundPen } from "lucide-react";
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
import { ReviewerSelect, reviewerName, useReviewSuggestion } from "./reviewer";
import { assigneeName, displayName, isPerson, PERSON, personSteps, useMembers, useMovesPersonTask } from "./people";

type Kind = "pause" | "resume" | "retry" | "run" | "accept" | "rework" | "cancel" | "reassign" | "reprioritize" | "direct" | "reviewer"
  | "work_start" | "work_submit" | "work_done";
interface Def { kind: Kind; label: string; icon: React.ElementType; destructive?: boolean; input?: boolean }
const defs: Record<Kind, Def> = {
  run: { kind: "run", label: "Run now", icon: Play },
  accept: { kind: "accept", label: "Accept", icon: CheckCheck },
  rework: { kind: "rework", label: "Send back", icon: Undo2, input: true },
  retry: { kind: "retry", label: "Retry", icon: RotateCcw },
  pause: { kind: "pause", label: "Pause", icon: Pause },
  resume: { kind: "resume", label: "Resume", icon: Play },
  direct: { kind: "direct", label: "Direct", icon: Megaphone, input: true },
  reassign: { kind: "reassign", label: "Reassign", icon: UserRoundPen, input: true },
  reprioritize: { kind: "reprioritize", label: "Change priority", icon: ArrowUpDown, input: true },
  reviewer: { kind: "reviewer", label: "Change reviewer", icon: Eye, input: true },
  cancel: { kind: "cancel", label: "Cancel task", icon: Ban, destructive: true, input: true },
  work_start: { kind: "work_start", label: "Start", icon: Play },
  work_submit: { kind: "work_submit", label: "Submit for review", icon: GitPullRequest, input: true },
  work_done: { kind: "work_done", label: "Mark done", icon: CheckCheck },
};

// The owner's actions on a task, in the order the menu lists them, and the one worth a primary button. A person's
// task (people-assignees) has no runs: no retry or directive; its person (or an owner) moves it instead.
function available(t: BoardTask, failed: boolean, moves: boolean): { all: Kind[]; primary?: Kind } {
  if (isTerminal(t.status)) return { all: [] };
  const person = isPerson(t.assignee);
  const all: Kind[] = [];
  const steps = moves ? personSteps(t) : [];
  all.push(...steps);
  if (t.status === "BACKLOG" || t.status === "READY" && !person) all.push("run");
  // Work in review with no agent reviewer waits for a person: accept it or send it back.
  const yours = t.status === "IN_REVIEW" && !t.reviewer;
  if (yours) all.push("accept", "rework");
  if (t.status !== "BACKLOG" && !person) all.push("retry");
  all.push(t.paused_by ? "resume" : "pause");
  if (!person) all.push("direct");
  all.push("reassign");
  if (["BACKLOG", "READY", "REWORK", "BLOCKED"].includes(t.status)) all.push("reviewer");
  all.push("reprioritize", "cancel");
  const primary: Kind | undefined = yours ? "accept" : steps[0] ?? (t.paused_by ? "resume" : t.status === "BACKLOG" ? "run" : !person && (t.status === "BLOCKED" || t.status === "REWORK" || failed) ? "retry" : person ? "reassign" : undefined);
  return { all, primary };
}

const titles: Record<Kind, string> = {
  run: "Run this task now?", accept: "Accept this work?", rework: "Send it back?", retry: "Retry this task?", pause: "Pause this task?", resume: "Resume this task?", direct: "Send this directive?",
  reassign: "Reassign this task?", reprioritize: "Change the priority?", cancel: "Cancel this task?", reviewer: "Change the reviewer?",
  work_start: "Start this task?", work_submit: "Submit it for review?", work_done: "Is the pull request merged?",
};

// explain: what an action does to this task, in words, for its confirmation.
function explain(a: Action, t: Task, nameOf: (assignee?: string) => string = (x) => x ?? ""): string {
  const name = `“${t.title}”`;
  switch (a.Kind) {
    case "run": return `${name} is released to the team and starts as soon as ${t.assignee || "an agent"} is free.${a.ReviewBy ? ` ${reviewerName(a.ReviewBy)} review${a.ReviewBy === "owner" ? "" : "s"} its work.` : ""}`;
    case "retry": return `${name} is released to run again.${a.ReviewBy ? ` ${reviewerName(a.ReviewBy)} review${a.ReviewBy === "owner" ? "" : "s"} its work.` : ""}`;
    case "reviewer": return `${reviewerName(a.ReviewBy)} review${a.ReviewBy === "owner" ? "" : "s"} the work of ${name}.`;
    case "accept": return `${name} is done. ${t.assignee || "The assignee"}'s submission is accepted.`;
    case "rework": return `${name} goes back to ${t.assignee || "the assignee"} with your feedback: ${a.Text}`;
    case "pause": return `No new runs of ${name} start until you resume it. A run in progress continues.`;
    case "resume": return `${name} can run again.`;
    case "direct": return `${t.assignee || "The assignee"} gets this directive${a.Urgent ? " now, interrupting the current run" : " on the next run"}: ${a.Text}`;
    case "reassign": return isPerson(a.Assignee)
      ? `${name} moves to ${nameOf(a.Assignee)}. No agent works on it: they start it, submit it with their pull request and mark it done after the review; tasks that depend on it wait for them.`
      : `${name} moves to ${a.Assignee}.`;
    case "work_start": return `${name} is in progress. Agents leave it to its person.`;
    case "work_submit": return `${name} goes to ${reviewerName(t.review_by) || "its reviewer"} for review with pull request ${a.PullRequest || "(none)"}.`;
    case "work_done": {
      const pr = t.submission?.pull_request;
      return `${name} is done${pr?.number ? ` with pull request #${pr.number} merged` : ""}; the tasks that wait for it start.`;
    }
    case "reprioritize": return `${name} gets priority ${a.Priority}.`;
    case "cancel": return a.Cascade ? `${name} and its open subtasks stop for good.` : `${name} stops for good.`;
  }
  return name;
}

export function TaskActions({ task, agents, hasOpenChildren, failed }: { task: BoardTask; agents: Agent[]; hasOpenChildren: boolean; failed: boolean }) {
  const { run, pending } = useOwnerAction();
  const [dialog, setDialog] = useState<Kind | null>(null);
  // A release needs the task's reviewer (review-coverage): without one, Run now / Retry ask for it first.
  const [releaseKind, setReleaseKind] = useState<Kind | null>(null);
  const moves = useMovesPersonTask(task);
  const people = useMembers().data;
  const nameOf = (x?: string) => assigneeName(x, people);
  const { all, primary } = available(task, failed, moves);
  if (all.length === 0) return null;

  const start = (k: Kind) => {
    if ((k === "run" || k === "retry") && !task.review_by) { setReleaseKind(k); setDialog("reviewer"); return; }
    if (defs[k].input) { setDialog(k); return; }
    const a: Action = { Kind: k, TaskID: task.id, Version: task.version };
    if (k === "pause") a.Text = "paused by owner";
    if (k === "resume") a.Text = "resumed by owner";
    if (k === "work_done") a.Merged = true;
    void run(a, { title: titles[k], confirm: k === "work_done" ? "Merged: mark done" : defs[k].label, description: explain(a, task, nameOf) });
  };
  const submit = async (a: Action) => {
    setDialog(null);
    if (a.Kind === "reviewer" && releaseKind) {
      const k = releaseKind;
      setReleaseKind(null);
      const rel: Action = { Kind: k, TaskID: task.id, Version: task.version, ReviewBy: a.ReviewBy };
      await run(rel, { title: titles[k], confirm: defs[k].label, description: explain(rel, task) });
      return;
    }
    await run(a, { title: titles[a.Kind as Kind], confirm: a.Kind === "cancel" ? "Cancel task" : defs[a.Kind as Kind].label, destructive: a.Kind === "cancel", description: explain(a, task, nameOf) });
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
      <div className="hidden shrink-0 items-center gap-2 md:flex">
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
      <InputDialog kind={dialog} task={task} agents={agents} hasOpenChildren={hasOpenChildren} release={!!releaseKind} onClose={() => { setDialog(null); setReleaseKind(null); }} onSubmit={submit} />
    </>
  );
}

function InputDialog({ kind, task, agents, hasOpenChildren, release, onClose, onSubmit }: {
  kind: Kind | null; task: Task; agents: Agent[]; hasOpenChildren: boolean; release?: boolean; onClose: () => void; onSubmit: (a: Action) => void;
}) {
  return (
    <Dialog open={!!kind} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        {kind && <InputForm key={kind} kind={kind} task={task} agents={agents} hasOpenChildren={hasOpenChildren} release={release} onClose={onClose} onSubmit={onSubmit} />}
      </DialogContent>
    </Dialog>
  );
}

function InputForm({ kind, task, agents, hasOpenChildren, release, onClose, onSubmit }: {
  kind: Kind; task: Task; agents: Agent[]; hasOpenChildren: boolean; release?: boolean; onClose: () => void; onSubmit: (a: Action) => void;
}) {
  const suggestion = useReviewSuggestion(kind === "reviewer" ? task.assignee : "").data?.review_by;
  const [reviewBy, setReviewBy] = useState(task.review_by ?? "");
  const shownReviewer = reviewBy || suggestion || "";
  const [text, setText] = useState("");
  const [urgent, setUrgent] = useState(false);
  const [cascade, setCascade] = useState(hasOpenChildren);
  const [assignee, setAssignee] = useState(task.assignee || "");
  const [priority, setPriority] = useState(String(task.priority));
  const [pr, setPR] = useState(task.submission?.pull_request?.url ?? (task.submission?.pull_request?.number ? `#${task.submission.pull_request.number}` : ""));
  const [error, setError] = useState("");
  const people = useMembers().data ?? [];
  const base: Action = { Kind: kind, TaskID: task.id, Version: task.version };

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (kind === "rework") {
      if (!text.trim()) return setError("Write what should change");
      onSubmit({ ...base, Text: text.trim() });
    } else if (kind === "direct") {
      if (!text.trim()) return setError("Write what the assignee should do");
      onSubmit({ ...base, Text: text.trim(), Urgent: urgent });
    } else if (kind === "reassign") {
      if (!assignee) return setError("Choose an agent or a person");
      if (assignee === task.assignee) return setError(`Already assigned to ${assigneeName(assignee, people)}`);
      onSubmit({ ...base, Assignee: assignee });
    } else if (kind === "work_submit") {
      if (!text.trim()) return setError("Write what was done");
      onSubmit({ ...base, Text: text.trim(), PullRequest: pr.trim() });
    } else if (kind === "reprioritize") {
      const p = Number(priority.trim());
      if (!Number.isInteger(p) || priority.trim() === "") return setError("A whole number");
      if (p < -1000 || p > 1000) return setError("Between -1000 and 1000");
      onSubmit({ ...base, Priority: p });
    } else if (kind === "cancel") {
      onSubmit({ ...base, Text: text.trim() || undefined, Cascade: cascade });
    } else if (kind === "reviewer") {
      if (!shownReviewer) return setError("Reviewer is required");
      if (shownReviewer === task.assignee) return setError("Someone other than the assignee");
      onSubmit({ ...base, ReviewBy: shownReviewer });
    }
  };

  const head: Record<string, [string, string]> = {
    rework: ["Send back for rework", `${task.assignee || "The assignee"} gets the task back with what should change.`],
    direct: ["Direct the assignee", `${task.assignee || "The assignee"} gets it on their next run of this task; an urgent one interrupts the current run.`],
    reassign: ["Reassign", "Who should work on this task? A person does it themselves; agents leave it to them."],
    work_submit: ["Submit for review", `${reviewerName(task.review_by) || "Its reviewer"} reviews it. After the approval, merge the pull request and mark the task done.`],
    reprioritize: ["Change priority", "Higher runs first. The board orders by it."],
    cancel: ["Cancel task", "The task stops for good and a run in progress is stopped."],
    reviewer: [release ? "Choose its reviewer" : "Change reviewer", release ? "Reviewer is required: who checks the work before it is done?" : "Who checks the work before it is done?"],
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
      {kind === "rework" && (
        <Field id="act-feedback" label="What should change" error={error}>
          <Textarea id="act-feedback" autoFocus rows={4} value={text} onChange={(e) => { setText(e.target.value); setError(""); }}
            placeholder="Run the tests too and attach the output." className="text-base md:text-sm" />
        </Field>
      )}
      {kind === "reassign" && (
        <Field id="act-assignee" label="Assignee" error={error}>
          <Select value={assignee} onValueChange={(v) => { setAssignee(v); setError(""); }}>
            <SelectTrigger id="act-assignee" className="w-full data-[size=default]:h-11 md:data-[size=default]:h-9"><SelectValue placeholder="Choose an agent or a person" /></SelectTrigger>
            <SelectContent position="popper">
              {agents.map((a) => <SelectItem key={a.name} value={a.name}>{a.name}{a.role && <span className="text-muted-foreground"> · {a.role}</span>}</SelectItem>)}
              {people.map((p) => <SelectItem key={p.user} value={PERSON + p.user}>{displayName(p)}<span className="text-muted-foreground"> · person</span></SelectItem>)}
            </SelectContent>
          </Select>
        </Field>
      )}
      {kind === "work_submit" && (
        <>
          <Field id="act-pr" label="Pull request" hint="#200 or its URL; needed when the project has a repository.">
            <Input id="act-pr" autoFocus value={pr} onChange={(e) => { setPR(e.target.value); setError(""); }} placeholder="#200" className={touchInput} />
          </Field>
          <Field id="act-summary" label="What was done" error={error}>
            <Textarea id="act-summary" rows={3} value={text} onChange={(e) => { setText(e.target.value); setError(""); }}
              placeholder="Laid out the main menu; screenshots in the pull request." className="text-base md:text-sm" />
          </Field>
        </>
      )}
      {kind === "reviewer" && (
        <Field id="act-reviewer" label="Reviewer" hint={suggestion && shownReviewer === suggestion ? "Suggested by the team's review rules." : undefined} error={error}>
          <ReviewerSelect id="act-reviewer" value={shownReviewer} onChange={(v) => { setReviewBy(v); setError(""); }} agents={agents} assignee={task.assignee} />
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

"use client";

import { BellRing, Check, CircleAlert, Clock, Eye, FolderInput, FolderPlus, GitBranch, Lock, MessageCircleQuestion, ShieldCheck, ShieldQuestion, Siren, X } from "lucide-react";
import Link from "@/components/app/link";
import { useState } from "react";
import { MessageVisuals } from "@/components/app/materials/visuals";
import { Markdown } from "@/components/app/run/markdown";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import type { ActiveAlert, Approval, Message, Question, RepoMode } from "@/lib/api/types";
import { repoModeWords } from "@/lib/project-setup";
import { ago, displayId } from "@/lib/format";
import { cn } from "@/lib/utils";
import { ArtifactPreview } from "./artifact-preview";
import { Countdown } from "./countdown";

// What the inbox does with an item; the page confirms and sends it.
export interface InboxHandlers {
  answer: (msg: Message, text: string, answers: Record<string, unknown>) => void;
  read: (msg: Message) => void;
  // details: "I'll send details" (promised, with an optional note) or withdraw that promise (inbox-badge-parity).
  details: (msg: Message, kind: "promised" | "withdrawn", note: string) => void;
  decide: (kind: "approve" | "reject", ap: Approval, msg: Message | undefined, reason: string) => void;
  busy: boolean;
}

type Kind = "question" | "escalation" | "approval" | "report";
const look: Record<Kind, { label: string; icon: React.ElementType; cls: string }> = {
  question: { label: "Question", icon: MessageCircleQuestion, cls: "bg-sky-500/10 text-sky-700 dark:text-sky-300" },
  escalation: { label: "Escalated to you", icon: Siren, cls: "bg-red-500/10 text-red-700 dark:text-red-300" },
  approval: { label: "Approval", icon: ShieldQuestion, cls: "bg-violet-500/10 text-violet-700 dark:text-violet-300" },
  report: { label: "To read", icon: Eye, cls: "bg-muted text-muted-foreground" },
};

// answerable: a question or an escalation that takes an answer (internal/tui/inbox.go); notifications and
// reports are marked read.
export const answerable = (m: Message) => m.message_type === "question" && !m.approval_id && (m.status === "PENDING" || m.status === "ESCALATED");
export const kindOf = (m: Message): Kind => (m.approval_id ? "approval" : m.escalation_reason || m.status === "ESCALATED" ? "escalation" : answerable(m) ? "question" : "report");

// escalationWhy: why a question came to the owner, in plain words.
function escalationWhy(reason: string): string {
  const r = reason.replace(/_/g, " ");
  if (/answer deadline exceeded/.test(r)) return "It came to you because nobody in the team answered it in time.";
  if (/clarification limit/.test(r)) return "It came to you because the agents asked each other too many times without settling it.";
  return `Why it came to you: ${r}.`;
}

function Shell({ kind, blocking, from, project, taskId, conversation, at, due, dueVerb, children }: {
  kind: Kind; blocking?: boolean; from: string; project?: string; taskId?: string; conversation?: string; at: number; due?: number; dueVerb?: string; children: React.ReactNode;
}) {
  const l = look[kind];
  return (
    <article className={cn("rounded-2xl border bg-card p-4 shadow-xs sm:p-5", blocking && "border-red-500/30 ring-1 ring-red-500/10")}>
      <header className="flex flex-wrap items-center gap-x-2 gap-y-1.5">
        <span className={cn("inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium", l.cls)}><l.icon className="size-3.5" />{l.label}</span>
        {blocking && <span className="inline-flex items-center gap-1 rounded-full bg-red-500 px-2 py-0.5 text-xs font-medium text-white"><CircleAlert className="size-3" />Blocking</span>}
        {due ? <Countdown at={due} verb={dueVerb} /> : null}
        <span className="ml-auto text-xs text-muted-foreground tabular-nums">{ago(at)}</span>
      </header>
      <p className="mt-3 text-xs text-muted-foreground">
        From <span className="font-medium text-foreground">{from}</span>
        {project && <> · {project}</>}
        {taskId && (taskId.startsWith("conv-")
          ? <> · <Link href={conversation?.startsWith("owner-") ? `/chat?c=${encodeURIComponent(conversation)}` : "/chat"} className="text-foreground underline-offset-4 hover:underline">in chat</Link></>
          : <> · <Link href={`/tasks/${taskId}`} className="font-mono text-foreground underline-offset-4 hover:underline" title={taskId}>{displayId(taskId)}</Link></>)}
      </p>
      <div className="mt-3">{children}</div>
    </article>
  );
}

export function MessageCard({ msg, projectName, h }: { msg: Message; projectName?: string; h: InboxHandlers }) {
  const kind = kindOf(msg);
  return (
    <Shell kind={kind} blocking={msg.blocking} from={msg.sender} project={projectName ?? msg.project_id} taskId={msg.task_id} conversation={msg.conversation_id} at={msg.created_at} due={msg.deadline}>
      <Markdown text={msg.body} className="font-medium" />
      <MessageVisuals project={msg.project_id} at={msg.created_at} author={msg.sender} attachments={msg.attachments} materials={msg.materials} className="mt-3" />
      {msg.escalation_reason && <p className="mt-2 text-xs text-muted-foreground">{escalationWhy(msg.escalation_reason)}</p>}
      {msg.may_be_outdated && (
        <p className="mt-3 rounded-lg bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-200">The task changed since this was asked; the question may be outdated.</p>
      )}
      {msg.details === "promised" && (
        <p className="mt-3 flex items-start gap-2 rounded-lg bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-200">
          <Clock className="mt-px size-3.5 shrink-0" />
          <span>Details promised{msg.details_at ? ` ${ago(msg.details_at)}` : ""}{msg.details_note ? ` (“${msg.details_note}”)` : ""}. {msg.blocking && msg.task_id ? `${msg.sender}'s task waits for them and does not run again until you send them or withdraw.` : "Send them below when you have them."}</span>
        </p>
      )}
      {answerable(msg) ? <AnswerForm msg={msg} h={h} /> : (
        <div className="mt-4 flex justify-end">
          <Button variant="outline" className="h-10" disabled={h.busy} onClick={() => h.read(msg)}><Check /> Mark as read</Button>
        </div>
      )}
    </Shell>
  );
}

// Later: "I'll send details" on an open question, "Withdraw" once they are promised (inbox-badge-parity).
function Later({ msg, h, note }: { msg: Message; h: InboxHandlers; note: string }) {
  if (msg.details === "promised") {
    return <Button variant="outline" className="h-10" disabled={h.busy} onClick={() => h.details(msg, "withdrawn", note)}>Withdraw promise</Button>;
  }
  return <Button variant="outline" className="h-10" disabled={h.busy} onClick={() => h.details(msg, "promised", note)}><Clock />I&apos;ll send details</Button>;
}

function AnswerForm({ msg, h }: { msg: Message; h: InboxHandlers }) {
  const qs = msg.questions ?? [];
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [free, setFree] = useState("");
  const promised = msg.details === "promised";
  const single = qs.length === 1 && qs[0].type === "single_choice" && (qs[0].options?.length ?? 0) > 0;

  // The promised details are free text, whatever form the question had.
  if (qs.length === 0 || promised) {
    return (
      <div className="mt-4 space-y-3">
        <Textarea value={free} onChange={(e) => setFree(e.target.value)} placeholder={promised ? "The details…" : "Your answer…"} className="min-h-24 text-base md:text-sm" aria-label="Answer" />
        <div className="flex flex-wrap justify-end gap-2">
          <Later msg={msg} h={h} note={promised ? "" : free.trim()} />
          <Button className="h-10" disabled={!free.trim() || h.busy} onClick={() => h.answer(msg, free.trim(), {})}>{promised ? "Send details" : "Send answer"}</Button>
        </div>
      </div>
    );
  }

  const set = (q: Question, v: string) => setAnswers((a) => ({ ...a, [q.id]: v }));
  const complete = qs.every((q) => (answers[q.id] ?? "").trim());
  const send = () => {
    const text = qs.length === 1 ? answers[qs[0].id].trim() : qs.map((q) => `${q.question} ${answers[q.id].trim()}`).join("\n");
    // Words of one's own instead of an option ("Something else…") answer with the text alone (Core accepts only
    // listed options as form answers).
    const own = qs.some((q) => q.options?.length && !q.options.includes(answers[q.id].trim()));
    h.answer(msg, text, own ? {} : Object.fromEntries(qs.map((q) => [q.id, answers[q.id].trim()])));
  };

  return (
    <div className="mt-4 space-y-4">
      {qs.map((q) => (
        <fieldset key={q.id} className="space-y-2">
          <legend className="mb-2 text-sm font-medium">{q.question}</legend>
          {q.options?.length ? <Options q={q} value={answers[q.id]} onChange={(v) => set(q, v)} /> : null}
          {(!q.options?.length || q.type !== "single_choice" || (answers[q.id] !== undefined && !q.options.includes(answers[q.id]))) && (
            <Textarea value={answers[q.id] ?? ""} onChange={(e) => set(q, e.target.value)} placeholder={q.options?.length ? "Something else…" : "Your answer…"}
              className="min-h-20 text-base md:text-sm" aria-label={q.question} autoFocus={!!q.options?.length} />
          )}
        </fieldset>
      ))}
      <div className="flex items-center justify-end gap-2">
        {single && <span className="mr-auto text-xs text-muted-foreground">{answers[qs[0].id] ? "" : "Pick an option"}</span>}
        <Later msg={msg} h={h} note="" />
        <Button className="h-10" disabled={!complete || h.busy} onClick={send}>Send answer</Button>
      </div>
    </div>
  );
}

// Factory escalation answers read as what they do (dependency-wait-escalations 2.2); other options as written.
const answerLabels: Record<string, string> = {
  keep_waiting: "Keep waiting",
  drop_dependency: "Stop waiting for this task",
  reset_workspace: "Reset the workspace",
  new_session: "Continue in a new session",
  spec: "It writes the spec: run without the check",
  retry: "Try again",
};

export function optionLabel(o: string): string {
  return answerLabels[o] ?? o.replace(/[-_]/g, " ");
}

function Options({ q, value, onChange }: { q: Question; value?: string; onChange: (v: string) => void }) {
  const opts = q.options ?? [];
  const other = value !== undefined && !opts.includes(value);
  return (
    <div role="radiogroup" aria-label={q.question} className="grid gap-2 sm:grid-cols-2">
      {[...opts, "\u0000other"].map((o) => {
        const isOther = o === "\u0000other";
        if (isOther && q.type !== "single_choice") return null;
        const on = isOther ? other : value === o;
        return (
          <button key={o} type="button" role="radio" aria-checked={on} onClick={() => onChange(isOther ? (other ? value! : "") : o)}
            className={cn("flex min-h-11 items-center gap-3 rounded-xl border px-3 py-2 text-left text-sm transition-colors",
              on ? "border-foreground bg-foreground/[0.04] font-medium ring-1 ring-foreground" : "hover:bg-accent/60",
              isOther && "text-muted-foreground")}>
            <span className={cn("flex size-4 shrink-0 items-center justify-center rounded-full border", on && "border-foreground bg-foreground")}>
              {on && <span className="size-1.5 rounded-full bg-background" />}
            </span>
            <span className="min-w-0 break-words">{isOther ? "Something else…" : optionLabel(o)}</span>
          </button>
        );
      })}
    </div>
  );
}

// AlertCard: an active task alert or a run to inspect (inbox-badge-parity), counted by the badge like every Inbox
// item. "with <lead>": the lead is expected to act; the owner sees it too.
export function AlertCard({ alert, projectName }: { alert: ActiveAlert; projectName?: string }) {
  return (
    <article className="rounded-2xl border bg-card p-4 shadow-xs sm:p-5">
      <header className="flex flex-wrap items-center gap-x-2 gap-y-1.5">
        <span className="inline-flex items-center gap-1 rounded-full bg-amber-500/10 px-2 py-0.5 text-xs font-medium text-amber-800 dark:text-amber-200">
          <BellRing className="size-3.5" />{alertLabel(alert.kind)}
        </span>
        {alert.with && <span className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">with {alert.with}</span>}
        <span className="ml-auto text-xs text-muted-foreground tabular-nums" title="raised">{ago(alert.since)}</span>
      </header>
      <p className="mt-3 text-xs text-muted-foreground">
        {projectName ?? alert.project_id}
        {alert.assignee && <> · {alert.assignee}</>}
        {" · "}<Link href={`/tasks/${alert.task_id}`} className="font-mono text-foreground underline-offset-4 hover:underline" title={alert.task_id}>{displayId(alert.task_id)}</Link>
      </p>
      {alert.task_title && <Link href={`/tasks/${alert.task_id}`} className="mt-1 block text-[15px] font-medium leading-snug underline-offset-4 hover:underline">{alert.task_title}</Link>}
      <p className="mt-2 text-sm whitespace-pre-wrap text-muted-foreground">{alert.summary}</p>
      {alert.action && <p className="mt-2 text-xs text-muted-foreground"><span className="font-medium text-foreground">To clear it:</span> {alert.action}</p>}
    </article>
  );
}

const alertLabels: Record<string, string> = {
  no_progress: "No progress",
  idle: "Idle",
  stalled: "Stalled",
  inspect: "Run to inspect",
  no_ready_node: "No ready node",
  sweep_failed: "Sweep failed",
  comment_unhandled: "Comment not handled",
  throughput: "Work waits, capacity idle",
};

export function alertLabel(kind: string): string {
  return alertLabels[kind] ?? kind.replace(/[-_]/g, " ");
}

export function ApprovalCard({ ap, msg, projectName, h }: { ap: Approval; msg?: Message; projectName?: string; h: InboxHandlers }) {
  const [reason, setReason] = useState("");
  const [needReason, setNeedReason] = useState(false);
  const args = Object.entries(ap.call?.arguments ?? {});
  const artifact = ap.kind === "artifact" || !!ap.artifact;
  return (
    <Shell kind="approval" blocking={msg?.blocking} from={ap.agent} project={projectName ?? ap.project_id} taskId={ap.task_id} at={ap.created_at} due={ap.expires_at} dueVerb="Expires">
      <Markdown text={msg?.body || (artifact ? `Approve ${ap.artifact?.name ?? ap.artifact?.id}?` : `Approve ${ap.call.name}?`)} className="font-medium" />
      {ap.reason && <p className="mt-1.5 text-sm text-muted-foreground">{ap.reason}</p>}

      <div className="mt-4">
        {artifact && ap.artifact ? <ArtifactPreview project={ap.project_id} artifact={ap.artifact} /> : ap.call?.name === "factory_project_create" ? (
          <ProjectRequest args={ap.call.arguments ?? {}} />
        ) : ap.call?.name === "factory_catalog_change" ? (
          <CatalogChangeRequest args={ap.call.arguments ?? {}} />
        ) : (
          <div className="overflow-hidden rounded-xl border">
            <div className="flex items-center gap-2 border-b bg-muted/40 px-3 py-2">
              <ShieldCheck className="size-3.5 shrink-0 text-muted-foreground" />
              <code className="min-w-0 break-all font-mono text-[13px] font-medium">{ap.call.name || "call"}</code>
            </div>
            {args.length ? (
              <dl className="divide-y text-sm">
                {args.map(([k, v]) => (
                  <div key={k} className="grid grid-cols-[minmax(5rem,auto)_1fr] gap-3 px-3 py-2">
                    <dt className="text-muted-foreground">{k.replace(/_/g, " ")}</dt>
                    <dd className="min-w-0 break-words font-mono text-[13px]">
                      {typeof v === "object" && v !== null ? <pre className="overflow-x-auto whitespace-pre-wrap">{JSON.stringify(v, null, 2)}</pre> : String(v)}
                    </dd>
                  </div>
                ))}
              </dl>
            ) : <p className="px-3 py-2 text-sm text-muted-foreground">No arguments</p>}
          </div>
        )}
      </div>

      <div className="mt-4 space-y-3">
        <Textarea value={reason} onChange={(e) => { setReason(e.target.value); setNeedReason(false); }}
          placeholder="Note for the agent (required to reject)" aria-label="Reason" aria-invalid={needReason}
          className="min-h-16 text-base md:text-sm" />
        {needReason && <p className="text-xs text-red-600 dark:text-red-400">Say why you reject it, so the agent can fix it.</p>}
        <div className="flex flex-wrap items-center justify-end gap-2">
          {ap.run_id && <Link href={`/runs/${ap.run_id}`} className="mr-auto inline-flex h-10 items-center text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">View the run</Link>}
          <Button variant="outline" className="h-10 border-red-500/30 text-red-600 hover:bg-red-500/10 hover:text-red-700 dark:text-red-400" disabled={h.busy}
            onClick={() => (reason.trim() ? h.decide("reject", ap, msg, reason.trim()) : setNeedReason(true))}>
            <X /> Reject
          </Button>
          <Button className="h-10 bg-emerald-600 text-white hover:bg-emerald-600/90 dark:bg-emerald-600" disabled={h.busy}
            onClick={() => h.decide("approve", ap, msg, reason.trim() || "approved by owner")}>
            <Check /> Approve
          </Button>
        </div>
      </div>
    </Shell>
  );
}

// ProjectRequest: an agent's factory_project_create call (project-creation D6) as the project it would create.
function ProjectRequest({ args }: { args: Record<string, unknown> }) {
  const str = (k: string) => (typeof args[k] === "string" ? (args[k] as string).trim() : args[k] == null ? "" : String(args[k]));
  const id = str("project_id"), name = str("name") || id;
  const mode = (str("repo_mode") || (str("workspace") || str("repo_url") ? "existing" : "local")) as RepoMode;
  const Icon = mode === "github" ? Lock : mode === "existing" ? FolderInput : GitBranch;
  const rows: [string, string][] = [["Folder on your machine", str("workspace")], ["Repository URL", str("repo_url")], ["Branch", str("default_branch")]];
  const row = "grid grid-cols-[minmax(6.5rem,auto)_1fr] gap-3 px-3 py-2";
  return (
    <div className="overflow-hidden rounded-xl border">
      <div className="flex items-start gap-3 border-b bg-muted/40 px-3 py-3">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-background text-muted-foreground ring-1 ring-border"><FolderPlus className="size-4" /></span>
        <div className="min-w-0">
          <p className="text-xs text-muted-foreground">New project</p>
          <p className="break-words font-medium leading-snug">{name}</p>
          {id && id !== name && <p className="break-all font-mono text-xs text-muted-foreground">{id}</p>}
        </div>
      </div>
      <dl className="divide-y text-sm">
        {str("description") && (
          <div className="px-3 py-2.5"><dt className="sr-only">Description</dt><dd className="break-words text-foreground/90">{str("description")}</dd></div>
        )}
        <div className={row}>
          <dt className="text-muted-foreground">Repository</dt>
          <dd className="flex min-w-0 items-center gap-1.5"><Icon className="size-3.5 shrink-0 text-muted-foreground" />{repoModeWords[mode] ?? mode}</dd>
        </div>
        {rows.filter(([, v]) => v).map(([k, v]) => (
          <div key={k} className={row}>
            <dt className="text-muted-foreground">{k}</dt>
            <dd className="min-w-0 break-all font-mono text-[13px]">{v}</dd>
          </div>
        ))}
        {mode === "existing" && (
          <div className={row}>
            <dt className="text-muted-foreground">Audit first</dt>
            <dd>{args.audit === true ? "Yes, a read-only audit task runs first" : "No"}</dd>
          </div>
        )}
      </dl>
      <p className="border-t bg-muted/20 px-3 py-2 text-xs text-muted-foreground">Nothing is created until you approve. The repository is set up when the project&apos;s first task runs.</p>
    </div>
  );
}

// CatalogChangeRequest (agent-company-authority 2.2): a catalog change in plain words — which agents, prompts, teams
// and company settings change, and what is deleted (highlighted: deleting a team or an agent needs an owner).
function catalogItem(p: string): string {
  const file = p.split("/").pop() ?? p;
  const name = file.replace(/\.(ya?ml|md)$/, "");
  if (p.startsWith("prompts/")) return `the prompt of ${name}`;
  if (/(^|\/)agents\//.test(p)) return `agent ${name}`;
  if (/(^|\/)teams\//.test(p)) return `team ${name}`;
  if (/(^|\/)profiles\//.test(p)) return `shared settings ${name}`;
  if (/(^|\/)policies\//.test(p)) return `rules ${name}`;
  if (/skills\.ya?ml$/.test(p)) return "the company's skills";
  if (/quota_keys\.ya?ml$/.test(p)) return "the company's subscriptions";
  if (/node-hooks\//.test(p)) return "the node hooks";
  return p;
}
function CatalogChangeRequest({ args }: { args: Record<string, unknown> }) {
  const put = Object.keys((args.put as Record<string, unknown>) ?? {});
  const del = ((args.delete as unknown[]) ?? []).map(String);
  const destructive = del.filter((p) => /(^|\/)(agents|teams)\//.test(p));
  return (
    <div className="space-y-2 rounded-xl border p-3 text-sm">
      {typeof args.note === "string" && args.note && <p className="font-medium">{args.note}</p>}
      {put.length > 0 && <p><span className="text-muted-foreground">Changes or adds:</span> {put.map(catalogItem).join(", ")}</p>}
      {del.length > 0 && (
        <p className={destructive.length ? "font-medium text-red-700 dark:text-red-300" : ""}>
          <span className={destructive.length ? "" : "text-muted-foreground"}>Deletes:</span> {del.map(catalogItem).join(", ")}
          {destructive.length > 0 && " — only an owner can approve this"}
        </p>
      )}
      <details className="text-xs text-muted-foreground"><summary className="cursor-pointer">Documents</summary>
        <ul className="mt-1 font-mono">{[...put, ...del].map((p) => <li key={p}>{p}</li>)}</ul>
      </details>
    </div>
  );
}

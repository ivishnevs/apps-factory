"use client";

import { Check, CircleAlert, Eye, FolderInput, FolderPlus, GitBranch, Lock, MessageCircleQuestion, ShieldCheck, ShieldQuestion, Siren, X } from "lucide-react";
import Link from "next/link";
import { useState } from "react";
import { Markdown } from "@/components/app/run/markdown";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import type { Approval, Message, Question, RepoMode } from "@/lib/api/types";
import { repoModeWords } from "@/lib/project-setup";
import { ago, displayId } from "@/lib/format";
import { cn } from "@/lib/utils";
import { ArtifactPreview } from "./artifact-preview";
import { Countdown } from "./countdown";

// What the inbox does with an item; the page confirms and sends it.
export interface InboxHandlers {
  answer: (msg: Message, text: string, answers: Record<string, unknown>) => void;
  read: (msg: Message) => void;
  decide: (kind: "approve" | "reject", ap: Approval, msg: Message | undefined, reason: string) => void;
  busy: boolean;
}

type Kind = "question" | "escalation" | "approval" | "report";
const look: Record<Kind, { label: string; icon: React.ElementType; cls: string }> = {
  question: { label: "Question", icon: MessageCircleQuestion, cls: "bg-sky-500/10 text-sky-700 dark:text-sky-300" },
  escalation: { label: "Escalation", icon: Siren, cls: "bg-red-500/10 text-red-700 dark:text-red-300" },
  approval: { label: "Approval", icon: ShieldQuestion, cls: "bg-violet-500/10 text-violet-700 dark:text-violet-300" },
  report: { label: "To read", icon: Eye, cls: "bg-muted text-muted-foreground" },
};

// answerable: a question or an escalation that takes an answer (internal/tui/inbox.go); notifications and
// reports are marked read.
export const answerable = (m: Message) => m.message_type === "question" && !m.approval_id && (m.status === "PENDING" || m.status === "ESCALATED");
export const kindOf = (m: Message): Kind => (m.approval_id ? "approval" : m.escalation_reason || m.status === "ESCALATED" ? "escalation" : answerable(m) ? "question" : "report");

function Shell({ kind, blocking, from, project, taskId, at, due, dueVerb, children }: {
  kind: Kind; blocking?: boolean; from: string; project?: string; taskId?: string; at: number; due?: number; dueVerb?: string; children: React.ReactNode;
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
        {taskId && <> · <Link href={`/tasks/${taskId}`} className="font-mono text-foreground underline-offset-4 hover:underline" title={taskId}>{displayId(taskId)}</Link></>}
      </p>
      <div className="mt-3">{children}</div>
    </article>
  );
}

export function MessageCard({ msg, projectName, h }: { msg: Message; projectName?: string; h: InboxHandlers }) {
  const kind = kindOf(msg);
  return (
    <Shell kind={kind} blocking={msg.blocking} from={msg.sender} project={projectName ?? msg.project_id} taskId={msg.task_id} at={msg.created_at} due={msg.deadline}>
      <Markdown text={msg.body} className="font-medium" />
      {msg.escalation_reason && <p className="mt-2 text-xs text-muted-foreground">Reason: <span className="font-medium text-foreground">{msg.escalation_reason.replace(/_/g, " ")}</span></p>}
      {msg.may_be_outdated && (
        <p className="mt-3 rounded-lg bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-200">The task changed since this was asked; the question may be outdated.</p>
      )}
      {answerable(msg) ? <AnswerForm msg={msg} h={h} /> : (
        <div className="mt-4 flex justify-end">
          <Button variant="outline" className="h-10" disabled={h.busy} onClick={() => h.read(msg)}><Check /> Mark as read</Button>
        </div>
      )}
    </Shell>
  );
}

function AnswerForm({ msg, h }: { msg: Message; h: InboxHandlers }) {
  const qs = msg.questions ?? [];
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const [free, setFree] = useState("");
  const single = qs.length === 1 && qs[0].type === "single_choice" && (qs[0].options?.length ?? 0) > 0;

  if (qs.length === 0) {
    return (
      <div className="mt-4 space-y-3">
        <Textarea value={free} onChange={(e) => setFree(e.target.value)} placeholder="Your answer…" className="min-h-24 text-base md:text-sm" aria-label="Answer" />
        <div className="flex justify-end">
          <Button className="h-10" disabled={!free.trim() || h.busy} onClick={() => h.answer(msg, free.trim(), {})}>Send answer</Button>
        </div>
      </div>
    );
  }

  const set = (q: Question, v: string) => setAnswers((a) => ({ ...a, [q.id]: v }));
  const complete = qs.every((q) => (answers[q.id] ?? "").trim());
  const send = () => {
    const text = qs.length === 1 ? answers[qs[0].id].trim() : qs.map((q) => `${q.question} ${answers[q.id].trim()}`).join("\n");
    h.answer(msg, text, Object.fromEntries(qs.map((q) => [q.id, answers[q.id].trim()])));
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
        <Button className="h-10" disabled={!complete || h.busy} onClick={send}>Send answer</Button>
      </div>
    </div>
  );
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
            <span className="min-w-0 break-words">{isOther ? "Something else…" : o.replace(/[-_]/g, " ")}</span>
          </button>
        );
      })}
    </div>
  );
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
  const openspec = args.openspec !== false;
  const rows: [string, string][] = [["Folder on the node", str("workspace")], ["Repository URL", str("repo_url")], ["Branch", str("default_branch")]];
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
        <div className={row}>
          <dt className="text-muted-foreground">OpenSpec</dt>
          <dd>{openspec ? (mode === "existing" ? "On; its openspec/ is imported" : "On") : "Off"}</dd>
        </div>
      </dl>
      <p className="border-t bg-muted/20 px-3 py-2 text-xs text-muted-foreground">Nothing is created until you approve. The repository is set up when the project&apos;s first task runs.</p>
    </div>
  );
}

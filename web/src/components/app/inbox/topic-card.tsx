"use client";

import { Check, CheckCheck, ChevronRight, CircleAlert, History, MessageSquareReply, Send, Undo2, X } from "lucide-react";
import Link from "@/components/app/link";
import { useMemo, useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { DecisionCard } from "@/components/app/decision-card";
import { ApprovalBadge } from "@/components/app/materials/kinds";
import { MessageVisuals } from "@/components/app/materials/visuals";
import { Markdown } from "@/components/app/run/markdown";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { useAction, useOverview, useTopicWrite } from "@/lib/api/client";
import type { Action } from "@/lib/api/types";
import type { ItemView, TopicDecision, TopicView } from "@/lib/api/types-topics";
import { ago } from "@/lib/format";
import { peopleText } from "@/lib/people-text";
import { cn } from "@/lib/utils";
import { ArtifactPreview } from "./artifact-preview";
import { CatalogChangeRequest, optionLabel, ProjectRequest } from "./cards";
import { ChoiceApproval, isChoice } from "./choice-approval";
import { approvalDecision } from "./decide";

// TopicCard (people-topics D5, D6, D9, D10): one open topic as people read it — its brief (what you decide, the
// recommendation, what happens after, what you do not decide, the context), who waits, then its items, each with its
// note, preview and buttons from Core's actions. Earlier versions of an item are its row's history. It never shows ids,
// hashes, JSON or an item's raw body: the brief, the notes and Core's cleaned text.

// leadOf: the agent of an owner chat (owner-<agent>-<project>).
export const leadOf = (t: Pick<TopicView, "conversation_id" | "project_id">) => {
  const c = t.conversation_id ?? "";
  if (!c.startsWith("owner-")) return "";
  const rest = c.slice("owner-".length);
  return rest.endsWith(`-${t.project_id}`) ? rest.slice(0, -(t.project_id.length + 1)) : rest;
};

const kindWords: Record<ItemView["kind"], string> = {
  approval: "Approve", question: "Question", escalation: "Stopped", decision: "Decision", review: "Review", update: "Update",
};

// A row: the current item of a version line (or a lone item) and the versions it replaced, newest first.
interface Row { key: string; current: ItemView; history: ItemView[] }

function rowsOf(items: ItemView[]): Row[] {
  const groups = new Map<string, ItemView[]>();
  for (const it of items) {
    if (it.kind === "update" || it.state === "update") continue;
    const k = it.lineage ? `line:${it.lineage}` : `${it.kind}:${it.ref}`;
    groups.set(k, [...(groups.get(k) ?? []), it]);
  }
  return [...groups.entries()].map(([key, list]) => {
    const sorted = [...list].sort((a, b) => a.added_at - b.added_at);
    const live = sorted.filter((i) => i.state !== "replaced");
    const current = live.at(-1) ?? sorted.at(-1)!;
    return { key, current, history: sorted.filter((i) => i !== current).reverse() };
  });
}

const versionOf = (it: ItemView) => it.version || it.approval?.artifact?.version || "";
const vLabel = (v: string) => (!v ? "" : /^v/i.test(v) ? v : `v${v}`);

// itemText: what the item is, for people. The note first; then the item's own brief when it differs from the topic's;
// the cleaned text when the topic has no brief yet (needs_brief) or the item says nothing else.
function itemText(t: TopicView, it: ItemView): string {
  if (it.note?.what) return it.note.what;
  const b = it.message?.brief;
  if (b?.ask && b.ask !== t.brief.ask) return b.ask;
  if (b?.title && b.title !== t.brief.title) return b.title;
  if (it.decision) return "";
  if (it.kind === "review") return it.task?.title ?? "";
  const single = t.items.filter((i) => i.state !== "update").length === 1;
  if (t.needs_brief || !t.brief.ask || !single) return it.message ? peopleText(it.message) : "";
  return "";
}

const itemName = (it: ItemView) => it.approval?.artifact?.name || it.task?.title || "";

function statusWords(it: ItemView): string {
  if (it.state === "replaced") return "";
  if (it.approval) return "";
  const s = it.status.toLowerCase().replace(/_/g, " ");
  return s === "answered" || s === "read" ? s : s === "done" ? "accepted" : s;
}

export function TopicCard({ topic, projectName, compact, onReply }: {
  topic: TopicView; projectName?: string;
  // compact: in a chat thread (no context, no reply box: the composer replies; onReply points it at this topic).
  compact?: boolean; onReply?: (t: TopicView) => void;
}) {
  const t = topic;
  const confirm = useConfirm();
  const { decide } = useTopicWrite();
  const action = useAction();
  const busy = decide.isPending || action.isPending;
  const rows = useMemo(() => rowsOf(t.items), [t.items]);
  const updates = t.items.filter((i) => i.kind === "update" || i.state === "update");
  const lead = leadOf(t);
  const title = t.brief.title || itemName(t.items[0] ?? ({} as ItemView)) || "A question from your team";

  // send decides items together (all or none), after a confirmation.
  const send = async (items: TopicDecision[], ask: { title: string; description?: string; confirm: string; destructive?: boolean }, done: string) => {
    if (!(await confirm(ask))) return false;
    try {
      await decide.mutateAsync({ topic: t.id, items });
      toast.success(done);
      return true;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
      return false;
    }
  };
  // act runs an item's own endpoint (a free answer, a review, mark read), after a confirmation.
  const act = async (a: Action, ask: { title: string; description?: string; confirm: string; destructive?: boolean }, done: string) => {
    if (!(await confirm(ask))) return false;
    try {
      await action.mutateAsync(a);
      toast.success(done);
      return true;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
      return false;
    }
  };

  const waiting = rows.map((r) => r.current).filter((i) => i.waits);
  const approvals = waiting.filter((i) => i.kind === "approval" && i.approval && i.actions?.includes("approve"));
  const escalations = waiting.filter((i) => i.kind === "escalation" && i.message && i.actions?.length);
  // Answers every waiting escalation offers ("retry all"), when two or more wait.
  const common = escalations.length >= 2
    ? (escalations[0].actions ?? []).filter((a) => a !== "answer" && escalations.every((e) => e.actions?.includes(a)))
    : [];

  return (
    <article id={`topic-${t.id}`} className={cn("scroll-mt-20 rounded-2xl border bg-card p-4 shadow-xs sm:p-5", t.blocked?.length && "border-red-500/30 ring-1 ring-red-500/10")}>
      <header className="flex flex-wrap items-center gap-x-2 gap-y-1.5">
        <span className="inline-flex items-center rounded-full bg-amber-500/10 px-2 py-0.5 text-xs font-medium text-amber-800 dark:text-amber-200">
          Waits for you: {t.waiting}
        </span>
        {!!t.blocked?.length && <span className="inline-flex items-center gap-1 rounded-full bg-red-500 px-2 py-0.5 text-xs font-medium text-white"><CircleAlert className="size-3" />Blocks work</span>}
        <span className="ml-auto text-xs text-muted-foreground tabular-nums">{ago(t.at)}</span>
      </header>
      <p className="mt-2 text-xs text-muted-foreground">
        {projectName ?? t.project_id}{t.brought_by && <> · brought by <span className="font-medium text-foreground">{t.brought_by === "factory" ? "App Factory" : t.brought_by}</span></>}
      </p>
      <h3 className={cn("mt-1.5 break-words font-semibold leading-snug tracking-tight", compact ? "text-base" : "text-lg")}>{title}</h3>
      {t.needs_brief && (
        <p className="mt-1.5 text-xs text-muted-foreground">The question is not written yet{lead ? `: ${lead} was asked to write it` : ""}. Below is what the team wrote.</p>
      )}

      <dl className="mt-3 space-y-2.5 text-sm">
        {t.brief.ask && <Field label="What you decide"><span className="font-medium">{t.brief.ask}</span></Field>}
        {t.brief.recommendation && <Field label="Recommended">{t.brief.recommendation}</Field>}
        {t.brief.after && <Field label="What happens after"><span className="whitespace-pre-wrap">{t.brief.after}</span></Field>}
        {t.brief.changes && <Field label="What changed">{t.brief.changes}</Field>}
        {t.brief.scope && <Field label="You do not decide">{t.brief.scope}</Field>}
      </dl>
      {t.brief.context && !compact && (
        <details className="group mt-3 rounded-xl border px-3 py-2 text-sm">
          <summary className="flex min-h-8 cursor-pointer list-none items-center gap-1 text-xs font-medium text-muted-foreground hover:text-foreground">
            <ChevronRight className="size-3.5 transition-transform group-open:rotate-90" />Context
          </summary>
          <Markdown text={t.brief.context} className="mt-2" />
        </details>
      )}
      {!!t.blocked?.length && (
        <p className="mt-3 text-xs text-muted-foreground">
          <span className="font-medium text-foreground">Waiting on you:</span>{" "}
          {t.blocked.map((b, i) => (
            <span key={b.id}>{i > 0 && ", "}<Link href={`/tasks/${encodeURIComponent(b.id)}`} className="underline-offset-4 hover:underline">{b.title}</Link>{b.assignee && ` (${b.assignee})`}</span>
          ))}
        </p>
      )}

      <ul className="mt-4 space-y-3">
        {rows.map((r) => (
          <li key={r.key}>
            <ItemRow topic={t} row={r} busy={busy} send={send} act={act} compact={compact} />
          </li>
        ))}
      </ul>

      {(approvals.length >= 2 || common.length > 0) && (
        <div className="mt-4 flex flex-wrap justify-end gap-2 border-t pt-3">
          {common.map((a) => (
            <Button key={a} variant="outline" className="h-10" disabled={busy}
              onClick={() => send(escalations.map((e) => ({ kind: "escalation", ref: e.ref, version: e.message!.version, action: a })),
                { title: `${optionLabel(a)}: all ${escalations.length}?`, description: escalations.map((e) => `• ${itemText(t, e) || itemName(e) || kindWords.escalation}`).join("\n"), confirm: optionLabel(a) },
                `${optionLabel(a)}: ${escalations.length} done`)}>
              {optionLabel(a)} all {escalations.length}
            </Button>
          ))}
          {approvals.length >= 2 && (
            <Button className="h-10 bg-emerald-600 text-white hover:bg-emerald-600/90 dark:bg-emerald-600" disabled={busy}
              onClick={() => send(approvals.map((i) => ({ kind: "approval", ref: i.ref, version: Math.max(i.approval!.version, 1), action: "approve" })),
                { title: `Approve all ${approvals.length}?`, description: `${approvals.map((i) => `• ${itemName(i) || itemText(t, i) || "approval"}${vLabel(versionOf(i)) ? ` ${vLabel(versionOf(i))}` : ""}`).join("\n")}\n\nAll are approved together, or none if one cannot be.`, confirm: "Approve all" },
                `${approvals.length} approved`)}>
              <CheckCheck />Approve all {approvals.length}
            </Button>
          )}
        </div>
      )}

      {updates.length > 0 && !compact && <Updates topic={t} items={updates} busy={busy} act={act} />}

      {t.conversation_id && (compact ? (
        onReply && (
          <div className="mt-3 flex justify-end">
            <Button variant="ghost" size="sm" className="h-10" onClick={() => onReply(t)}><MessageSquareReply />Reply in this topic</Button>
          </div>
        )
      ) : <TopicReply topic={t} lead={lead} />)}
    </article>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 break-words">{children}</dd>
    </div>
  );
}

type Send = (items: TopicDecision[], ask: { title: string; description?: string; confirm: string; destructive?: boolean }, done: string) => Promise<boolean>;
type Act = (a: Action, ask: { title: string; description?: string; confirm: string; destructive?: boolean }, done: string) => Promise<boolean>;

function ItemRow({ topic, row, busy, send, act, compact }: { topic: TopicView; row: Row; busy: boolean; send: Send; act: Act; compact?: boolean }) {
  const it = row.current;
  const text = itemText(topic, it);
  const name = itemName(it);
  const v = vLabel(versionOf(it));
  const replaced = it.state === "replaced";
  const status = statusWords(it);
  const ap = it.approval;
  return (
    <div className={cn("rounded-xl border p-3", !it.waits && "bg-muted/30")}>
      <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
        <span className="font-medium text-foreground">{kindWords[it.kind]}</span>
        {name && <span className="min-w-0 break-words">{name}{v && ` · ${v}`}</span>}
        {ap && !it.waits && <ApprovalBadge status={ap.status} />}
        {!ap && !it.waits && status && <span className="rounded-full bg-muted px-2 text-[11px] leading-5">{status}</span>}
        {it.brought_by && it.brought_by !== topic.brought_by && <span>· brought by {it.brought_by}</span>}
      </p>
      {text && <Markdown text={text} className={cn("mt-1.5 text-sm", it.waits && "font-medium")} />}
      {it.note?.changes && <p className="mt-1.5 text-sm"><span className="text-xs font-medium text-muted-foreground">What changed: </span>{it.note.changes}</p>}
      {it.message?.questions && it.message.questions.length > 1 && (
        <ul className="mt-1.5 list-disc space-y-0.5 pl-5 text-sm">{it.message.questions.map((q) => <li key={q.id}>{q.question}</li>)}</ul>
      )}
      {ap && isChoice(ap) ? (
        // A choice approval (artifact-choice-approvals): its options are its preview and its buttons.
        <div className="mt-3">
          <ChoiceApproval ap={ap} compact={compact || !it.waits} busy={busy} decide={it.waits ? (kind, reason, option) => {
            const d = approvalDecision(kind, ap, reason, option);
            void send([{ kind: "approval", ref: it.ref, version: Math.max(ap.version, 1), action: kind, reason, option }], d.ask, d.done);
          } : undefined} />
        </div>
      ) : <ItemVisuals it={it} compact={compact} />}
      {row.history.length > 0 && <HistoryList items={row.history} current={it} />}
      {replaced && <p className="mt-2 text-xs text-muted-foreground">{ap?.status === "SUPERSEDED" && ap.decision_reason ? `Withdrawn: ${ap.decision_reason}` : "Replaced by a newer version"}</p>}
      {it.waits && !(ap && isChoice(ap)) && <ItemActions topic={topic} it={it} busy={busy} send={send} act={act} />}
    </div>
  );
}

// ItemVisuals: the artifact to approve, the project or catalog change a call would make, the files a message carries.
function ItemVisuals({ it, compact }: { it: ItemView; compact?: boolean }) {
  const ap = it.approval;
  if (ap?.artifact && (ap.kind === "artifact" || !ap.call?.name)) {
    // A replaced or decided version stays visible but smaller.
    return <div className={cn("mt-3", (!it.waits || compact) && "max-h-72 overflow-hidden rounded-xl")}><ArtifactPreview project={ap.project_id} artifact={{ ...ap.artifact, name: ap.artifact.name || "Artifact" }} /></div>;
  }
  if (ap?.call?.name === "factory_project_create") return <div className="mt-3"><ProjectRequest args={ap.call.arguments ?? {}} /></div>;
  if (ap?.call?.name === "factory_catalog_change") return <div className="mt-3"><CatalogChangeRequest args={ap.call.arguments ?? {}} /></div>;
  const m = it.message;
  if (m && (m.attachments?.length || m.materials?.length)) {
    return <MessageVisuals project={m.project_id} at={m.created_at} author={m.sender} attachments={m.attachments} materials={m.materials} className="mt-3" />;
  }
  return null;
}

// HistoryList: the versions this row replaced, with what people decided and said on each (D10).
function HistoryList({ items, current }: { items: ItemView[]; current: ItemView }) {
  const now = vLabel(versionOf(current));
  return (
    <details className="group mt-3 text-xs">
      <summary className="flex min-h-8 cursor-pointer list-none items-center gap-1 font-medium text-muted-foreground hover:text-foreground">
        <History className="size-3.5" />Earlier versions ({items.length})
        <ChevronRight className="size-3.5 transition-transform group-open:rotate-90" />
      </summary>
      <ul className="mt-1.5 space-y-1.5 border-l pl-3">
        {items.map((h) => {
          const v = vLabel(versionOf(h));
          return (
            <li key={`${h.kind}:${h.ref}`} className="text-muted-foreground">
              <span className="flex flex-wrap items-center gap-x-1.5">
                <span className="font-medium text-foreground">{v || "Earlier version"}</span>
                {h.approval && <ApprovalBadge status={h.approval.status} />}
                {h.state === "replaced" && now && <span>replaced by {now}</span>}
                <span className="tabular-nums">· {ago(h.added_at)}</span>
              </span>
              {h.note?.what && <span className="block">{h.note.what}</span>}
              {h.approval?.decision_reason && h.approval.decision_reason !== "approved by owner" && <span className="block">Your comment: “{h.approval.decision_reason}”</span>}
            </li>
          );
        })}
      </ul>
    </details>
  );
}

// ItemActions: the item's buttons, only those Core lists (D5). A rejection and a return ask for a reason first.
function ItemActions({ topic, it, busy, send, act }: { topic: TopicView; it: ItemView; busy: boolean; send: Send; act: Act }) {
  const [why, setWhy] = useState<"" | "reject" | "return" | "answer">("");
  const [text, setText] = useState("");
  const { data: ov } = useOverview();
  const actions = it.actions ?? [];
  const label = itemName(it) || itemText(topic, it).slice(0, 80) || topic.brief.title || "this";
  const reset = () => { setWhy(""); setText(""); };

  if (it.kind === "decision" && it.decision) return <DecisionCard id={it.decision.id} compact />;

  if (it.kind === "approval" && it.approval) {
    const ap = it.approval;
    const one = (action: "approve" | "reject", reason?: string) =>
      send([{ kind: "approval", ref: it.ref, version: Math.max(ap.version, 1), action, reason }],
        { title: `${action === "approve" ? "Approve" : "Reject"} ${label}?`, description: reason ? `Your reason: ${reason}` : topic.brief.after ? `After: ${topic.brief.after}` : undefined, confirm: action === "approve" ? "Approve" : "Reject", destructive: action === "reject" },
        action === "approve" ? "Approved" : "Rejected").then((ok) => ok && reset());
    return (
      <Reasoned open={why === "reject"} text={text} setText={setText} placeholder="Why you reject it, so the team can fix it" onCancel={reset}
        onSend={() => one("reject", text.trim())} sendLabel="Reject" busy={busy} destructive>
        {actions.includes("reject") && (
          <Button variant="outline" className="h-10 border-red-500/30 text-red-600 hover:bg-red-500/10 hover:text-red-700 dark:text-red-400" disabled={busy} onClick={() => setWhy("reject")}><X />Reject</Button>
        )}
        {actions.includes("approve") && (
          <Button className="h-10 bg-emerald-600 text-white hover:bg-emerald-600/90 dark:bg-emerald-600" disabled={busy} onClick={() => one("approve")}><Check />Approve</Button>
        )}
      </Reasoned>
    );
  }

  if (it.kind === "review" && it.task) {
    const task = ov?.tasks.find((x) => x.id === it.task!.id);
    if (!task) return <Link href={`/tasks/${encodeURIComponent(it.task.id)}`} className="mt-3 inline-flex h-10 items-center text-sm font-medium underline-offset-4 hover:underline">Review the task</Link>;
    const review = (accept: boolean, feedback?: string) =>
      act({ Kind: accept ? "accept" : "rework", TaskID: task.id, Version: task.version, Text: feedback },
        accept ? { title: "Accept this work?", description: `“${task.title}” is done.`, confirm: "Accept" } : { title: "Send it back?", description: `Your feedback: ${feedback}`, confirm: "Send back" },
        accept ? "Accepted" : "Sent back").then((ok) => ok && reset());
    return (
      <Reasoned open={why === "return"} text={text} setText={setText} placeholder="What should change" onCancel={reset} onSend={() => review(false, text.trim())} sendLabel="Send back" busy={busy}>
        {actions.includes("return") && <Button variant="outline" className="h-10" disabled={busy} onClick={() => setWhy("return")}><Undo2 />Send back</Button>}
        {actions.includes("accept") && <Button className="h-10" disabled={busy} onClick={() => review(true)}><CheckCheck />Accept</Button>}
      </Reasoned>
    );
  }

  const m = it.message;
  if (!m || (it.kind !== "question" && it.kind !== "escalation")) return null;
  const options = actions.filter((a) => a !== "answer");
  const choose = (o: string) =>
    send([{ kind: it.kind, ref: it.ref, version: m.version, action: o }],
      { title: `${optionLabel(o)}?`, description: label, confirm: "Send answer" }, `Answered: ${optionLabel(o)}`);
  // A free answer goes to the message itself, so each question gets the words (decide takes only listed options).
  const free = () => {
    const qs = m.questions ?? [];
    const answer = text.trim();
    return act({ Kind: "answer", MessageID: m.id, Version: m.version, Text: answer, Answers: qs.length === 1 && !qs[0].options?.length ? { [qs[0].id]: answer } : {} },
      { title: `Answer ${m.sender}?`, description: `Your answer: ${answer}`, confirm: "Send answer" }, `Answer sent to ${m.sender}`).then((ok) => ok && reset());
  };
  return (
    <Reasoned open={why === "answer"} text={text} setText={setText} placeholder="Your answer…" onCancel={options.length ? reset : undefined} onSend={free} sendLabel="Send answer" busy={busy}
      alwaysOpen={options.length === 0}>
      {options.map((o) => <Button key={o} variant="outline" className="h-10 min-w-0 whitespace-normal text-left" disabled={busy} onClick={() => choose(o)}>{optionLabel(o)}</Button>)}
      {options.length > 0 && <Button variant="ghost" className="h-10" disabled={busy} onClick={() => setWhy("answer")}>In your own words</Button>}
    </Reasoned>
  );
}

// Reasoned: an item's buttons, or the text box a rejection, a return or a free answer needs.
function Reasoned({ open, alwaysOpen, text, setText, placeholder, onCancel, onSend, sendLabel, busy, destructive, children }: {
  open: boolean; alwaysOpen?: boolean; text: string; setText: (s: string) => void; placeholder: string; onCancel?: () => void; onSend: () => void;
  sendLabel: string; busy: boolean; destructive?: boolean; children: React.ReactNode;
}) {
  if (!open && !alwaysOpen) return <div className="mt-3 flex flex-wrap justify-end gap-2">{children}</div>;
  return (
    <div className="mt-3 space-y-2">
      <Textarea autoFocus={!alwaysOpen} rows={2} value={text} onChange={(e) => setText(e.target.value)} placeholder={placeholder} aria-label={placeholder}
        className="min-h-16 text-base md:text-sm" />
      <div className="flex flex-wrap justify-end gap-2">
        {onCancel && !alwaysOpen && <Button variant="outline" className="h-10" onClick={onCancel}>Cancel</Button>}
        <Button className="h-10" variant={destructive ? "destructive" : "default"} disabled={busy || !text.trim()} onClick={onSend}>{sendLabel}</Button>
      </div>
    </div>
  );
}

// Updates: what the lead reported in the topic; the ones still unread can be marked read here.
function Updates({ topic, items, busy, act }: { topic: TopicView; items: ItemView[]; busy: boolean; act: Act }) {
  const { data: ov } = useOverview();
  const unread = new Set((ov?.inbox ?? []).map((m) => m.id));
  return (
    <div className="mt-4 border-t pt-3">
      <p className="text-xs font-medium text-muted-foreground">Updates</p>
      <ul className="mt-1.5 space-y-2">
        {[...items].sort((a, b) => b.added_at - a.added_at).map((u) => {
          const m = u.message;
          const text = u.note?.what || (m ? peopleText(m) : "");
          return (
            <li key={`${u.kind}:${u.ref}`} className="text-sm">
              <span className="text-xs text-muted-foreground">{m?.sender ?? topic.brought_by} · {ago(u.added_at)}</span>
              {text && <Markdown text={text} className="mt-0.5" />}
              {m && unread.has(m.id) && (
                <Button variant="ghost" size="sm" className="mt-1 h-9" disabled={busy}
                  onClick={() => act({ Kind: "read", MessageID: m.id, Version: m.version }, { title: "Mark as read?", description: text.slice(0, 200), confirm: "Mark read" }, "Marked as read")}>
                  <Check />Mark as read
                </Button>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

// TopicReply: write to the lead in the topic's thread (D9); the answer comes back in the chat and here as an update.
// Writing decides nothing: only the buttons act.
export function TopicReply({ topic, lead }: { topic: TopicView; lead: string }) {
  const [open, setOpen] = useState(false);
  const [text, setText] = useState("");
  const { reply } = useTopicWrite();
  const who = lead || "the lead";
  const post = () => reply.mutate({ topic: topic.id, body: text.trim() }, {
    onSuccess: () => { toast.success(`Sent to ${who}`); setText(""); setOpen(false); },
    onError: (e) => toast.error(e.message),
  });
  return (
    <div className="mt-4 border-t pt-3">
      {!open ? (
        <div className="flex flex-wrap items-center justify-end gap-2">
          <Link href={`/chat?c=${encodeURIComponent(topic.conversation_id ?? "")}`} className="mr-auto inline-flex h-10 items-center text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">Open in chat</Link>
          <Button variant="ghost" className="h-10" onClick={() => setOpen(true)}><MessageSquareReply />Ask {who}</Button>
        </div>
      ) : (
        <div className="space-y-2">
          <Textarea autoFocus rows={2} value={text} onChange={(e) => setText(e.target.value)} placeholder={`A question or a comment for ${who} about this topic`} aria-label={`Message ${who}`}
            className="min-h-16 text-base md:text-sm" maxLength={16384} />
          <p className="text-xs text-muted-foreground">It goes to {who} in this topic&apos;s thread; it decides nothing.</p>
          <div className="flex flex-wrap justify-end gap-2">
            <Button variant="outline" className="h-10" onClick={() => { setOpen(false); setText(""); }}>Cancel</Button>
            <Button className="h-10" disabled={!text.trim() || reply.isPending} onClick={post}><Send />Send</Button>
          </div>
        </div>
      )}
    </div>
  );
}

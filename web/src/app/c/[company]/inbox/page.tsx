"use client";

import { CheckCheck, CheckCircle2, Undo2 } from "lucide-react";
import Link from "@/components/app/link";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { ApprovalCommentOutcome } from "@/components/app/approval-comment";
import { DecisionCard } from "@/components/app/decision-card";
import { ApprovalBadge } from "@/components/app/materials/kinds";
import { AlertCard, ApprovalCard, MessageCard, type InboxHandlers } from "@/components/app/inbox/cards";
import { useInbox } from "@/components/app/inbox/model";
import { TopicCard } from "@/components/app/inbox/topic-card";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { useAction } from "@/lib/api/client";
import type { Action, Approval, Message, Task } from "@/lib/api/types";
import { peopleText } from "@/lib/people-text";

export default function InboxPage() {
  const confirm = useConfirm();
  const action = useAction();
  const [gone, setGone] = useState<Set<string>>(new Set());

  // The same items the nav badge counts (inbox-badge-parity): open topics, then messages, approvals, reviews and
  // alerts outside them.
  const { ov: { data: ov, error, isLoading }, model, ready } = useInbox(gone);
  const { topics, decide, read, reviews, alerts, total } = model;

  // A link to a topic (#topic-<id>, from the chat) scrolls to it once it is listed.
  useEffect(() => {
    const id = typeof window !== "undefined" ? window.location.hash.slice(1) : "";
    if (ready && id.startsWith("topic-")) document.getElementById(id)?.scrollIntoView({ block: "start" });
  }, [ready]);

  // approval-comments-handled: your recent decisions with a comment, with what became of it. Not counted: they wait
  // on the team, not on you.
  const notes = useMemo(() => recentApprovalComments(ov?.approvals ?? [], Date.now() / 1000), [ov?.approvals]);

  const projectName = useMemo(() => Object.fromEntries((ov?.status ?? []).map((s) => [s.project.id, s.project.name])), [ov?.status]);

  // run confirms, hides the item at once and restores it if the action fails.
  const run = async (key: string, a: Action, ask: { title: string; description?: string; confirm: string; destructive?: boolean }, done: string) => {
    if (!(await confirm(ask))) return;
    setGone((s) => new Set(s).add(key));
    action.mutate(a, {
      onSuccess: () => toast.success(done),
      onError: (e) => {
        setGone((s) => { const n = new Set(s); n.delete(key); return n; });
        toast.error(e.message);
      },
    });
  };

  const keyOf = (msg?: Message, ap?: Approval) => (msg ? `m:${msg.id}` : `a:${ap!.id}`);
  const h: InboxHandlers = {
    busy: action.isPending,
    answer: (msg, text, answers) =>
      run(keyOf(msg), { Kind: "answer", MessageID: msg.id, Version: msg.version, Text: text, Answers: answers },
        { title: `Answer ${msg.sender}?`, description: `${msg.questions?.length === 1 ? msg.questions[0].question : peopleText(msg).slice(0, 160)}\n\nYour answer: ${text}`, confirm: "Send answer" }, `Answer sent to ${msg.sender}`),
    // inbox-badge-parity: "I'll send details" keeps the question open and the task waiting; withdrawing lets it go on.
    details: (msg, kind, note) =>
      kind === "promised"
        ? promise(msg, note)
        : run(keyOf(msg), { Kind: "answer", MessageID: msg.id, Version: msg.version, Text: note, Details: "withdrawn" },
          { title: "Withdraw your promise?", description: `${msg.sender} stops waiting for your details and continues without them.`, confirm: "Withdraw" }, `${msg.sender} continues without the details`),
    read: (msg) =>
      run(keyOf(msg), { Kind: "read", MessageID: msg.id, Version: msg.version },
        { title: "Mark as read?", description: peopleText(msg).slice(0, 200), confirm: "Mark read" }, "Marked as read"),
    decide: (kind, ap, msg, reason) =>
      run(keyOf(msg, ap), { Kind: kind, ApprovalID: ap.id, Version: Math.max(ap.version, 1), Text: reason },
        {
          title: `${kind === "approve" ? "Approve" : "Reject"} ${ap.artifact ? ap.artifact.name || ap.artifact.id : ap.call.name}?`,
          description: `${ap.agent} asked${ap.task_id ? ` on ${ap.task_id}` : ""}.\nNote: ${reason}`,
          confirm: kind === "approve" ? "Approve" : "Reject", destructive: kind === "reject",
        }, kind === "approve" ? "Approved" : "Rejected"),
  };

  const review = (t: Task, accept: boolean, feedback?: string) =>
    run(`r:${t.id}`, { Kind: accept ? "accept" : "rework", TaskID: t.id, Version: t.version, Text: feedback },
      accept ? { title: "Accept this work?", description: `“${t.title}” is done. ${t.implementer || t.assignee}'s submission is accepted.`, confirm: "Accept" }
        : { title: "Send it back?", description: `“${t.title}” goes back to ${t.implementer || t.assignee} with your feedback: ${feedback}`, confirm: "Send back" },
      accept ? "Accepted" : "Sent back");

  // A promise keeps the question in the Inbox: it is not hidden, it shows "details promised" after the refresh.
  const promise = async (msg: Message, note: string) => {
    if (!(await confirm({ title: "Send the details later?", description: `The question stays open in your Inbox as “details promised”, and ${msg.sender}'s task waits for them without running again. Answer it (or message ${msg.sender}) with the details when you have them.`, confirm: "I'll send details" }))) return;
    action.mutate({ Kind: "answer", MessageID: msg.id, Version: msg.version, Text: note, Details: "promised" }, {
      onSuccess: () => toast.success(`${msg.sender} waits for your details`),
      onError: (e) => toast.error(e.message),
    });
  };

  const blocking = decide.filter((i) => i.blocking).length + topics.filter((t) => t.blocked?.length).length;

  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader title="Inbox"
        description={!ov ? "What waits on you." : total === 0 ? "Nothing waits on you." : `${total} waiting on you${blocking ? ` · ${blocking} blocking work` : ""}`} />
      {error && !ov ? <ErrorState error={error} />
        : isLoading || !ov || !ready ? <LoadingRows rows={3} />
        : total === 0 ? <EmptyState icon={CheckCircle2} title="You're all caught up" hint="Questions, approvals, reviews, escalations and alerts from your team land here." />
        : (
          <div className="space-y-8">
            {topics.length > 0 && (
              <Section title="Needs your decision" count={topics.length}>
                {topics.map((t) => <TopicCard key={t.id} topic={t} projectName={projectName[t.project_id]} />)}
              </Section>
            )}
            {decide.length > 0 && (
              <Section title={topics.length ? "More to decide" : "Needs your decision"} count={decide.length}>
                {decide.map((i) => i.ap
                  ? <ApprovalCard key={i.key} ap={i.ap} msg={i.msg} projectName={projectName[i.ap.project_id]} h={h} />
                  : i.msg?.decision_id ? <DecisionItem key={i.key} msg={i.msg} projectName={projectName[i.msg.project_id]} />
                  : <MessageCard key={i.key} msg={i.msg!} projectName={projectName[i.msg!.project_id]} h={h} />)}
              </Section>
            )}
            {reviews.length > 0 && (
              <Section title="Reviews for you" count={reviews.length}>
                {reviews.map((t) => <ReviewCard key={t.id} task={t} projectName={projectName[t.project_id]} busy={action.isPending} onReview={review} />)}
              </Section>
            )}
            {read.length > 0 && (
              <Section title="Reports for you" count={read.length}>
                {read.map((i) => <MessageCard key={i.key} msg={i.msg!} projectName={projectName[i.msg!.project_id]} h={h} />)}
              </Section>
            )}
            {alerts.length > 0 && (
              <Section title="Alerts" count={alerts.length}>
                {alerts.map((a) => <AlertCard key={a.id} alert={a} projectName={projectName[a.project_id]} />)}
              </Section>
            )}
          </div>
        )}
      {ov && notes.length > 0 && (
        <div className="mt-8">
          <Section title="Your comments on approvals" count={notes.length}>
            {notes.map((ap) => <ApprovalNote key={ap.id} ap={ap} projectName={projectName[ap.project_id]} />)}
          </Section>
        </div>
      )}
    </div>
  );
}

// recentApprovalComments: decisions of the last week that came with a comment, newest first.
function recentApprovalComments(approvals: Approval[], now: number): Approval[] {
  return approvals.filter((a) => a.comment && a.status !== "PENDING" && (a.decided_at ?? 0) > now - 7 * 86400)
    .sort((a, b) => (b.decided_at ?? 0) - (a.decided_at ?? 0)).slice(0, 10);
}

// ApprovalNote: one decision's comment and its outcome ("done by launch-creative: frame 1 without the window").
function ApprovalNote({ ap, projectName }: { ap: Approval; projectName?: string }) {
  const what = ap.artifact ? `${ap.artifact.name || ap.artifact.id}${ap.artifact.version ? ` · ${ap.artifact.version}` : ""}` : ap.call?.name || "approval";
  return (
    <article className="rounded-2xl border bg-card p-4 shadow-xs">
      <p className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
        <span>{projectName ?? ap.project_id} · {ap.agent} · {what}</span>
        <ApprovalBadge status={ap.status} />
      </p>
      <p className="mt-1.5 text-sm wrap-anywhere">“{ap.decision_reason}”</p>
      <ApprovalCommentOutcome comment={ap.comment} className="mt-1 block" />
      {ap.task_id && <Link href={`/tasks/${encodeURIComponent(ap.task_id)}`} className="mt-2 inline-flex text-xs font-medium underline-offset-4 hover:underline">Open the task</Link>}
    </article>
  );
}

function Section({ title, count, children }: { title: string; count: number; children: React.ReactNode }) {
  return (
    <section aria-label={title}>
      <h2 className="mb-3 flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground">
        {title}<span className="rounded-full bg-muted px-1.5 py-0.5 text-[11px] tabular-nums">{count}</span>
      </h2>
      <div className="space-y-4">{children}</div>
    </section>
  );
}

// ReviewCard: a task submitted for your review; accept it or send it back with what should change.
function ReviewCard({ task, projectName, busy, onReview }: {
  task: Task; projectName?: string; busy: boolean; onReview: (t: Task, accept: boolean, feedback?: string) => void;
}) {
  const [back, setBack] = useState(false);
  const [text, setText] = useState("");
  return (
    <article className="rounded-2xl border bg-card p-4 shadow-xs">
      <p className="text-xs text-muted-foreground">{projectName ?? task.project_id} · from {task.implementer || task.assignee}</p>
      <Link href={`/tasks/${encodeURIComponent(task.id)}`} className="mt-1 block text-[15px] font-medium leading-snug underline-offset-4 hover:underline">{task.title}</Link>
      {task.submission?.summary && <p className="mt-2 line-clamp-4 text-sm whitespace-pre-wrap text-muted-foreground">{task.submission.summary}</p>}
      {back && (
        <Textarea aria-label="What should change" autoFocus rows={3} value={text} onChange={(e) => setText(e.target.value)}
          placeholder="What should change" className="mt-3 text-base md:text-sm" />
      )}
      <div className="mt-3 flex flex-wrap gap-2">
        {!back ? (
          <>
            <Button className="h-10" disabled={busy} onClick={() => onReview(task, true)}><CheckCheck />Accept</Button>
            <Button className="h-10" variant="outline" disabled={busy} onClick={() => setBack(true)}><Undo2 />Send back</Button>
          </>
        ) : (
          <>
            <Button className="h-10" disabled={busy || !text.trim()} onClick={() => onReview(task, false, text.trim())}><Undo2 />Send back</Button>
            <Button className="h-10" variant="outline" onClick={() => { setBack(false); setText(""); }}>Cancel</Button>
          </>
        )}
      </div>
    </article>
  );
}

// DecisionItem: a lead's decision proposal; decided here or in the chat (decision-approval-ux).
function DecisionItem({ msg, projectName }: { msg: Message; projectName?: string }) {
  return (
    <article className="rounded-2xl border bg-card p-4 shadow-xs">
      <p className="text-xs text-muted-foreground">{projectName ?? msg.project_id} · proposed by {msg.sender}</p>
      <DecisionCard id={msg.decision_id!} />
    </article>
  );
}

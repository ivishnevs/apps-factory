"use client";

import { CheckCircle2 } from "lucide-react";
import { useMemo, useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { ApprovalCard, answerable, MessageCard, type InboxHandlers } from "@/components/app/inbox/cards";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { useAction, useOverview } from "@/lib/api/client";
import type { Action, Approval, Message } from "@/lib/api/types";

type Item = { key: string; msg?: Message; ap?: Approval; blocking: boolean; due: number; at: number; decide: boolean };

// Blocking first, then the soonest deadline, then the newest.
const order = (a: Item, b: Item) =>
  Number(b.blocking) - Number(a.blocking) || (a.due || Infinity) - (b.due || Infinity) || b.at - a.at;

export default function InboxPage() {
  const { data: ov, error, isLoading } = useOverview();
  const confirm = useConfirm();
  const action = useAction();
  const [gone, setGone] = useState<Set<string>>(new Set());

  const { decide, read } = useMemo(() => {
    const inbox = ov?.inbox ?? [];
    const approvals = (ov?.approvals ?? []).filter((a) => a.status === "PENDING" || !a.status);
    const apById = new Map(approvals.map((a) => [a.id, a]));
    const used = new Set<string>();
    const items: Item[] = inbox.map((m) => {
      const ap = (m.approval_id && apById.get(m.approval_id)) || approvals.find((a) => a.message_id === m.id);
      if (ap) used.add(ap.id);
      return { key: `m:${m.id}`, msg: m, ap: ap || undefined, blocking: m.blocking, due: ap ? ap.expires_at : m.deadline ?? 0, at: m.created_at, decide: !!ap || answerable(m) };
    });
    for (const ap of approvals) {
      if (!used.has(ap.id)) items.push({ key: `a:${ap.id}`, ap, blocking: false, due: ap.expires_at, at: ap.created_at, decide: true });
    }
    const live = items.filter((i) => !gone.has(i.key));
    return { decide: live.filter((i) => i.decide).sort(order), read: live.filter((i) => !i.decide).sort((a, b) => b.at - a.at) };
  }, [ov?.inbox, ov?.approvals, gone]);

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
        { title: `Answer ${msg.sender}?`, description: `${msg.questions?.length === 1 ? msg.questions[0].question : msg.body.slice(0, 160)}\n\nYour answer: ${text}`, confirm: "Send answer" }, `Answer sent to ${msg.sender}`),
    read: (msg) =>
      run(keyOf(msg), { Kind: "read", MessageID: msg.id, Version: msg.version },
        { title: "Mark as read?", description: msg.body.slice(0, 200), confirm: "Mark read" }, "Marked as read"),
    decide: (kind, ap, msg, reason) =>
      run(keyOf(msg, ap), { Kind: kind, ApprovalID: ap.id, Version: Math.max(ap.version, 1), Text: reason },
        {
          title: `${kind === "approve" ? "Approve" : "Reject"} ${ap.artifact ? ap.artifact.name || ap.artifact.id : ap.call.name}?`,
          description: `${ap.agent} asked${ap.task_id ? ` on ${ap.task_id}` : ""}.\nNote: ${reason}`,
          confirm: kind === "approve" ? "Approve" : "Reject", destructive: kind === "reject",
        }, kind === "approve" ? "Approved" : "Rejected"),
  };

  const total = decide.length + read.length;
  const blocking = decide.filter((i) => i.blocking).length;

  return (
    <div className="mx-auto max-w-3xl">
      <PageHeader title="Inbox"
        description={!ov ? "What waits on you." : total === 0 ? "Nothing waits on you." : `${total} waiting on you${blocking ? ` · ${blocking} blocking work` : ""}`} />
      {error && !ov ? <ErrorState error={error} />
        : isLoading || !ov ? <LoadingRows rows={3} />
        : total === 0 ? <EmptyState icon={CheckCircle2} title="You're all caught up" hint="Questions, approvals and escalations from your team land here." />
        : (
          <div className="space-y-8">
            {decide.length > 0 && (
              <Section title="Needs your decision" count={decide.length}>
                {decide.map((i) => i.ap
                  ? <ApprovalCard key={i.key} ap={i.ap} msg={i.msg} projectName={projectName[i.ap.project_id]} h={h} />
                  : <MessageCard key={i.key} msg={i.msg!} projectName={projectName[i.msg!.project_id]} h={h} />)}
              </Section>
            )}
            {read.length > 0 && (
              <Section title="To read" count={read.length}>
                {read.map((i) => <MessageCard key={i.key} msg={i.msg!} projectName={projectName[i.msg!.project_id]} h={h} />)}
              </Section>
            )}
          </div>
        )}
    </div>
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

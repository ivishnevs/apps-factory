"use client";

import { useMemo } from "react";
import { useOwnerAction } from "@/components/app/nodes/owner-action";
import { peopleText } from "@/lib/people-text";
import { useOverview } from "@/lib/api/client";
import type { Approval, Message, Overview } from "@/lib/api/types";
import { ApprovalCard, answerable, MessageCard, type InboxHandlers } from "./cards";

// agent-questions-in-chat: the open questions and approvals of the Inbox, answerable where they matter too — in the
// owner's chat with the project's lead and on the asking task. An answer anywhere closes the item everywhere (the
// same message and compare-and-set as the Inbox).

export interface OpenItem { key: string; msg?: Message; ap?: Approval; at: number }

export const itemProject = (i: OpenItem) => i.ap?.project_id ?? i.msg?.project_id ?? "";
export const itemAgent = (i: OpenItem) => i.ap?.agent ?? i.msg?.sender ?? "";
export const itemTask = (i: OpenItem) => i.ap?.task_id || i.msg?.task_id || "";

// openItems: the questions and approvals waiting for people, oldest first (decision proposals and reports stay in
// the Inbox and the lead's own chat).
export function openItems(ov?: Overview): OpenItem[] {
  if (!ov) return [];
  // A delegated approval is the delegate's until it escalates (approval-delegation).
  const approvals = (ov.approvals ?? []).filter((a) => (a.status === "PENDING" || !a.status) && (!a.delegate || a.escalated));
  const used = new Set<string>();
  const out: OpenItem[] = [];
  for (const m of ov.inbox ?? []) {
    const ap = approvals.find((a) => a.id === m.approval_id || a.message_id === m.id);
    if (ap) used.add(ap.id);
    if (ap || answerable(m)) out.push({ key: `m:${m.id}`, msg: m, ap, at: m.created_at });
  }
  for (const ap of approvals) if (!used.has(ap.id)) out.push({ key: `a:${ap.id}`, ap, at: ap.created_at });
  return out.sort((a, b) => a.at - b.at);
}

// useOpenItemHandlers answers and decides like the Inbox: confirm, act, toast; the overview refresh removes the item.
export function useOpenItemHandlers(): InboxHandlers {
  const { run, pending } = useOwnerAction();
  return {
    busy: pending,
    answer: (msg, text, answers) =>
      void run({ Kind: "answer", MessageID: msg.id, Version: msg.version, Text: text, Answers: answers },
        { title: `Answer ${msg.sender}?`, description: `${msg.questions?.length === 1 ? msg.questions[0].question : peopleText(msg).slice(0, 160)}\n\nYour answer: ${text}`, confirm: "Send answer" },
        `Answer sent to ${msg.sender}`),
    // inbox-badge-parity: "I'll send details" keeps the question open; withdrawing lets the task go on.
    details: (msg, kind, note) =>
      void run({ Kind: "answer", MessageID: msg.id, Version: msg.version, Text: note, Details: kind },
        kind === "promised"
          ? { title: "Send the details later?", description: `The question stays open as “details promised”, and ${msg.sender}'s task waits for them without running again.`, confirm: "I'll send details" }
          : { title: "Withdraw your promise?", description: `${msg.sender} stops waiting for your details and continues without them.`, confirm: "Withdraw" },
        kind === "promised" ? `${msg.sender} waits for your details` : `${msg.sender} continues without the details`),
    read: (msg) => void run({ Kind: "read", MessageID: msg.id, Version: msg.version }, { title: "Mark as read?", description: peopleText(msg).slice(0, 200), confirm: "Mark read" }, "Marked as read"),
    decide: (kind, ap, _msg, reason) =>
      void run({ Kind: kind, ApprovalID: ap.id, Version: Math.max(ap.version, 1), Text: reason },
        {
          title: `${kind === "approve" ? "Approve" : "Reject"} ${ap.artifact ? ap.artifact.name || ap.artifact.id : ap.call.name}?`,
          description: `${ap.agent} asked${ap.task_id ? ` on ${ap.task_id}` : ""}.\nNote: ${reason}`,
          confirm: kind === "approve" ? "Approve" : "Reject", destructive: kind === "reject",
        }, kind === "approve" ? "Approved" : "Rejected"),
  };
}

export function OpenItemCard({ item, projectName, h }: { item: OpenItem; projectName?: string; h: InboxHandlers }) {
  return item.ap
    ? <ApprovalCard ap={item.ap} msg={item.msg} projectName={projectName} h={h} />
    : <MessageCard msg={item.msg!} projectName={projectName} h={h} />;
}

// TaskOpenItems: the task's open question or approval on the task page, answerable in place.
export function TaskOpenItems({ taskId, projectName }: { taskId: string; projectName?: string }) {
  const { data: ov } = useOverview();
  const items = useMemo(() => openItems(ov).filter((i) => itemTask(i) === taskId), [ov, taskId]);
  const h = useOpenItemHandlers();
  if (items.length === 0) return null;
  return (
    <section aria-label="Waiting for your answer" className="space-y-3">
      <h2 className="text-sm font-semibold tracking-tight">Waiting for your answer</h2>
      {items.map((i) => <OpenItemCard key={i.key} item={i} projectName={projectName} h={h} />)}
    </section>
  );
}

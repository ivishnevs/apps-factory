import type { ActiveAlert, Approval, Message, Overview, Task } from "@/lib/api/types";
import { answerable } from "./cards";

// The Inbox's items, computed once for the page and for the nav badge (inbox-badge-parity): the badge counts exactly
// what the page lists.

export type InboxItem = { key: string; msg?: Message; ap?: Approval; blocking: boolean; due: number; at: number; decide: boolean };

export interface InboxModel {
  decide: InboxItem[];
  read: InboxItem[];
  reviews: Task[];
  alerts: ActiveAlert[];
  total: number;
}

// Blocking first, then the soonest deadline, then the newest.
const order = (a: InboxItem, b: InboxItem) =>
  Number(b.blocking) - Number(a.blocking) || (a.due || Infinity) - (b.due || Infinity) || b.at - a.at;

const none = new Set<string>();

// inboxModel builds the Inbox from the overview; gone hides items an action just took (restored if it fails).
export function inboxModel(ov: Overview | undefined, gone: Set<string> = none): InboxModel {
  const inbox = ov?.inbox ?? [];
  // A delegated approval is the delegate's until it escalates (approval-delegation).
  const approvals = (ov?.approvals ?? []).filter((a) => (a.status === "PENDING" || !a.status) && (!a.delegate || a.escalated));
  const apById = new Map(approvals.map((a) => [a.id, a]));
  const used = new Set<string>();
  const items: InboxItem[] = inbox.map((m) => {
    const ap = (m.approval_id && apById.get(m.approval_id)) || approvals.find((a) => a.message_id === m.id);
    if (ap) used.add(ap.id);
    return { key: `m:${m.id}`, msg: m, ap: ap || undefined, blocking: m.blocking, due: ap ? ap.expires_at : m.deadline ?? 0, at: m.created_at, decide: !!ap || answerable(m) };
  });
  for (const ap of approvals) {
    if (!used.has(ap.id)) items.push({ key: `a:${ap.id}`, ap, blocking: false, due: ap.expires_at, at: ap.created_at, decide: true });
  }
  // Only what is open (decision-approval-ux): questions, approvals, decision proposals, reports asking for action.
  const live = items.filter((i) => !gone.has(i.key));
  const decide = live.filter((i) => i.decide || !!i.msg?.decision_id).sort(order);
  const read = live.filter((i) => !i.decide && !i.msg?.decision_id).sort((a, b) => b.at - a.at);
  // Work submitted for your review (human-review-visibility 2.3): tasks in review that wait for you.
  const reviews = (ov?.tasks ?? []).filter((t) => t.status === "IN_REVIEW" && t.clearer?.who === "owner" && !gone.has(`r:${t.id}`))
    .sort((a, b) => b.updated_at - a.updated_at);
  // Active task alerts and runs to inspect (GET /alerts), newest first; an older Core sends none.
  const alerts = (ov?.alerts ?? []).filter((a) => !gone.has(`al:${a.id}`));
  return { decide, read, reviews, alerts, total: decide.length + read.length + reviews.length + alerts.length };
}

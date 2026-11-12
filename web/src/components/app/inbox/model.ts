import { useMemo } from "react";
import { useOverview, useTopics } from "@/lib/api/client";
import type { ActiveAlert, Approval, Message, Overview, Task } from "@/lib/api/types";
import type { TopicView } from "@/lib/api/types-topics";
import { answerable } from "./cards";

// The Inbox's items, computed once for the page and for the nav badge (inbox-badge-parity): the badge counts exactly
// what the page lists. Open people topics come first (people-topics D9); what belongs to a listed topic is not listed
// again on its own.

export type InboxItem = { key: string; msg?: Message; ap?: Approval; blocking: boolean; due: number; at: number; decide: boolean };

export interface InboxModel {
  topics: TopicView[];
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

// TopicRefs: what the listed topics hold (topic ids, message, approval, decision and task ids), so nothing shows twice.
export interface TopicRefs { topics: Set<string>; messages: Set<string>; approvals: Set<string>; decisions: Set<string>; reviews: Set<string> }

export function topicRefs(topics: TopicView[]): TopicRefs {
  const r: TopicRefs = { topics: new Set(), messages: new Set(), approvals: new Set(), decisions: new Set(), reviews: new Set() };
  for (const t of topics) {
    r.topics.add(t.id);
    for (const it of t.items) {
      if (it.message) r.messages.add(it.message.id);
      switch (it.kind) {
        case "approval": r.approvals.add(it.ref); if (it.approval?.message_id) r.messages.add(it.approval.message_id); break;
        case "decision": r.decisions.add(it.ref); if (it.decision?.message_id) r.messages.add(it.decision.message_id); break;
        case "review": r.reviews.add(it.ref); break;
        default: r.messages.add(it.ref);
      }
    }
  }
  return r;
}

// inTopic: the message or approval is shown in one of the listed topics.
export const inTopic = (r: TopicRefs, msg?: Message, ap?: Approval) =>
  !!(msg && (r.messages.has(msg.id) || (msg.topic_id && r.topics.has(msg.topic_id)) || (msg.decision_id && r.decisions.has(msg.decision_id)) || (msg.approval_id && r.approvals.has(msg.approval_id))))
  || !!(ap && (r.approvals.has(ap.id) || r.messages.has(ap.message_id)));

// Topics with something waiting for people, the most urgent first: blocking work, then the newest.
const topicOrder = (a: TopicView, b: TopicView) => Number(!!b.blocked?.length) - Number(!!a.blocked?.length) || b.at - a.at;

// inboxModel builds the Inbox from the overview and the open topics; gone hides items an action just took (restored
// if it fails).
export function inboxModel(ov: Overview | undefined, openTopics: TopicView[] = [], gone: Set<string> = none): InboxModel {
  const topics = openTopics.filter((t) => t.open && !gone.has(`t:${t.id}`)).sort(topicOrder);
  const refs = topicRefs(openTopics.filter((t) => t.open));
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
  const live = items.filter((i) => !gone.has(i.key) && !inTopic(refs, i.msg, i.ap));
  const decide = live.filter((i) => i.decide || !!i.msg?.decision_id).sort(order);
  const read = live.filter((i) => !i.decide && !i.msg?.decision_id).sort((a, b) => b.at - a.at);
  // Work submitted for your review (human-review-visibility 2.3): tasks in review that wait for you.
  const reviews = (ov?.tasks ?? []).filter((t) => t.status === "IN_REVIEW" && t.clearer?.who === "owner" && !gone.has(`r:${t.id}`) && !refs.reviews.has(t.id))
    .sort((a, b) => b.updated_at - a.updated_at);
  // Active task alerts and runs to inspect (GET /alerts), newest first; an older Core sends none.
  const alerts = (ov?.alerts ?? []).filter((a) => !gone.has(`al:${a.id}`));
  return { topics, decide, read, reviews, alerts, total: topics.length + decide.length + read.length + reviews.length + alerts.length };
}

// useInbox is the Inbox for the page and the nav badge: the overview and the open topics. ready: both arrived (or
// the topics failed: the items are then listed on their own), so the page never lists an item twice while loading.
export function useInbox(gone: Set<string> = none) {
  const ov = useOverview();
  const topics = useTopics();
  const model = useMemo(() => inboxModel(ov.data, topics.data, gone), [ov.data, topics.data, gone]);
  return { ov, topics, model, ready: !!ov.data && (!!topics.data || !!topics.error) };
}

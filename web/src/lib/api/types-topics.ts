import type { Approval, Decision, Message } from "./types";

// People topics (people-topics D2–D6): what people decide, grouped. Mirrors internal/messaging Brief and ItemNote and
// internal/storage TopicView, ItemView and TaskRef. People read the brief and the notes, never ids, hashes or JSON.

export interface Brief {
  title?: string; ask?: string; after?: string; recommendation?: string; changes?: string; scope?: string; context?: string;
}
export interface ItemNote { what?: string; changes?: string }

export interface TaskRef { id: string; title: string; status: string; assignee?: string }

export type TopicItemKind = "approval" | "question" | "escalation" | "decision" | "review" | "update";

export interface ItemView {
  kind: TopicItemKind; ref: string; lineage?: string; version?: string; note: ItemNote; brought_by?: string;
  // open: current; replaced: a newer version of its line came; update: a notification attached to the topic.
  state: "open" | "replaced" | "update"; added_at: number;
  waits: boolean; status: string; actions?: string[];
  approval?: Approval; message?: Message; decision?: Decision; task?: TaskRef;
}

export interface TopicView {
  id: string; project_id: string; key: string; status: "open" | "settled"; brief: Brief; needs_brief?: boolean;
  brought_by: string; conversation_id?: string; created_at: number; updated_at: number;
  open: boolean; waiting: number; items: ItemView[]; blocked?: TaskRef[]; tasks?: TaskRef[]; at: number;
}

// TopicDecision is one item decided from a topic (POST /topics/{id}/decide; all or none).
export interface TopicDecision {
  kind: TopicItemKind; ref: string; version: number; action: string; reason?: string; answers?: Record<string, unknown>;
  // A choice approval (artifact-choice-approvals): choose takes option (from 1), changes may name options.
  option?: number; options?: number[];
}

export type PeopleLanguage = "en" | "ru";

"use client";

import { useQueries } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useState } from "react";
import { api, useOverview } from "@/lib/api/client";
import type { Message, Overview } from "@/lib/api/types";
import { defaultReportOf, directReports, ownerConversation, type TaskWithKind } from "@/lib/api/types-ops";

// A conversation of the owner with one direct report in one project (owner-<agent>-<project>).
export interface Chat {
  id: string; agent: string; role?: string; project: string; projectName: string; isDefault: boolean;
  messages: Message[]; last?: Message; unread: number; waiting: number; replyingRun?: string;
  loading: boolean; error?: unknown;
}

const SEEN_KEY = "factory.chat.seen";

function readSeen(): Record<string, number> {
  try { return JSON.parse(localStorage.getItem(SEEN_KEY) ?? "{}") ?? {}; } catch { return {}; }
}

// replyingRun: the live run of the report's conversation task in the project (it is writing a reply).
function replyingRun(ov: Overview, agent: string, project: string): string | undefined {
  const runs = ov.status.flatMap((p) => p.runs ?? []);
  for (const t of ov.tasks as TaskWithKind[]) {
    if (t.kind === "conversation" && t.assignee === agent && t.project_id === project) {
      const r = runs.find((x) => x.task_id === t.id);
      if (r) return r.id;
    }
  }
  return undefined;
}

// useChats reads every owner conversation (each direct report in each project), newest first.
export function useChats() {
  const ov = useOverview();
  const data = ov.data;
  const def = data ? defaultReportOf(data) : "";
  const reports = useMemo(() => (data ? directReports(data.agents ?? [], def) : []), [data, def]);
  const projects = useMemo(() => (data?.status ?? []).map((s) => s.project), [data]);
  const pairs = useMemo(() => projects.flatMap((p) => reports.map((a) => ({ agent: a, project: p.id, projectName: p.name || p.id }))), [projects, reports]);
  const results = useQueries({
    queries: pairs.map((p) => {
      const conversation = ownerConversation(p.agent, p.project);
      return {
        queryKey: ["messages", conversation],
        queryFn: () => api<Message[] | null>(`/messages?conversation=${encodeURIComponent(conversation)}`),
        refetchInterval: 5000,
      };
    }),
  });

  const [seen, setSeen] = useState<Record<string, number>>({});
  useEffect(() => setSeen(readSeen()), []);
  const markSeen = useCallback((id: string, at: number) => {
    setSeen((s) => {
      if ((s[id] ?? 0) >= at) return s;
      const next = { ...s, [id]: at };
      try { localStorage.setItem(SEEN_KEY, JSON.stringify(next)); } catch { /* private mode */ }
      return next;
    });
  }, []);

  const chats: Chat[] = pairs.map((p, i) => {
    const r = results[i];
    const id = ownerConversation(p.agent, p.project);
    const messages = [...(r?.data ?? [])].sort((a, b) => a.created_at - b.created_at);
    // Unread: the report's messages newer than the owner's own last message and than what was seen here.
    let floor = seen[id] ?? 0;
    for (const m of messages) if (m.sender === "owner") floor = Math.max(floor, m.created_at);
    return {
      id, ...p, role: data?.agents.find((a) => a.name === p.agent)?.role, isDefault: p.agent === def,
      messages, last: messages.at(-1),
      unread: messages.filter((m) => m.sender !== "owner" && m.created_at > floor).length,
      waiting: messages.filter((m) => m.sender !== "owner" && m.message_type === "question" && m.status === "PENDING").length,
      replyingRun: data ? replyingRun(data, p.agent, p.project) : undefined,
      loading: !!r?.isLoading, error: r?.error ?? undefined,
    };
  });
  chats.sort((a, b) => (b.last?.created_at ?? 0) - (a.last?.created_at ?? 0) || a.project.localeCompare(b.project) || reports.indexOf(a.agent) - reports.indexOf(b.agent));
  return { overview: ov, chats, reports, def, projects, markSeen };
}

"use client";

import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import type { Action, CostView, FeedItem, Meta, Message, Overview, Page, Run, RunEvent, TaskDetail, ToolCall } from "./types";
import type { Material, MaterialPage } from "./types-materials";

// Every request goes to this app's /api/*, which Next proxies to the owner API (next.config.ts). The browser
// never holds a token; when the SaaS adds sign-in, the session cookie rides along (credentials: same-origin).
export class ApiError extends Error {
  constructor(message: string, readonly status: number) { super(message); }
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api${path}`, { credentials: "same-origin", ...init });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(body?.error ?? `HTTP ${res.status}`, res.status);
  return body as T;
}

const qs = (p: Record<string, string | number | boolean | undefined>) => {
  const s = new URLSearchParams();
  for (const [k, v] of Object.entries(p)) if (v !== undefined && v !== "" && v !== false) s.set(k, v === true ? "1" : String(v));
  const t = s.toString();
  return t ? `?${t}` : "";
};

// The overview is everything the shell and the dashboard show; it refreshes every 5 s.
export const useOverview = () => useQuery({ queryKey: ["overview"], queryFn: () => api<Overview>("/overview"), refetchInterval: 5000 });
export const useMeta = () => useQuery({ queryKey: ["meta"], queryFn: () => api<Meta>("/meta"), staleTime: Infinity });
export const useTask = (id: string) => useQuery({ queryKey: ["task", id], queryFn: () => api<TaskDetail>(`/tasks/${id}`), refetchInterval: 5000 });
export const useRun = (id: string) => useQuery({ queryKey: ["run", id], queryFn: () => api<Run>(`/runs/${id}`), refetchInterval: 5000 });
export const useRunEvents = (id: string) => useQuery({ queryKey: ["run-events", id], queryFn: () => api<RunEvent[]>(`/runs/${id}/events`) });
export const useToolCalls = (id: string) => useQuery({ queryKey: ["tool-calls", id], queryFn: () => api<ToolCall[]>(`/runs/${id}/toolcalls`) });
export const useMessages = (conversation: string) =>
  useQuery({ queryKey: ["messages", conversation], queryFn: () => api<Message[]>(`/messages${qs({ conversation })}`), refetchInterval: 5000, enabled: !!conversation });
export const useCosts = (since: string) => useQuery({ queryKey: ["costs", since], queryFn: () => api<CostView>(`/costs${qs({ since })}`) });

// Task materials: what a task produced (refreshes slowly; materials only appear when work is reported), and
// the Library across the company, newest first, paged with next_before.
export const useTaskMaterials = (id: string) =>
  useQuery({ queryKey: ["materials", "task", id], queryFn: () => api<Material[]>(`/tasks/${encodeURIComponent(id)}/materials`), refetchInterval: 30000 });
export interface LibraryFilter { project?: string; kind?: string; q?: string }
export const useLibrary = (f: LibraryFilter, limit = 50) =>
  useInfiniteQuery({
    queryKey: ["materials", "library", f, limit],
    queryFn: ({ pageParam }) => api<MaterialPage>(`/materials${qs({ ...f, limit, before: pageParam || undefined })}`),
    initialPageParam: 0,
    getNextPageParam: (p) => (p.has_more ? p.next_before : undefined),
  });

export interface FeedFilter { project?: string; task?: string; subtree?: string; attention?: boolean }
export const useFeed = (f: FeedFilter, limit = 100) =>
  useQuery({ queryKey: ["feed", f, limit], queryFn: () => api<Page<FeedItem>>(`/feed${qs({ ...f, limit })}`) });

// useAction runs an owner action and refreshes what it may change. Confirm in the UI before calling it.
export function useAction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (a: Action) =>
      api<{ result: string }>("/action", { method: "POST", headers: { "Content-Type": "application/json", "X-Factory-Web": "1" }, body: JSON.stringify(a) }),
    onSettled: () => qc.invalidateQueries(),
  });
}

// useStream follows a server-sent event stream (/api/stream/feed or /api/stream/run/{id}); events are "feed"
// (FeedItem), "run" (RunEvent), "end" ({outcome}) and "state" ({connected, err}). EventSource reconnects by itself.
export function useStream<T>(path: string | null, kind: "feed" | "run", onItem: (item: T) => void) {
  const [connected, setConnected] = useState(false);
  const [ended, setEnded] = useState<string | null>(null);
  const cb = useRef(onItem);
  cb.current = onItem;
  useEffect(() => {
    if (!path) return;
    const es = new EventSource(`/api${path}`);
    es.addEventListener(kind, (e) => cb.current(JSON.parse((e as MessageEvent).data)));
    es.addEventListener("state", (e) => setConnected(!!JSON.parse((e as MessageEvent).data).connected));
    es.addEventListener("end", (e) => { setEnded(JSON.parse((e as MessageEvent).data).outcome ?? "ended"); es.close(); });
    es.onerror = () => setConnected(false);
    return () => es.close();
  }, [path, kind]);
  return { connected, ended };
}

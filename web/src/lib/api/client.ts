"use client";

import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import type { Action, AgentRuns, CostView, FeedItem, Meta, Message, Overview, Page, Run, RunEvent, TaskDetail, ToolCall } from "./types";
import type { Material, MaterialPage } from "./types-materials";
import type { CatalogDocument, CatalogListing, CatalogVersion, Company, RuntimeView, Teams } from "./types-catalog";
import type { CompanyInfo, Me } from "./types-account";
import type { AgentDoc, AgentPreset, Setup, TeamPreset } from "./types-presets";
import type { TopicDecision, TopicView } from "./types-topics";
import { csrf, currentCompany } from "@/lib/company";

// Every request goes to this app's /api/*, which Next proxies to the owner API (next.config.ts). The browser
// never holds a token: signed in, the session cookie rides along (credentials: same-origin), every call names the
// page's company and every change carries the CSRF header (user-accounts D5, D6).
export class ApiError extends Error {
  // field names the form field a refusal is about (team-builder: {"field", "error"}).
  constructor(message: string, readonly status: number, readonly reason?: string, readonly field?: string) { super(message); }
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  const company = currentCompany();
  if (company && !headers.has("X-Factory-Company")) headers.set("X-Factory-Company", company);
  const method = (init?.method ?? "GET").toUpperCase();
  if (method !== "GET" && method !== "HEAD") headers.set("X-Factory-CSRF", csrf());
  const res = await fetch(`/api${path}`, { credentials: "same-origin", ...init, headers });
  const text = await res.text();
  let body: { error?: string; reason?: string; field?: string } = {};
  try { body = text ? JSON.parse(text) : {}; } catch { body = { error: text.trim() }; }
  if (res.status === 401 && typeof window !== "undefined" && !window.location.pathname.startsWith("/signin")) {
    // Signed out or the session expired: back to the sign-in, then here again.
    window.location.assign(`/signin?return=${encodeURIComponent(window.location.pathname + window.location.search)}${body?.reason === "session_expired" ? "&expired=1" : ""}`);
  }
  if (!res.ok) throw new ApiError(body?.error || `HTTP ${res.status}`, res.status, body?.reason, body?.field || undefined);
  return body as T;
}

// core calls an owner route of Core as it is (/api/core/*: /me, /company, secrets, nodes, skills, teams).
export function core<T>(path: string, init?: RequestInit): Promise<T> {
  const write = init?.method && init.method !== "GET";
  const headers = new Headers(init?.headers);
  if (write) { headers.set("X-Factory-Web", "1"); if (init?.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json"); }
  return api<T>(`/core${path}`, { ...init, headers });
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
// taskQuery is a task's details; the board prefetches them under the same key (task-board-ux 2.2).
export const taskQuery = (id: string) => ({ queryKey: ["task", id], queryFn: () => api<TaskDetail>(`/tasks/${id}`) });
export const useTask = (id: string) => useQuery({ ...taskQuery(id), refetchInterval: 5000 });
// usePrefetchTask fetches a task's details ahead of opening it, unless they were fetched in the last few seconds.
export function usePrefetchTask() {
  const qc = useQueryClient();
  return useCallback((id: string) => { void qc.prefetchQuery({ ...taskQuery(id), staleTime: 5000 }); }, [qc]);
}
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
// artifact narrows the Library to that artifact's card (artifact-links: a message links its artifacts there).
export interface LibraryFilter { project?: string; kind?: string; q?: string; artifact?: string }
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

// The company's catalog (company-self-service): the team, documents and history; writes refresh them.
// work-and-conversation-runs: each agent's live and queued runs per kind.
export const useAgentRuns = () => useQuery({ queryKey: ["agent-runs"], queryFn: () => api<Record<string, AgentRuns> | null>("/agents/runs"), refetchInterval: 5000 });
export const useTeams = () => useQuery({ queryKey: ["catalog", "teams"], queryFn: () => api<Teams>("/catalog/teams"), staleTime: 30_000 });
export const useCatalog = () => useQuery({ queryKey: ["catalog", "listing"], queryFn: () => api<CatalogListing>("/catalog") });
export const useCatalogHistory = () => useQuery({ queryKey: ["catalog", "history"], queryFn: () => api<CatalogVersion[]>("/catalog/history") });
export const useCatalogDocument = (path: string | null, version?: number) =>
  useQuery({ queryKey: ["catalog", "document", path, version], queryFn: () => api<CatalogDocument>(`/catalog/document${qs({ path: path ?? "", version })}`), enabled: !!path });
export const useTemplates = () => useQuery({ queryKey: ["templates"], queryFn: () => api<string[]>("/templates"), staleTime: Infinity });
export const useCompanies = () => useQuery({ queryKey: ["companies"], queryFn: () => api<Company[]>("/companies"), retry: false });

// usePost posts JSON to an owner route that changes the catalog or companies (X-Factory-Web: see useAction).
export function usePost<T>(path: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: unknown) =>
      api<T>(path, { method: "POST", headers: { "Content-Type": "application/json", "X-Factory-Web": "1" }, body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["catalog"] }).then(() => qc.invalidateQueries({ queryKey: ["companies"] })),
  });
}

// The signed-in person and their companies (user-accounts 4.3); the page's company with the person's role.
export const useMe = () => useQuery({ queryKey: ["me"], queryFn: () => core<Me>("/me"), staleTime: 60_000, retry: false });
export const useCompanyInfo = () => useQuery({ queryKey: ["company-info"], queryFn: () => core<CompanyInfo>("/company"), staleTime: 60_000,
  retry: (n, e) => !(e instanceof ApiError && e.status === 404) && n < 1 });

// useCore sends a change to an owner route of Core (/api/core/*) and refreshes what it may change.
export function useCore<T>(method: "POST" | "PUT" | "PATCH" | "DELETE", path: string, refresh: unknown[][] = []) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body?: unknown) => core<T>(path, { method, body: body === undefined ? undefined : JSON.stringify(body) }),
    onSuccess: () => Promise.all(refresh.map((queryKey) => qc.invalidateQueries({ queryKey }))),
  });
}

// People topics (people-topics D5, D9): the open topics of the Inbox (refreshed with the overview) and one topic. A
// Core without topics (404) has none: the Inbox shows its items as before.
const noTopics = (e: unknown) => { if (e instanceof ApiError && e.status === 404) return [] as TopicView[]; throw e; };
export const useTopics = () =>
  useQuery({ queryKey: ["topics", "open"], queryFn: () => core<TopicView[] | null>("/topics?open=1").then((v) => v ?? [], noTopics), refetchInterval: 5000 });
export const useTopic = (id: string | undefined) =>
  useQuery({ queryKey: ["topic", id], queryFn: () => core<TopicView>(`/topics/${encodeURIComponent(id ?? "")}`), enabled: !!id, staleTime: 60_000, retry: false });

// useTopicWrite decides items of a topic together (all or none) or writes in its thread; both refresh the Inbox and chats.
export function useTopicWrite() {
  const qc = useQueryClient();
  const refresh = () => Promise.all([["topics"], ["topic"], ["overview"], ["messages"]].map((queryKey) => qc.invalidateQueries({ queryKey })));
  const decide = useMutation({
    mutationFn: ({ topic, items }: { topic: string; items: TopicDecision[] }) =>
      core<{ decided: string[] }>(`/topics/${encodeURIComponent(topic)}/decide`, { method: "POST", body: JSON.stringify({ items, request_id: requestId() }) }),
    onSettled: refresh,
  });
  const reply = useMutation({
    mutationFn: ({ topic, body }: { topic: string; body: string }) =>
      core<Message>(`/topics/${encodeURIComponent(topic)}/messages`, { method: "POST", body: JSON.stringify({ body, request_id: requestId() }) }),
    onSettled: refresh,
  });
  return { decide, reply };
}

const requestId = () => (typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(36).slice(2)}`);

// The team builder (team-builder): presets, an agent as fields, and what keeps the company's work from running.
export const useAgentPresets = () => useQuery({ queryKey: ["presets", "agents"], queryFn: () => core<AgentPreset[]>("/presets/agents"), staleTime: Infinity });
export const useTeamPresets = () => useQuery({ queryKey: ["presets", "teams"], queryFn: () => core<TeamPreset[]>("/presets/teams"), staleTime: Infinity });
export const useAgentDoc = (name: string | null) =>
  useQuery({ queryKey: ["catalog", "agent", name], queryFn: () => core<AgentDoc>(`/catalog/agents/${encodeURIComponent(name ?? "")}`), enabled: !!name });
// agent-effort-in-web: the runtimes with their efforts and offered models (static per Core build).
export const useRuntimes = () => useQuery({ queryKey: ["catalog", "runtimes"], queryFn: () => core<RuntimeView[]>("/catalog/runtimes"), staleTime: Infinity });
export const useSetup = () => useQuery({ queryKey: ["catalog", "setup"], queryFn: () => core<Setup>("/setup"), refetchInterval: 15_000 });

// signOut ends the session and goes to the sign-in page.
export async function signOut() {
  await fetch("/auth/logout", { method: "POST", credentials: "same-origin", headers: { "X-Factory-CSRF": csrf() } }).catch(() => undefined);
  window.location.assign("/signin");
}

"use client";

import { useQuery } from "@tanstack/react-query";
import { core } from "@/lib/api/client";

// Company secrets and agents' secret uses (core-secret-store, agent-secrets-ui). Values never reach the browser.

export type SecretKind = "string" | "file";
export interface Secret { name: string; kind?: SecretKind; version?: number; updated_by?: string; updated_at?: number; used_by?: string[] }
// One entry of an agent's spec.secrets: a string in the variable env, or with file a file whose path is in env.
export interface SecretUse { name: string; env: string; file?: boolean }
// A secret an agent's runs get (GET /catalog/agents/{name}/secrets): its own, inherited from profile, or needed by
// the header of an MCP server; state says whether the company has it with the kind the use needs.
export interface AgentSecretUse { name: string; env?: string; kind: SecretKind; profile?: string; server?: string; state: "set" | "missing" | "wrong_kind"; secret_kind?: SecretKind }
export interface AgentSecrets { base: number; uses: AgentSecretUse[] }

export function secretList(v: unknown): Secret[] {
  if (Array.isArray(v)) return v as Secret[];
  const o = v as { secrets?: Secret[]; items?: Secret[] } | null;
  return o?.secrets ?? o?.items ?? [];
}

export const useSecrets = () => useQuery({ queryKey: ["secrets"], queryFn: async () => secretList(await core<unknown>("/secrets")) });
export const useAgentSecrets = (name: string | null) =>
  useQuery({ queryKey: ["agent-secrets", name], queryFn: () => core<AgentSecrets>(`/catalog/agents/${encodeURIComponent(name ?? "")}/secrets`), enabled: !!name });

export const kindLabel = (k?: SecretKind) => (k === "file" ? "file" : "text");

// The agents a secret's used_by names ("agent NAME"; MCP servers are "mcp SERVER").
export const agentsOf = (usedBy?: string[]) => (usedBy ?? []).filter((u) => u.startsWith("agent ")).map((u) => u.slice(6));

const envPattern = /^[A-Za-z_][A-Za-z0-9_]{0,127}$/;

// envError checks a variable name as Core does (config.validSecrets): a valid name, not FACTORY_*, not taken.
export function envError(env: string, taken: string[]): string | null {
  if (!envPattern.test(env)) return "Letters, digits and _, not starting with a digit";
  if (env.toUpperCase().startsWith("FACTORY_")) return "FACTORY_* variables are App Factory's own";
  if (taken.includes(env)) return `${env} is already used by another secret of this agent`;
  return null;
}

// problemOf describes a use's state for people: null when set with its kind.
export function problemOf(u: AgentSecretUse): string | null {
  if (u.state === "missing") return "not set: runs wait until it is added on the Secrets page";
  if (u.state === "wrong_kind") return `set as ${kindLabel(u.secret_kind)}, needed as ${kindLabel(u.kind)}: runs wait`;
  return null;
}

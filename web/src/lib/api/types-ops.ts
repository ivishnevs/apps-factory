// Shapes the Chat, Costs and Nodes screens read beyond types.ts (mirroring the Go json tags).
import type { Agent, FactoryNode, Overview, Pause, Task } from "./types";

// A task's kind (tasks.Task.Kind): "conversation" is a report's chat task with the owner.
export type TaskWithKind = Task & { kind?: string };

// storage.NodeDriver.
export interface NodeDriver {
  name: string; version?: string; ok: boolean; check?: string; detail?: string; transient_failures?: number; until?: number;
}

// A node's last heartbeat health (runtime protocol v2), as internal/tui/nodes.go reads it.
export interface NodeHealth {
  load1?: number; cpus?: number; free_disk_bytes?: number; busy_slots?: number; disk?: "ok" | "low" | "critical" | string;
  disk_low_bytes?: number; disk_critical_bytes?: number; checks?: { check: string; status: string; detail?: string }[];
  stalled_seconds?: number;
}

export type NodeFull = FactoryNode & { node_id: string; resources?: Record<string, number> | null; drivers?: NodeDriver[] | null };

export const nodeHealth = (n: FactoryNode) => (n.health ?? {}) as NodeHealth;
export const nodeId = (n: FactoryNode) => (n as NodeFull).node_id ?? n.id;

// The owner conversation with a direct report in a project (internal/tui ownerConversation).
export const ownerConversation = (agent: string, project: string) => `owner-${agent}-${project}`;

// The owner's direct reports, the default report first, then by name (internal/tui directReports).
export function directReports(agents: Agent[], def: string): string[] {
  return agents.filter((a) => a.reports_to === "owner").map((a) => a.name)
    .sort((a, b) => (a === def) !== (b === def) ? (a === def ? -1 : 1) : a.localeCompare(b));
}

// owner.default_report, or the only direct report (internal/tui defaultReportOf).
export function defaultReportOf(ov: Overview): string {
  if (ov.health.default_report) return ov.health.default_report;
  const r = directReports(ov.agents ?? [], "");
  return r.length === 1 ? r[0] : "";
}

// The report a leading @name addresses, else the default report; null when @name is not a direct report.
export function addressee(body: string, reports: string[], def: string): string | null {
  if (body.startsWith("@")) {
    const name = body.slice(1).split(/\s/, 1)[0].replace(/[,:]+$/, "");
    return reports.includes(name) ? name : null;
  }
  return def || null;
}

// The scope of a pause in words (quota_key → quota key).
export const scopeName = (s: string) => (s === "quota_key" ? "quota key" : s);
export const companyPause = (pauses: Pause[] | undefined) => (pauses ?? []).find((p) => p.scope === "company");

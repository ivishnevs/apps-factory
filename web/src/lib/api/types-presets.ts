// The team builder (team-builder D1–D4): the release's presets, an agent's document as fields, the setup status.

export interface PresetMeta { name: string; title: string; description: string; tags?: string[]; category?: string; examples?: string[] }
export interface PresetSecret { name: string; env: string; file?: boolean; why?: string }

export interface AgentPreset {
  metadata: PresetMeta; version: string; prompt: string;
  spec: { role: string; agent?: Record<string, unknown>; skills: string[]; secrets: PresetSecret[] };
}

export interface TeamPresetAgent { key: string; preset: string; reports_to: string; can_message: string[]; description?: string }
export interface TeamPreset {
  metadata: PresetMeta; version: string; skills: string[]; secrets: PresetSecret[];
  spec: { team: string; lead: string; agents: TeamPresetAgent[] };
}

// The context window per run kind (work-and-conversation-runs): tokens, 200k, 1M or max; empty keeps the runtime default.
export interface ContextWindow { work?: string; conversation?: string }

// The agent document as config.Agent's JSON: the fields the form edits.
export interface AgentSpec {
  extends?: string; team?: string; role?: string; reports_to?: string;
  runtime?: { provider?: string; model?: string; quota_key?: string; context_window?: ContextWindow };
  // work-and-conversation-runs: max_concurrent_runs limits work runs; conversation.max_concurrent_runs conversation runs (0: no cap).
  execution?: { timeout?: string; max_concurrent_runs?: number; max_concurrent_runs_per_project?: number; max_retries?: number; conversation?: { max_concurrent_runs?: number; share_work_limit?: boolean } };
  prompt?: { file?: string };
  communication?: { can_message?: string[]; can_create_tasks?: boolean; can_assign_tasks?: boolean | "none" | "direct_reports" | "subtree" | string[] };
  skills?: { include?: string[] };
  capabilities?: string[];
  [k: string]: unknown;
}
export interface Agent { apiVersion?: string; kind?: string; metadata: { name: string; description: string; preset?: string }; spec: AgentSpec }
export interface PresetUpdate { preset: string; from: string; to: string; prompt: string; changes?: string[] }
export interface AgentDoc { base: number; agent: Agent; prompt: string; update?: PresetUpdate }

export interface AgentOverrides {
  description?: string; reports_to?: string; can_message?: string[]; provider?: string; model?: string; quota_key?: string; prompt?: string;
  context_window?: ContextWindow; max_concurrent_runs?: number; conversation_max_runs?: number;
}

export interface NeededSecret { name: string; kind: "string" | "file"; set: boolean; wrong_kind?: boolean; agents: string[] }
export interface Setup { missing_secrets: NeededSecret[]; no_node: boolean; teams_without_projects: string[]; ready: boolean }

// presetVersion: the version part of an agent's metadata.preset ("developer@3f2a…").
export function presetOf(ref?: string): { name: string; version: string } | null {
  if (!ref) return null;
  const [name, version] = ref.split("@");
  return name ? { name, version: version ?? "" } : null;
}

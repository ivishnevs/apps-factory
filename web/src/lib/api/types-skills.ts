// The company's skills (skill-registry design D10): sources, the library indexed from them, installed skills.
export type SourceStatus = "ok" | "syncing" | "waiting_for_node" | "failed";

export interface SkillSource {
  id: string; kind: "builtin" | "git" | "upload"; url?: string; ref?: string; subpath?: string;
  access?: "public" | "node"; update?: "pinned" | "track"; commit?: string; synced_at?: number;
  status: SourceStatus; error?: string; skills?: number;
}

export interface LibrarySkill {
  source: string; name: string; commit?: string; hash?: string; description?: string; categories?: string[];
  error?: string; installed?: boolean;
}

export interface SkillFile { path: string; size: number }

export interface SkillUpdate { hash: string; commit?: string; files?: { path: string; change: string }[] }

export interface InstalledSkill {
  name: string; source: string; hash: string; commit?: string; scope?: string; description?: string;
  categories?: string[]; update?: SkillUpdate;
}

// scopeLabel: who may be given an installed skill (design D9). It is availability, not use: an agent has a skill
// only when its spec.skills.include names it (skill-use-visible).
export function scopeLabel(scope?: string): string {
  if (!scope) return "Any agent may use";
  const [kind, name] = scope.split(":");
  return kind === "team" ? `Team ${name} only` : kind === "agent" ? `Agent ${name} only` : scope;
}

// skillUsers maps each skill to the agents that include it.
export function skillUsers(teams: { agents: { name: string; skills?: string[] }[] }[]): Map<string, string[]> {
  const out = new Map<string, string[]>();
  for (const t of teams) for (const a of t.agents) for (const s of a.skills ?? []) out.set(s, [...(out.get(s) ?? []), a.name]);
  return out;
}

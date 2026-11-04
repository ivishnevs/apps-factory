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

// scopeLabel: who may use an installed skill (design D9).
export function scopeLabel(scope?: string): string {
  if (!scope) return "Every agent";
  const [kind, name] = scope.split(":");
  return kind === "team" ? `Team ${name}` : kind === "agent" ? `Agent ${name}` : scope;
}

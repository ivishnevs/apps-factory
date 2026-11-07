// The company's stored catalog (company-self-service): Core's /catalog, /templates and /companies.

export interface CatalogVersion { version: number; author: string; note?: string; created_at: number; documents: number }
export interface CatalogDocumentRef { path: string; size: number }
export interface CatalogListing { version: number; author: string; note?: string; created_at: number; company: string; documents: CatalogDocumentRef[] }
export interface CatalogDocument { version: number; path: string; size: number; content: string }

export interface TeamMember {
  name: string; team?: string; document?: string; skills?: string[]; description?: string; role: string; reports_to: string; prompt: string; provider: string; model?: string;
  creates_tasks?: boolean; review_required?: boolean; reviewer?: string; review_paths?: string[];
}
// A team of the company (company-teams): its lead gets the owner's unaddressed messages in its projects.
export interface ApprovalMode { mode: "" | "owner" | "agent" | "lead"; agent?: string }
export interface ApprovalModes { internal_artifacts?: ApprovalMode; external_artifacts?: ApprovalMode; decisions?: ApprovalMode }
export interface TeamView { name: string; title?: string; description?: string; lead: string; audit_assignee: string; profile?: string; agents: TeamMember[]; projects: string[]; approvals?: ApprovalModes }
export interface Teams { company: string; version: number; teams: TeamView[] }

export interface CatalogChange { base: number; put?: Record<string, string>; delete?: string[]; note?: string; dry_run?: boolean }

export interface Company { id: string; name: string; created_at: number; catalog_version: number }
export interface CompanyCreated { company: string; catalog_version: number; owner_token: { id: string; token: string; name: string } }

// Document groups in the editor, by path.
export function documentGroup(path: string): "Teams" | "Agents" | "Profiles" | "Policies" | "Prompts" | "Node hooks" | "Settings" {
  if (path.startsWith("prompts/")) return "Prompts";
  if (/\/teams\//.test(path)) return "Teams";
  if (/\/agents\//.test(path)) return "Agents";
  if (/\/profiles\//.test(path)) return "Profiles";
  if (/\/policies\//.test(path)) return "Policies";
  if (/\/node-hooks\//.test(path)) return "Node hooks";
  return "Settings";
}

// lineDiff is a line diff of a and b (LCS), or null when they are too large to diff here.
export type DiffLine = { kind: "same" | "add" | "del"; text: string };
export function lineDiff(a: string, b: string): DiffLine[] | null {
  const x = a.split("\n"), y = b.split("\n");
  if (x.length * y.length > 4_000_000) return null;
  const n = x.length, m = y.length;
  const dp = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--) dp[i][j] = x[i] === y[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
  const out: DiffLine[] = [];
  let i = 0, j = 0;
  while (i < n && j < m) {
    if (x[i] === y[j]) { out.push({ kind: "same", text: x[i] }); i++; j++; }
    else if (dp[i + 1][j] >= dp[i][j + 1]) out.push({ kind: "del", text: x[i++] });
    else out.push({ kind: "add", text: y[j++] });
  }
  while (i < n) out.push({ kind: "del", text: x[i++] });
  while (j < m) out.push({ kind: "add", text: y[j++] });
  return out;
}

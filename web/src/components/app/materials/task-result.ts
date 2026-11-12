import type { Material, MaterialKind } from "@/lib/api/types-materials";

// task-result-first: how a task's materials split into the result, the rest and the earlier versions.

// The kinds that can lead a result: something to look at or read.
const readable: MaterialKind[] = ["image", "document", "pdf", "web"];

export interface TaskMaterialsLayout {
  // lead is the big card of a finished task; current the rows right under it; earlier the folded groups.
  lead?: Material;
  current: Material[];
  earlier: { key: string; title: string; items: Material[] }[];
}

// layout splits a task's materials (the API lists the result first, in the order named, then the rest newest first).
// With a result: its first image or document leads, the rest of it and the pull requests follow, everything else is
// an earlier version. Without one: the newest material with content leads and only the older versions of an
// artifact fold away. Never by kind order.
export function layout(items: Material[], featured: boolean): TaskMaterialsLayout {
  const result = items.filter((m) => m.result);
  const hasContent = (m: Material) => !!m.content_url;
  let lead: Material | undefined;
  if (featured) {
    lead = result.length > 0
      ? result.find((m) => hasContent(m) && readable.includes(m.kind)) ?? result.find(hasContent)
      : items.find(hasContent);
  }
  const seen = new Map<string, string>(); // artifact → its newest version
  const isEarlier = (m: Material) => {
    if (result.length > 0) return !m.result && m.kind !== "pull_request";
    if (!m.artifact_id || !m.version) return false;
    const newest = seen.get(m.artifact_id);
    if (newest === undefined) { seen.set(m.artifact_id, m.version); return false; }
    return newest !== m.version;
  };
  const current: Material[] = [];
  const groups = new Map<string, { key: string; title: string; items: Material[] }>();
  for (const m of items) {
    if (m === lead) continue;
    if (!isEarlier(m)) { current.push(m); continue; }
    const key = m.artifact_id ? `artifact:${m.artifact_id}` : m.report ? `report:${m.report.id}` : m.id;
    let g = groups.get(key);
    if (!g) {
      g = { key, title: m.artifact_id ?? (m.report?.summary || m.name), items: [] };
      groups.set(key, g);
    }
    g.items.push(m);
  }
  return { lead, current, earlier: [...groups.values()] };
}

// versionLabel names the artifact and version a material comes from, so same-named files of different versions
// stay apart ("01-hero.png · screenshots-sample v2").
export const versionLabel = (m: Material) => (m.artifact_id && m.version ? `${m.artifact_id} ${m.version}` : "");

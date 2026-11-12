import type { MaterialRef, MessageAttachment } from "@/lib/api/types";
import type { Material, MaterialKind } from "@/lib/api/types-materials";

// What a message or a decision proposal carries (inbox-visual-previews): attached files and referenced materials as
// viewer materials, in the order the sender gave, with their captions. Images show inline (the preview of a large
// one); the viewer opens the original.

const byExt: Record<string, MaterialKind> = {
  md: "document", markdown: "document", txt: "document", rtf: "document", doc: "document", docx: "document",
  pdf: "pdf", html: "web", htm: "web",
  png: "image", jpg: "image", jpeg: "image", gif: "image", webp: "image", svg: "image", avif: "image",
  json: "data", csv: "data", tsv: "data", yaml: "data", yml: "data", xml: "data", xlsx: "data",
  mp4: "link", mov: "link", webm: "link",
};

const ext = (name = "") => (name.match(/\.([a-z0-9]+)$/i)?.[1] ?? "").toLowerCase();
const base = (p = "") => p.split("/").filter(Boolean).pop() ?? p;
export const isVideo = (name = "") => ["mp4", "mov", "webm"].includes(ext(name));

export function kindOfName(name: string): MaterialKind {
  return byExt[ext(name)] ?? "file";
}

const blobURL = (sha: string, name: string) => `/api/blobs/${sha}?name=${encodeURIComponent(name)}`;

export interface Visual {
  material: Material;
  caption?: string;
  // thumb is what the card shows inline: the preview of a large image, else the original.
  thumb?: string;
}

export function messageVisuals(project: string, at: number, author: string, attachments: MessageAttachment[] = [], materials: MaterialRef[] = []): Visual[] {
  const out: Visual[] = [];
  attachments.forEach((a, i) => {
    const name = base(a.path);
    const url = blobURL(a.sha256, name);
    const kind = kindOfName(name);
    out.push({
      caption: a.caption,
      thumb: kind === "image" ? (a.preview ? blobURL(a.preview.sha256, `preview-${name}`) : url) : undefined,
      material: { id: `msg:${i}:${a.sha256}`, kind, source: "message", name, path: a.path, size: a.size, sha256: a.sha256, content_url: url, redacted: a.redacted, project_id: project, author, created_at: at },
    });
  });
  materials.forEach((m, i) => {
    const name = m.name || base(m.path) || m.artifact_id || "file";
    let url: string | undefined;
    if (m.artifact_id && m.sha256) {
      url = `/api/artifacts/${encodeURIComponent(project)}/${encodeURIComponent(m.artifact_id)}/${encodeURIComponent(m.artifact_version ?? "")}?name=${encodeURIComponent(name)}`;
    } else if (m.sha256) {
      url = blobURL(m.sha256, name);
    }
    const external = !url && m.uri && /^https?:\/\//.test(m.uri) ? m.uri : undefined;
    const kind = url ? kindOfName(name) : "link";
    out.push({
      caption: m.caption,
      thumb: kind === "image" && url ? (m.preview ? blobURL(m.preview.sha256, `preview-${name}`) : url) : undefined,
      material: {
        id: `ref:${i}:${m.artifact_id ?? m.report_id}:${m.artifact_version ?? m.path}`, kind, source: m.artifact_id ? "artifact" : "report", name, path: m.path,
        version: m.artifact_version, artifact_id: m.artifact_id, size: m.size, sha256: m.sha256, content_url: url, url: external, redacted: m.redacted, project_id: project, author, created_at: at,
      },
    });
  });
  // A video is a link that opens it in a new tab (no playback in the card).
  return out.map((v) => (isVideo(v.material.name) && v.material.content_url ? { ...v, material: { ...v.material, kind: "link", url: v.material.content_url, content_url: undefined } } : v));
}

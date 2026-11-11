import type { PRFacts } from "./types";

// Task materials (internal/web/materials.go): what tasks produced. A report's attached file (a blob), an
// artifact version (uploaded content or an external link) or a pull request.
export type MaterialKind = "document" | "pdf" | "web" | "image" | "data" | "code" | "file" | "link" | "pull_request";

export interface Material {
  id: string;
  kind: MaterialKind;
  source: "report" | "artifact" | "submission" | "exit" | "archive";
  // archive: the archive version an image comes from; versions: an artifact's older versions (one card per artifact).
  archive?: string;
  versions?: { version: string; created_at: number; content_url?: string; url?: string; size?: number; archived_at?: number }[];
  // archived_at (Unix seconds) and archived_reason: retention archived this artifact version (artifact-retention); its
  // metadata stays, its content is gone, so it has no content_url.
  archived_at?: number;
  archived_reason?: string;
  name: string;
  path?: string;
  version?: string;
  size?: number;
  sha256?: string;
  // content_url is this app's raw content (/api/blobs/… or /api/artifacts/…); url an external link.
  content_url?: string;
  url?: string;
  redacted?: boolean;
  project_id: string;
  task_id?: string;
  task_title?: string;
  run_id?: string;
  author?: string;
  created_at: number;
  report?: { id: string; kind: string; summary: string; result?: string };
  approval?: { id: string; status: string; decided_at?: number; reason?: string };
  pr?: PRFacts;
}

export interface MaterialPage { items: Material[]; next_before?: number; has_more: boolean }

import type { PRFacts } from "./types";

// Task materials (internal/web/materials.go): what tasks produced. A report's attached file (a blob), an
// artifact version (uploaded content or an external link) or a pull request.
export type MaterialKind = "document" | "pdf" | "web" | "image" | "data" | "code" | "file" | "link" | "pull_request";

export interface Material {
  id: string;
  kind: MaterialKind;
  source: "report" | "artifact" | "submission" | "exit";
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

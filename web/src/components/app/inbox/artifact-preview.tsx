"use client";

import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ExternalLink, FileText } from "lucide-react";
import { useState } from "react";
import { ErrorState } from "@/components/app/page";
import { CodeBlock, Markdown } from "@/components/app/run/markdown";
import { Skeleton } from "@/components/ui/skeleton";
import type { Artifact } from "@/lib/api/types";
import type { ArtifactMeta } from "@/lib/api/types-live";
import { cn } from "@/lib/utils";

type Kind = "image" | "html" | "markdown" | "json" | "text" | "pdf" | "binary";

const ext = (name = "") => (name.match(/\.([a-z0-9]+)$/i)?.[1] ?? "").toLowerCase();

function kindOf(name: string | undefined, type: string): Kind {
  const e = ext(name);
  if (type.startsWith("image/") || ["png", "jpg", "jpeg", "gif", "webp", "svg", "avif"].includes(e)) return "image";
  if (e === "pdf" || type === "application/pdf") return "pdf";
  if (["html", "htm"].includes(e) || type.startsWith("text/html")) return "html";
  if (["md", "markdown"].includes(e)) return "markdown";
  if (e === "json" || type.includes("json")) return "json";
  if (type.startsWith("text/") || ["txt", "csv", "yaml", "yml", "log", "go", "ts", "tsx", "js", "py", "swift", "kt"].includes(e)) return "text";
  return "binary";
}

export const byteSize = (n?: number) => (!n ? "" : n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MiB` : n >= 1024 ? `${(n / 1024).toFixed(1)} KiB` : `${n} B`);

// ArtifactPreview shows an artifact version: images inline, markdown/json/text rendered, HTML in a sandboxed
// frame (no scripts, no same-origin), anything else as a download link.
export function ArtifactPreview({ project, artifact }: { project: string; artifact: Artifact & ArtifactMeta }) {
  const url = `/api/artifacts/${encodeURIComponent(project)}/${encodeURIComponent(artifact.id)}/${encodeURIComponent(artifact.version)}`;
  const [open, setOpen] = useState(false);
  const q = useQuery({
    queryKey: ["artifact", url],
    staleTime: Infinity,
    queryFn: async () => {
      const res = await fetch(url, { credentials: "same-origin" });
      const type = res.headers.get("content-type") ?? "";
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body?.error ?? `HTTP ${res.status}`);
      }
      const kind = kindOf(artifact.name, type);
      const text = ["markdown", "json", "text"].includes(kind) ? await res.text() : "";
      return { kind, text };
    },
  });

  return (
    <div className="overflow-hidden rounded-xl border">
      <div className="flex items-center gap-2 border-b bg-muted/40 px-3 py-2 text-xs">
        <FileText className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="min-w-0 truncate font-medium">{artifact.name || artifact.id}</span>
        <span className="shrink-0 rounded bg-background px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">{artifact.version}</span>
        <span className="ml-auto shrink-0 text-muted-foreground">{byteSize(artifact.size)}</span>
        <a href={url} target="_blank" rel="noreferrer" aria-label="Open the artifact in a new tab"
          className="-my-2 -mr-3 flex size-10 shrink-0 items-center justify-center text-muted-foreground hover:text-foreground">
          <ExternalLink className="size-3.5" />
        </a>
      </div>
      <div className={cn("relative", !open && "max-h-80 overflow-hidden")}>
        {q.error ? <div className="p-3"><ErrorState error={q.error} /></div>
          : !q.data ? <div className="space-y-2 p-4"><Skeleton className="h-4 w-1/2" /><Skeleton className="h-4 w-4/5" /><Skeleton className="h-4 w-2/3" /></div>
          : q.data.kind === "image" ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={url} alt={artifact.name || artifact.id} className="mx-auto block max-h-[32rem] w-auto max-w-full bg-[repeating-conic-gradient(#8881_0_25%,transparent_0_50%)] bg-[length:16px_16px]" />
          ) : q.data.kind === "html" || q.data.kind === "pdf" ? (
            <iframe src={url} title={artifact.name || artifact.id} sandbox="" className="block h-96 w-full bg-white" />
          ) : q.data.kind === "markdown" ? (
            <div className="p-4"><Markdown text={q.data.text} /></div>
          ) : q.data.kind === "json" ? (
            <CodeBlock text={prettyJSON(q.data.text)} className="rounded-none border-0" />
          ) : q.data.kind === "text" ? (
            <CodeBlock text={q.data.text} className="rounded-none border-0" />
          ) : (
            <p className="p-4 text-sm text-muted-foreground">No preview for this file type. <a className="underline" href={url} target="_blank" rel="noreferrer">Open it</a>.</p>
          )}
        {!open && q.data && ["markdown", "json", "text"].includes(q.data.kind) && q.data.text.split("\n").length > 14 && (
          <div className="absolute inset-x-0 bottom-0 h-20 bg-gradient-to-t from-background to-transparent" />
        )}
      </div>
      {q.data && ["markdown", "json", "text"].includes(q.data.kind) && q.data.text.split("\n").length > 14 && (
        <button type="button" onClick={() => setOpen(!open)}
          className="flex h-10 w-full items-center justify-center gap-1 border-t text-xs font-medium text-muted-foreground hover:bg-accent/50 hover:text-foreground">
          {open ? "Show less" : "Show all"} <ChevronDown className={cn("size-3.5 transition-transform", open && "rotate-180")} />
        </button>
      )}
    </div>
  );
}

function prettyJSON(s: string) {
  try { return JSON.stringify(JSON.parse(s), null, 2); } catch { return s; }
}

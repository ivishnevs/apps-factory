"use client";

import { useQuery } from "@tanstack/react-query";
import { ArrowUpRight, ChevronRight, Download, EyeOff, X } from "lucide-react";
import Link from "next/link";
import { ErrorState } from "@/components/app/page";
import { CodeBlock, Markdown } from "@/components/app/run/markdown";
import { since } from "@/components/app/tasks/model";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetDescription, SheetTitle } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import type { Material } from "@/lib/api/types-materials";
import { fileSize } from "@/lib/format";
import { cleanText } from "@/lib/humanize";
import { cn } from "@/lib/utils";
import { ApprovalBadge, KindIcon, authorName, kinds, materialTitle } from "./kinds";

const ext = (name = "") => (name.match(/\.([a-z0-9]+)$/i)?.[1] ?? "").toLowerCase();
const withParam = (url: string, k: string, v: string) => `${url}${url.includes("?") ? "&" : "?"}${k}=${v}`;
// Text larger than this is not rendered inline; it can still be downloaded.
const maxInline = 8_000_000;

type Body = { as: "markdown" | "text" | "code" | "json" | "table" | "none"; text: string };

function asOf(m: Material, type: string): Body["as"] {
  const e = ext(m.name);
  const textual = type.startsWith("text/") || type.includes("json") || type.includes("xml") || type.includes("yaml");
  if (!textual) return "none";
  if (e === "md" || e === "markdown" || type.startsWith("text/markdown")) return "markdown";
  if (e === "json" || type.includes("json")) return "json";
  if (e === "csv" || e === "tsv") return "table";
  if (m.kind === "document" || e === "txt") return "text";
  return "code";
}

// MaterialViewer is a material full screen on a phone and a wide side sheet on a desktop: the file rendered
// (markdown as a document, images inline, JSON pretty, CSV as a table, HTML and PDF in a sandboxed frame,
// other text as code) under its name, who made it, the report it came with and a download button.
export function MaterialViewer({ material, onClose, now, showTask = true }: { material: Material | null; onClose: () => void; now: number; showTask?: boolean }) {
  return (
    <Sheet open={!!material} onOpenChange={(o) => { if (!o) onClose(); }}>
      <SheetContent side="right" showCloseButton={false} className="w-full gap-0 p-0 sm:max-w-3xl xl:max-w-4xl">
        {material && <ViewerBody m={material} onClose={onClose} now={now} showTask={showTask} />}
      </SheetContent>
    </Sheet>
  );
}

function ViewerBody({ m, onClose, now, showTask }: { m: Material; onClose: () => void; now: number; showTask: boolean }) {
  const ago = since(m.created_at, now);
  const meta = [kinds[m.kind]?.label, fileSize(m.size), authorName(m.author), ago && (ago === "now" ? "just now" : `${ago} ago`)].filter(Boolean).join(" · ");
  const url = m.content_url;
  return (
    <>
      <header className="flex items-center gap-3 border-b px-4 py-3 pt-[max(0.75rem,env(safe-area-inset-top))] sm:px-5">
        <KindIcon kind={m.kind} className="size-9 rounded-lg" />
        <div className="min-w-0 flex-1">
          <SheetTitle className="truncate text-base leading-tight" title={m.path || m.name}>{materialTitle(m)}</SheetTitle>
          <SheetDescription className="mt-0.5 truncate text-xs tabular-nums">{meta}</SheetDescription>
        </div>
        {url && (
          <>
            <Button asChild variant="outline" size="sm" className="hidden h-9 sm:inline-flex">
              <a href={withParam(url, "download", "1")} download={m.name}><Download />Download</a>
            </Button>
            <Button asChild variant="ghost" size="icon" className="size-10 sm:hidden" aria-label="Download">
              <a href={withParam(url, "download", "1")} download={m.name}><Download /></a>
            </Button>
            <Button asChild variant="ghost" size="icon" className="hidden size-9 sm:inline-flex" aria-label="Open in a new tab">
              <a href={url} target="_blank" rel="noreferrer noopener"><ArrowUpRight /></a>
            </Button>
          </>
        )}
        <Button variant="ghost" size="icon" className="-mr-1 size-10 sm:size-9" aria-label="Close" onClick={onClose}><X /></Button>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain">
        <About m={m} showTask={showTask} />
        <div className="px-4 pb-[max(2rem,env(safe-area-inset-bottom))] sm:px-8">
          {url ? <Content m={m} url={url} /> : <p className="py-10 text-center text-sm text-muted-foreground">This item has no content stored in App Factory.</p>}
        </div>
      </div>
    </>
  );
}

// About: the report the file came with, its approval, the task it belongs to.
function About({ m, showTask }: { m: Material; showTask: boolean }) {
  const summary = m.report?.summary && cleanText(m.report.summary);
  const path = m.path && m.path !== m.name ? m.path : "";
  if (!summary && !m.approval && !(showTask && m.task_id) && !path && !m.redacted) return <div className="h-4" />;
  return (
    <div className="space-y-3 px-4 pt-4 pb-5 sm:px-8 sm:pt-6">
      {summary && (
        <div className="rounded-xl border bg-muted/30 px-4 py-3">
          <p className="text-xs font-medium text-muted-foreground">{m.report?.kind === "stage_completed" ? "Report" : "Note"} from {authorName(m.author) || "the team"}</p>
          <p className="mt-1 text-sm leading-relaxed wrap-anywhere">{summary}</p>
        </div>
      )}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-sm">
        {showTask && m.task_id && (
          <Link href={`/tasks/${encodeURIComponent(m.task_id)}`} className="inline-flex min-w-0 max-w-full items-center gap-1 font-medium underline-offset-4 hover:underline">
            <span className="shrink-0 text-muted-foreground">Task</span><span className="min-w-0 truncate">{m.task_title || "Open the task"}</span><ChevronRight className="size-3.5 shrink-0 text-muted-foreground" />
          </Link>
        )}
        {m.approval && <ApprovalBadge status={m.approval.status} />}
        {m.approval?.status === "PENDING" && <Link href="/inbox" className="text-sm font-medium underline-offset-4 hover:underline">Review in the inbox</Link>}
        {m.redacted && <span className="inline-flex items-center gap-1 text-xs text-muted-foreground"><EyeOff className="size-3.5" />Secrets were removed from this file</span>}
        {path && <span className="min-w-0 max-w-full truncate font-mono text-xs text-muted-foreground" title={path}>{path}</span>}
      </div>
    </div>
  );
}

function Content({ m, url }: { m: Material; url: string }) {
  const frame = m.kind === "web" || m.kind === "pdf";
  const textual = !frame && m.kind !== "image";
  const tooBig = textual && (m.size ?? 0) > maxInline;
  const q = useQuery({
    queryKey: ["material-content", url],
    staleTime: Infinity,
    enabled: textual && !tooBig,
    queryFn: async (): Promise<Body> => {
      const res = await fetch(url, { credentials: "same-origin" });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body?.error ?? `Could not load the file (HTTP ${res.status})`);
      }
      const as = asOf(m, res.headers.get("content-type") ?? "");
      return { as, text: as === "none" ? "" : await res.text() };
    },
  });

  if (m.kind === "image") {
    return (
      <div className="flex justify-center rounded-xl border bg-[repeating-conic-gradient(#8881_0_25%,transparent_0_50%)] bg-[length:16px_16px] p-2">
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src={url} alt={m.name} className="block h-auto max-h-[75dvh] w-auto max-w-full" />
      </div>
    );
  }
  if (frame) return <iframe src={url} title={m.name} sandbox="" className="block h-[calc(100dvh-10rem)] min-h-96 w-full rounded-xl border bg-white" />;
  if (tooBig) return <NoPreview m={m} url={url} why="This file is too large to show here." />;
  if (q.error) return <ErrorState error={q.error} />;
  if (!q.data) return <div className="space-y-3 py-2"><Skeleton className="h-7 w-2/3" /><Skeleton className="h-4 w-full" /><Skeleton className="h-4 w-5/6" /><Skeleton className="h-4 w-3/4" /></div>;
  const { as, text } = q.data;
  switch (as) {
    case "markdown":
      return <article className="mx-auto max-w-[72ch]"><Markdown text={text} variant="document" /></article>;
    case "text":
      return <article className="mx-auto max-w-[72ch] text-[15px] leading-7 whitespace-pre-wrap wrap-anywhere">{text}</article>;
    case "json":
      return <CodeBlock text={pretty(text)} lang="json" tall />;
    case "table":
      return <CsvTable text={text} tab={ext(m.name) === "tsv"} />;
    case "code":
      return <CodeBlock text={text} lang={ext(m.name)} tall />;
  }
  return <NoPreview m={m} url={url} why="There is no preview for this type of file." />;
}

function NoPreview({ m, url, why }: { m: Material; url: string; why: string }) {
  return (
    <div className="flex flex-col items-center rounded-xl border border-dashed px-6 py-12 text-center">
      <KindIcon kind={m.kind} className="mb-3" />
      <p className="font-medium">{why}</p>
      <Button asChild className="mt-4 h-10"><a href={withParam(url, "download", "1")} download={m.name}><Download />Download {fileSize(m.size)}</a></Button>
    </div>
  );
}

function pretty(s: string) {
  try { return JSON.stringify(JSON.parse(s), null, 2); } catch { return s; }
}

// parseCSV reads quoted fields and doubled quotes; at most 1000 rows are shown.
function parseCSV(text: string, sep: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = "";
  let quoted = false;
  for (let i = 0; i < text.length && rows.length <= 1000; i++) {
    const c = text[i];
    if (quoted) {
      if (c === '"' && text[i + 1] === '"') { cell += '"'; i++; } else if (c === '"') quoted = false; else cell += c;
    } else if (c === '"' && cell === "") quoted = true;
    else if (c === sep) { row.push(cell); cell = ""; }
    else if (c === "\n" || c === "\r") {
      if (c === "\r" && text[i + 1] === "\n") i++;
      row.push(cell); cell = "";
      if (row.some((x) => x !== "")) rows.push(row);
      row = [];
    } else cell += c;
  }
  if (cell || row.length) { row.push(cell); rows.push(row); }
  return rows;
}

function CsvTable({ text, tab }: { text: string; tab: boolean }) {
  const rows = parseCSV(text, tab ? "\t" : ",");
  if (rows.length === 0) return <p className="text-sm text-muted-foreground">The file is empty.</p>;
  const [head, ...body] = rows;
  return (
    <div className="overflow-auto rounded-xl border">
      <table className="w-full border-collapse text-left text-sm">
        <thead className="sticky top-0 bg-muted"><tr>{head.map((c, i) => <th key={i} scope="col" className="border-b px-3 py-2 font-semibold whitespace-nowrap">{c}</th>)}</tr></thead>
        <tbody>{body.map((r, i) => <tr key={i} className={cn("border-b last:border-0", i % 2 === 1 && "bg-muted/30")}>{head.map((_, j) => <td key={j} className="px-3 py-1.5 align-top whitespace-nowrap tabular-nums">{r[j] ?? ""}</td>)}</tr>)}</tbody>
      </table>
    </div>
  );
}

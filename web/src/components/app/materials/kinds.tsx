import { Braces, File, FileCode2, FileText, GitPullRequest, Globe, Image as ImageIcon, Link2, type LucideIcon } from "lucide-react";
import type { Material, MaterialKind } from "@/lib/api/types-materials";
import { cn } from "@/lib/utils";

// What each kind of material looks like: a word, an icon and a tint.
export const kinds: Record<MaterialKind, { label: string; icon: LucideIcon; tint: string }> = {
  document: { label: "Document", icon: FileText, tint: "bg-blue-500/10 text-blue-600 dark:text-blue-300" },
  pdf: { label: "PDF", icon: FileText, tint: "bg-red-500/10 text-red-600 dark:text-red-300" },
  web: { label: "Web page", icon: Globe, tint: "bg-violet-500/10 text-violet-600 dark:text-violet-300" },
  image: { label: "Image", icon: ImageIcon, tint: "bg-pink-500/10 text-pink-600 dark:text-pink-300" },
  data: { label: "Data", icon: Braces, tint: "bg-amber-500/10 text-amber-700 dark:text-amber-300" },
  code: { label: "Code", icon: FileCode2, tint: "bg-slate-500/10 text-slate-700 dark:text-slate-300" },
  file: { label: "File", icon: File, tint: "bg-muted text-muted-foreground" },
  link: { label: "Link", icon: Link2, tint: "bg-sky-500/10 text-sky-700 dark:text-sky-300" },
  pull_request: { label: "Pull request", icon: GitPullRequest, tint: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300" },
};

// The Library's type filter: groups of kinds.
export const kindGroups: { key: string; label: string; kinds: MaterialKind[] }[] = [
  { key: "docs", label: "Documents", kinds: ["document", "pdf"] },
  { key: "images", label: "Images", kinds: ["image"] },
  { key: "web", label: "Web pages", kinds: ["web"] },
  { key: "data", label: "Data", kinds: ["data"] },
  { key: "code", label: "Code", kinds: ["code"] },
  { key: "prs", label: "Pull requests", kinds: ["pull_request"] },
  { key: "links", label: "Links", kinds: ["link"] },
  { key: "other", label: "Other files", kinds: ["file"] },
];

export function KindIcon({ kind, className }: { kind: MaterialKind; className?: string }) {
  const k = kinds[kind] ?? kinds.file;
  return (
    <span aria-hidden className={cn("flex size-10 shrink-0 items-center justify-center rounded-xl", k.tint, className)}>
      <k.icon className="size-[18px]" />
    </span>
  );
}

// opensInViewer: files with content open in the viewer; links and pull requests open where they live.
export const opensInViewer = (m: Material) => !!m.content_url;

// title is what a material is called: a pull request by its number, a file by its name.
export const materialTitle = (m: Material) => m.name || kinds[m.kind]?.label || "File";

export const authorName = (a?: string) => (!a ? "" : a === "owner" ? "You" : a === "system" || a === "factory" ? "App Factory" : a);

const approvalWords: Record<string, { label: string; tone: string }> = {
  PENDING: { label: "Waiting for your approval", tone: "bg-amber-500/15 text-amber-800 dark:text-amber-200" },
  APPROVED: { label: "Approved", tone: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300" },
  REJECTED: { label: "Rejected", tone: "bg-red-500/15 text-red-700 dark:text-red-300" },
  EXPIRED: { label: "Approval expired", tone: "bg-muted text-muted-foreground" },
};

export function ApprovalBadge({ status, className }: { status: string; className?: string }) {
  const a = approvalWords[status] ?? { label: status.toLowerCase(), tone: "bg-muted text-muted-foreground" };
  return <span className={cn("inline-flex items-center rounded-full px-2 text-[11px] font-medium leading-5 whitespace-nowrap", a.tone, className)}>{a.label}</span>;
}

const prTone: Record<string, string> = {
  merged: "bg-violet-500/15 text-violet-700 dark:text-violet-300",
  open: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  closed: "bg-red-500/15 text-red-700 dark:text-red-300",
};

export function PRBadge({ state, className }: { state: string; className?: string }) {
  return <span className={cn("inline-flex items-center rounded-full px-2 text-[11px] font-medium leading-5 capitalize", prTone[state] ?? "bg-muted text-muted-foreground", className)}>{state}</span>;
}

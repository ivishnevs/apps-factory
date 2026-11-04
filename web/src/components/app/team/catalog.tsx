"use client";

import { Bot, FileText, RotateCcw, Save, ShieldCheck, Sparkles, Undo2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { useConfirm } from "@/components/app/confirm";
import { ErrorState, LoadingRows } from "@/components/app/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Textarea } from "@/components/ui/textarea";
import { useCatalogDocument, useCatalogHistory, usePost } from "@/lib/api/client";
import { documentGroup, lineDiff, type CatalogChange, type CatalogVersion, type TeamMember } from "@/lib/api/types-catalog";
import { ago } from "@/lib/format";
import { cn } from "@/lib/utils";

// The catalog's building blocks shared by the Teams pages (company-self-service 6.1, company-teams 7.1): an agent
// card, the document editor (a diff before saving, the loader's error at the document) and the version history
// with rollback. Saves go on top of the version the page read; a newer version refuses the save.

export function MemberCard({ m, onOpen, onSkills }: { m: TeamMember; onOpen: (path: string) => void; onSkills?: () => void }) {
  return (
    <li className="rounded-xl border bg-card p-4">
      <div className="flex items-start gap-3">
        <span className="flex size-10 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary"><Bot className="size-5" /></span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <p className="font-medium">{m.name}</p>
            <Badge variant="secondary">{m.role}</Badge>
            {m.creates_tasks && <Badge variant="outline">plans work</Badge>}
          </div>
          {m.description && <p className="mt-0.5 text-sm text-muted-foreground">{m.description}</p>}
          <p className="mt-2 text-xs text-muted-foreground">
            reports to <span className="font-medium text-foreground">{m.reports_to}</span> · {m.provider}{m.model && m.model !== "default" ? ` ${m.model}` : ""}
          </p>
          {(m.review_required || m.reviewer || (m.review_paths?.length ?? 0) > 0) && (
            <div className="mt-2 flex flex-wrap items-center gap-1.5 text-xs">
              <ShieldCheck className="size-3.5 text-emerald-600" />
              {m.review_required && <span>review required</span>}
              {m.reviewer && <span>by <span className="font-medium">{m.reviewer}</span></span>}
              {m.review_paths?.map((p) => <Badge key={p} variant="outline" className="font-mono text-[11px]">{p}</Badge>)}
            </div>
          )}
        </div>
      </div>
      {m.prompt && (
        <Button variant="ghost" size="sm" className="mt-3 h-9 w-full justify-start text-muted-foreground" onClick={() => onOpen(m.prompt)}>
          <FileText className="size-4" />Edit prompt <span className="truncate font-mono text-xs">{m.prompt}</span>
        </Button>
      )}
      {onSkills && (
        <Button variant="ghost" size="sm" className="h-9 w-full justify-start text-muted-foreground" onClick={onSkills}>
          <Sparkles className="size-4" />Skills <span className="truncate text-xs">{m.skills?.length ? m.skills.join(", ") : "none"}</span>
        </Button>
      )}
    </li>
  );
}

function Diff({ before, after }: { before: string; after: string }) {
  const lines = useMemo(() => lineDiff(before, after), [before, after]);
  if (!lines) return <p className="text-sm text-muted-foreground">The document is too large to show a diff here.</p>;
  const changed = lines.filter((l) => l.kind !== "same").length;
  if (!changed) return <p className="text-sm text-muted-foreground">No changes.</p>;
  return (
    <pre className="max-h-80 overflow-auto rounded-lg border bg-muted/30 p-2 font-mono text-xs leading-5">
      {lines.map((l, i) => (
        <div key={i} className={cn("whitespace-pre-wrap px-1", l.kind === "add" && "bg-emerald-500/15 text-emerald-800 dark:text-emerald-300", l.kind === "del" && "bg-red-500/15 text-red-800 line-through dark:text-red-300")}>
          {l.kind === "add" ? "+ " : l.kind === "del" ? "- " : "  "}{l.text}
        </div>
      ))}
    </pre>
  );
}

export function Editor({ path, base, onClose }: { path: string | null; base: number; onClose: () => void }) {
  const { data: doc, isLoading, error } = useCatalogDocument(path);
  const [text, setText] = useState("");
  const [note, setNote] = useState("");
  const [review, setReview] = useState(false);
  const [refusal, setRefusal] = useState("");
  const save = usePost<CatalogVersion>("/catalog");
  useEffect(() => { setText(doc?.content ?? ""); setReview(false); setRefusal(""); setNote(""); }, [doc?.content, path]);
  const dirty = !!doc && text !== doc.content;
  const submit = async (dryRun: boolean) => {
    if (!path) return;
    setRefusal("");
    const change: CatalogChange = { base, put: { [path]: text }, note: note.trim() || `edit ${path}`, dry_run: dryRun };
    try {
      const v = await save.mutateAsync(change);
      if (dryRun) toast.success("The catalog is valid with this change");
      else { toast.success(`Saved as version ${v.version}`); onClose(); }
    } catch (e) {
      setRefusal(e instanceof Error ? e.message : String(e));
    }
  };
  return (
    <Sheet open={!!path} onOpenChange={(o) => !o && onClose()}>
      <SheetContent side="right" className="w-full gap-0 sm:max-w-2xl">
        <SheetHeader>
          <SheetTitle className="break-all font-mono text-base">{path}</SheetTitle>
          <SheetDescription>{documentGroup(path ?? "")} · saved on top of version {base}; running work keeps the configuration it started with.</SheetDescription>
        </SheetHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-4 pb-4">
          {isLoading && <LoadingRows rows={3} />}
          {error && <ErrorState error={error} />}
          {doc && !review && (
            <Textarea value={text} onChange={(e) => setText(e.target.value)} spellCheck={false} className="min-h-[50dvh] flex-1 font-mono text-xs leading-5" aria-label={`Content of ${path}`} />
          )}
          {doc && review && <Diff before={doc.content} after={text} />}
          {refusal && <ErrorState error={refusal} />}
          {doc && (
            <>
              <Input value={note} onChange={(e) => setNote(e.target.value)} placeholder="What changed and why (shown in the history)" aria-label="Change note" />
              <div className="flex flex-wrap gap-2">
                <Button variant="outline" className="h-10" disabled={!dirty} onClick={() => setReview((r) => !r)}>{review ? "Back to editing" : "Review changes"}</Button>
                <Button variant="outline" className="h-10" disabled={!dirty || save.isPending} onClick={() => submit(true)}>Validate</Button>
                <Button className="h-10" disabled={!dirty || save.isPending} onClick={() => submit(false)}><Save className="size-4" />Save</Button>
                {dirty && <Button variant="ghost" className="h-10" onClick={() => setText(doc.content)}><Undo2 className="size-4" />Discard</Button>}
              </div>
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

export function HistoryList({ current }: { current: number }) {
  const { data, isLoading, error } = useCatalogHistory();
  const rollback = usePost<CatalogVersion>("/catalog/rollback");
  const confirm = useConfirm();
  if (isLoading) return <LoadingRows />;
  if (error) return <ErrorState error={error} />;
  return (
    <ul className="divide-y rounded-xl border bg-card">
      {(data ?? []).map((v) => (
        <li key={v.version} className="flex items-center gap-3 px-4 py-3">
          <span className={cn("flex size-9 shrink-0 items-center justify-center rounded-full text-sm font-medium", v.version === current ? "bg-primary text-primary-foreground" : "bg-muted")}>{v.version}</span>
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm">{v.note || "no note"}</p>
            <p className="text-xs text-muted-foreground">{v.author} · {ago(v.created_at)} · {v.documents} documents</p>
          </div>
          {v.version !== current && (
            <Button variant="outline" className="h-9 shrink-0" disabled={rollback.isPending} onClick={async () => {
              if (!(await confirm({ title: `Roll back to version ${v.version}?`, description: "A new version with its documents becomes current; the history keeps every version.", confirm: "Roll back" }))) return;
              try { const r = await rollback.mutateAsync({ base: current, to: v.version }); toast.success(`Version ${r.version} is current`); }
              catch (e) { toast.error(e instanceof Error ? e.message : String(e)); }
            }}><RotateCcw className="size-4" />Roll back</Button>
          )}
        </li>
      ))}
    </ul>
  );
}


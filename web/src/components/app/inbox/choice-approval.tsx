"use client";

import { Check, ChevronRight, FolderOpen, MessageSquareReply, Star, X } from "lucide-react";
import Link from "@/components/app/link";
import { useState } from "react";
import { libraryHref } from "@/components/app/materials/viewer";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import type { Approval, ApprovalOption } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import { ArtifactPreview } from "./artifact-preview";

// Choice approvals (artifact-choice-approvals): one approval over several artifact versions. People choose one
// (it is approved, the others are not chosen and stay usable), ask for changes (text) or reject all (reason). The
// options the delegate dropped are collapsed with its reasons, and still choosable. Each option shows its preview
// (images inline, prototypes in a frame and in a new tab) and links its Library card.

export type ChoiceKind = "choose" | "changes" | "reject";
export type ChoiceDecide = (kind: ChoiceKind, reason: string, option?: number) => void;

export const isChoice = (ap?: Approval) => !!ap?.options?.length;

// optionName: "V2" or "option 2".
export const optionName = (ap: Approval, n: number) => ap.options?.[n - 1]?.label || `option ${n}`;

// choiceOutcome: what was decided on a choice, in words, for a decided or replaced item.
export function choiceOutcome(ap: Approval): string {
  switch (ap.status) {
    case "APPROVED": return ap.chosen ? `Chosen: ${optionName(ap, ap.chosen)}` : "Approved";
    case "CHANGES_REQUESTED": return "Changes requested";
    case "REJECTED": return "All options rejected";
    default: return "";
  }
}

export function ChoiceApproval({ ap, decide, busy, compact }: { ap: Approval; decide?: ChoiceDecide; busy?: boolean; compact?: boolean }) {
  const [picked, setPicked] = useState(0);
  const [text, setText] = useState("");
  const [need, setNeed] = useState<"" | "changes" | "reject">("");
  const opts = (ap.options ?? []).map((o, i) => ({ o, n: i + 1 }));
  const kept = opts.filter((x) => !x.o.dropped);
  const dropped = opts.filter((x) => x.o.dropped);
  const open = !!decide && ap.status === "PENDING";
  const option = (x: { o: ApprovalOption; n: number }) => (
    <OptionCard key={x.n} ap={ap} o={x.o} n={x.n} picked={picked === x.n} onPick={open ? () => setPicked(x.n) : undefined} compact={compact} />
  );
  return (
    <div className="space-y-3">
      <p className="text-xs font-medium text-muted-foreground">
        {open ? `Choose one of ${opts.length} versions` : choiceOutcome(ap) || `A choice of ${opts.length} versions`}
      </p>
      <div className={cn("grid gap-3", !compact && kept.length > 1 && "sm:grid-cols-2")}>{kept.map(option)}</div>
      {dropped.length > 0 && (
        <details className="group rounded-xl border bg-muted/20 px-3 py-2">
          <summary className="flex min-h-8 cursor-pointer list-none items-center gap-1 text-xs font-medium text-muted-foreground hover:text-foreground">
            <ChevronRight className="size-3.5 transition-transform group-open:rotate-90" />
            Dropped by {[...new Set(dropped.map((x) => x.o.dropped_by || "the team"))].join(", ")} ({dropped.length}): still yours to choose
          </summary>
          <div className={cn("mt-2 grid gap-3", !compact && dropped.length > 1 && "sm:grid-cols-2")}>{dropped.map(option)}</div>
        </details>
      )}
      {open && (
        <div className="space-y-2">
          <Textarea value={text} onChange={(e) => { setText(e.target.value); setNeed(""); }} rows={2}
            placeholder="A comment on your choice, the changes you want, or why you reject all" aria-label="Comment, changes or reason"
            aria-invalid={!!need} className="min-h-16 text-base md:text-sm" />
          {need && <p className="text-xs text-red-600 dark:text-red-400">{need === "changes" ? "Say what to change." : "Say why none of them works, so the team can fix it."}</p>}
          <div className="flex flex-wrap items-center justify-end gap-2">
            <Button variant="outline" className="h-10 border-red-500/30 text-red-600 hover:bg-red-500/10 hover:text-red-700 dark:text-red-400" disabled={busy}
              onClick={() => (text.trim() ? decide!("reject", text.trim()) : setNeed("reject"))}>
              <X />Reject all
            </Button>
            <Button variant="outline" className="h-10" disabled={busy} onClick={() => (text.trim() ? decide!("changes", text.trim()) : setNeed("changes"))}>
              <MessageSquareReply />Ask for changes
            </Button>
            <Button className="h-10 bg-emerald-600 text-white hover:bg-emerald-600/90 dark:bg-emerald-600" disabled={busy || !picked}
              onClick={() => decide!("choose", text.trim(), picked)}>
              <Check />{picked ? `Choose ${optionName(ap, picked)}` : "Pick an option"}
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

function OptionCard({ ap, o, n, picked, onPick, compact }: { ap: Approval; o: ApprovalOption; n: number; picked: boolean; onPick?: () => void; compact?: boolean }) {
  const chosen = ap.status === "APPROVED" && ap.chosen === n;
  const library = libraryHref({ artifact_id: o.artifact.id, project_id: ap.project_id });
  return (
    <div className={cn("overflow-hidden rounded-xl border", picked && "ring-2 ring-emerald-500", chosen && "border-emerald-500/50", ap.status === "APPROVED" && !chosen && "opacity-70")}>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 px-3 py-2">
        {onPick ? (
          <label className="flex min-h-10 min-w-0 flex-1 cursor-pointer items-center gap-2">
            <input type="radio" name={`choice-${ap.id}`} checked={picked} onChange={onPick} className="size-4 accent-emerald-600" />
            <span className="min-w-0 break-words font-medium">{o.label}</span>
          </label>
        ) : <span className="min-w-0 flex-1 break-words font-medium">{o.label}</span>}
        {ap.recommended === n && <span className="inline-flex items-center gap-1 rounded-full bg-amber-500/15 px-2 text-[11px] font-medium leading-5 text-amber-800 dark:text-amber-200"><Star className="size-3" />Recommended</span>}
        {chosen && <span className="rounded-full bg-emerald-500/15 px-2 text-[11px] font-medium leading-5 text-emerald-700 dark:text-emerald-300">Chosen</span>}
        {ap.status === "APPROVED" && !chosen && <span className="rounded-full bg-muted px-2 text-[11px] font-medium leading-5 text-muted-foreground">Not chosen</span>}
        {library && (
          <Link href={library} className="inline-flex h-8 items-center gap-1 text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline">
            <FolderOpen className="size-3.5" />Library
          </Link>
        )}
      </div>
      {o.note && <p className="px-3 pb-2 text-sm text-muted-foreground">{o.note}</p>}
      {o.dropped && <p className="px-3 pb-2 text-sm text-muted-foreground"><span className="font-medium">Dropped by {o.dropped_by || "the team"}:</span> {o.dropped}</p>}
      <div className={cn("px-3 pb-3", compact && "max-h-72 overflow-hidden")}>
        <ArtifactPreview project={ap.project_id} artifact={{ ...o.artifact, name: o.artifact.name || o.label }} />
      </div>
    </div>
  );
}

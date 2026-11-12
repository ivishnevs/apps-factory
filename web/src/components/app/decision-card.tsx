"use client";

import { useQuery } from "@tanstack/react-query";
import { Check, MessagesSquare, Scale, X } from "lucide-react";
import Link from "@/components/app/link";
import { useState } from "react";
import { MessageVisuals } from "@/components/app/materials/visuals";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api/client";
import type { Decision } from "@/lib/api/types";
import { ownerConversation } from "@/lib/api/types-ops";
import { cn } from "@/lib/utils";
import { useOwnerAction } from "./tasks/use-owner-action";

// DecisionCard (decision-approval-ux): a proposed decision with its options — the recommendation and the
// alternatives — decided in place: approve the chosen option or your own words, reject with a reason, or discuss it
// in the chat with the lead (it stays open). Deciding here or in the chat clears it everywhere; the lead is told.
export function useDecision(id?: string) {
  return useQuery({ queryKey: ["decision", id], queryFn: () => api<Decision>(`/decisions/${encodeURIComponent(id!)}`), enabled: !!id, refetchInterval: 15_000 });
}

export function DecisionCard({ id, compact, inChat }: { id: string; compact?: boolean; inChat?: boolean }) {
  const { data: d } = useDecision(id);
  const { run, pending } = useOwnerAction();
  const [choice, setChoice] = useState<string | null>(null);
  const [own, setOwn] = useState("");
  const [rejecting, setRejecting] = useState(false);
  const [reason, setReason] = useState("");
  if (!d) return null;
  const open = d.status === "proposed" || d.status === "under_discussion";
  if (!open) {
    return (
      <p className={cn("mt-2 text-xs font-medium", d.status === "approved" ? "text-emerald-700 dark:text-emerald-300" : "text-muted-foreground")}>
        {d.status === "approved" ? `Approved: ${d.value}` : d.status === "rejected" ? "Rejected" : d.status.replace(/_/g, " ")}
      </p>
    );
  }
  const options = [d.value, ...(d.alternatives ?? [])];
  const picked = choice === "\u0000own" ? own.trim() : choice ?? d.value;
  const approve = () => run({ Kind: "decide", DecisionID: d.id, Version: d.version, Status: "approved", Value: picked !== d.value ? picked : undefined, Text: picked !== d.value ? `Chose: ${picked}` : "Approved" },
    { title: `Approve “${d.topic}”?`, description: picked, confirm: "Approve" });
  const reject = () => run({ Kind: "decide", DecisionID: d.id, Version: d.version, Status: "rejected", Text: reason.trim() },
    { title: `Reject “${d.topic}”?`, description: `Your reason: ${reason.trim()}`, confirm: "Reject", destructive: true });
  return (
    <div className={cn("mt-3 space-y-3", compact && "text-sm")}>
      <p className="flex items-center gap-1.5 text-sm font-medium"><Scale className="size-3.5 opacity-70" />Decision: {d.topic}</p>
      {!compact && d.rationale && <p className="text-sm text-muted-foreground">{d.rationale}</p>}
      {/* In the chat the proposal's message shows them (inbox-visual-previews). */}
      {!inChat && <MessageVisuals project={d.project_id} at={d.created_at} author={d.created_by} attachments={d.attachments} materials={d.materials} />}
      <div role="radiogroup" aria-label={d.topic} className="grid gap-2">
        {options.map((o, i) => {
          const on = (choice ?? d.value) === o;
          return (
            <button key={i} type="button" role="radio" aria-checked={on} onClick={() => setChoice(o)}
              className={cn("flex min-h-11 items-start gap-3 rounded-xl border px-3 py-2 text-left text-sm transition-colors",
                on ? "border-foreground ring-1 ring-foreground" : "hover:bg-accent/60")}>
              <span className={cn("mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border", on && "border-foreground bg-foreground")}>
                {on && <span className="size-1.5 rounded-full bg-background" />}
              </span>
              <span className="min-w-0 whitespace-pre-wrap break-words">{o}{i === 0 && <span className="ml-1.5 text-xs text-muted-foreground">recommended</span>}</span>
            </button>
          );
        })}
        <button type="button" role="radio" aria-checked={choice === "\u0000own"} onClick={() => setChoice("\u0000own")}
          className={cn("flex min-h-11 items-center gap-3 rounded-xl border px-3 py-2 text-left text-sm text-muted-foreground transition-colors",
            choice === "\u0000own" ? "border-foreground ring-1 ring-foreground" : "hover:bg-accent/60")}>
          <span className={cn("flex size-4 shrink-0 items-center justify-center rounded-full border", choice === "\u0000own" && "border-foreground bg-foreground")} />
          Something else…
        </button>
        {choice === "\u0000own" && (
          <Textarea autoFocus aria-label="Your decision" value={own} onChange={(e) => setOwn(e.target.value)} placeholder="Your decision" className="min-h-20 text-base md:text-sm" />
        )}
      </div>
      {rejecting && (
        <Textarea autoFocus aria-label="Why not" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Why not, and what instead" className="min-h-20 text-base md:text-sm" />
      )}
      <div className="flex flex-wrap gap-2">
        {!rejecting ? (
          <>
            <Button className="h-10" disabled={pending || !picked} onClick={approve}><Check />Approve</Button>
            <Button className="h-10" variant="outline" disabled={pending} onClick={() => setRejecting(true)}><X />Reject</Button>
            {!inChat && (
              <Button className="h-10" variant="ghost" asChild>
                <Link href={`/chat?c=${encodeURIComponent(ownerConversation(d.created_by, d.project_id))}`}><MessagesSquare />Discuss</Link>
              </Button>
            )}
          </>
        ) : (
          <>
            <Button className="h-10" variant="destructive" disabled={pending || !reason.trim()} onClick={reject}><X />Reject</Button>
            <Button className="h-10" variant="outline" onClick={() => { setRejecting(false); setReason(""); }}>Back</Button>
          </>
        )}
      </div>
    </div>
  );
}

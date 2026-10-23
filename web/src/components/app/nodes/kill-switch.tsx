"use client";

import { OctagonX, Play, Power } from "lucide-react";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import type { Pause } from "@/lib/api/types";
import { ago } from "@/lib/format";
import { useOwnerAction } from "./owner-action";

const WORD = "STOP";

// KillSwitch is the company's stop: Stop all pauses everything and revokes the live runs (POST /stop); Start
// lifts it. Stopping asks for a reason and the typed word, then the confirmation.
export function KillSwitch({ stopped }: { stopped?: Pause }) {
  const { run, pending } = useOwnerAction();
  const [open, setOpen] = useState(false);
  const [reason, setReason] = useState("");
  const [word, setWord] = useState("");

  const stop = async () => {
    setOpen(false);
    const ok = await run({ Kind: "stop", Text: reason.trim() },
      { title: "Stop the whole company?", destructive: true, confirm: "Stop all",
        description: `Every live run is revoked and no new run starts until you start the company again.${reason.trim() ? `\n\nReason: ${reason.trim()}` : ""}` },
      "Company stopped");
    if (ok) { setReason(""); setWord(""); }
  };

  if (stopped) {
    return (
      <section className="overflow-hidden rounded-2xl border border-red-500/40 bg-red-500/[0.06] shadow-xs">
        <div className="flex flex-col gap-4 p-4 sm:flex-row sm:items-center md:p-5">
          <div className="flex min-w-0 flex-1 items-start gap-3 sm:items-center">
          <span className="flex size-11 shrink-0 items-center justify-center rounded-full bg-red-500 text-white"><OctagonX className="size-5" /></span>
          <div className="min-w-0 flex-1">
            <h2 className="font-semibold text-red-700 dark:text-red-300">Company stopped</h2>
            <p className="mt-0.5 text-sm text-muted-foreground">
              {stopped.reason ? <>“{stopped.reason}” · </> : null}by {stopped.actor} {ago(stopped.created_at)}. No run starts until you start it.
            </p>
          </div>
          </div>
          <Button className="h-11 px-5" disabled={pending}
            onClick={() => run({ Kind: "start", Text: stopped.reason }, { title: "Start the company?", description: "The kill switch is lifted: runnable tasks start again on free nodes.", confirm: "Start" }, "Company started")}>
            <Play className="size-4" />Start company
          </Button>
        </div>
      </section>
    );
  }

  return (
    <section className="rounded-2xl border bg-card shadow-xs">
      <div className="flex flex-col gap-4 p-4 sm:flex-row sm:items-center md:p-5">
        <div className="flex min-w-0 flex-1 items-start gap-3 sm:items-center">
          <span className="flex size-11 shrink-0 items-center justify-center rounded-full bg-emerald-500/15 text-emerald-600 dark:text-emerald-400"><Power className="size-5" /></span>
        <div className="min-w-0 flex-1">
          <h2 className="font-semibold">Company running</h2>
          <p className="mt-0.5 text-sm text-muted-foreground">The kill switch stops every agent at once: live runs are revoked and nothing new starts.</p>
        </div>
        </div>
        <Button variant="destructive" className="h-11 px-5" disabled={pending} onClick={() => setOpen(true)}>
          <OctagonX className="size-4" />Stop all
        </Button>
      </div>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <form onSubmit={(e) => { e.preventDefault(); if (word.trim().toUpperCase() === WORD) stop(); }}>
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2 text-red-600 dark:text-red-400"><OctagonX className="size-5" />Stop the company</DialogTitle>
              <DialogDescription>
                All live runs are revoked (their work in progress may be lost) and no new run starts on any node until you start the company again.
              </DialogDescription>
            </DialogHeader>
            <div className="mt-5 space-y-4">
              <div className="space-y-1.5">
                <label htmlFor="stop-reason" className="text-sm font-medium">Reason</label>
                <Textarea id="stop-reason" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Why everything stops (shown on the dashboard)" className="min-h-20" />
              </div>
              <div className="space-y-1.5">
                <label htmlFor="stop-word" className="text-sm font-medium">Type <span className="font-mono font-semibold">{WORD}</span> to confirm</label>
                <Input id="stop-word" className="h-10 font-mono" value={word} onChange={(e) => setWord(e.target.value)} autoComplete="off" autoCapitalize="characters" spellCheck={false} />
              </div>
            </div>
            <DialogFooter className="mt-6 gap-2">
              <Button type="button" variant="outline" className="h-10" onClick={() => setOpen(false)}>Cancel</Button>
              <Button type="submit" variant="destructive" className="h-10" disabled={word.trim().toUpperCase() !== WORD}>Stop all</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </section>
  );
}

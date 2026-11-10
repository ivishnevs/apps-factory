"use client";

import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/app/page";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { core } from "@/lib/api/client";

// Session affinity (session-affinity, work-and-conversation-runs): how long a run waits for the node holding its
// agent's session. Work runs wait while that node is active and are released release_minutes after it went inactive;
// a conversation waits at most conversation_wait_minutes, then another node starts it fresh with recent history.
interface Affinity { release_minutes: number; conversation_wait_minutes: number }

const bounds = { release: [10, 7 * 24 * 60], conversation: [1, 24 * 60] } as const;

function minutesError(v: string, [min, max]: readonly [number, number]): string | null {
  const n = Number(v);
  return v.trim() !== "" && Number.isInteger(n) && n >= min && n <= max ? null : `Whole minutes from ${min} to ${max}`;
}

export function SessionSettings({ manage }: { manage: boolean }) {
  const q = useQuery({ queryKey: ["session-affinity"], queryFn: () => core<Affinity>("/settings/company/session_affinity") });
  const [release, setRelease] = useState("");
  const [conv, setConv] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (q.data) { setRelease(String(q.data.release_minutes)); setConv(String(q.data.conversation_wait_minutes)); }
  }, [q.data]);
  const releaseErr = minutesError(release, bounds.release), convErr = minutesError(conv, bounds.conversation);
  const changed = !!q.data && (release !== String(q.data.release_minutes) || conv !== String(q.data.conversation_wait_minutes));
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    if (releaseErr || convErr) return;
    setBusy(true);
    try {
      await core("/settings/company/session_affinity", { method: "PUT", body: JSON.stringify({ release_minutes: Number(release), conversation_wait_minutes: Number(conv) }) });
      await q.refetch();
      toast.success("Session settings saved");
    } catch (err) { toast.error(err instanceof Error ? err.message : String(err)); } finally { setBusy(false); }
  };
  return (
    <form onSubmit={save} className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="block space-y-1.5 text-sm"><span className="font-medium">Work: release after (minutes)</span>
          <Input inputMode="numeric" value={release} onChange={(e) => setRelease(e.target.value)} disabled={!manage || !q.data} />
          {releaseErr && q.data ? <span className="block text-xs text-red-600 dark:text-red-400">{releaseErr}</span>
            : <span className="block text-xs text-muted-foreground">A work run waits while its session&apos;s machine is up; this long after it went offline or paused, another machine starts fresh. Default 90.</span>}
        </label>
        <label className="block space-y-1.5 text-sm"><span className="font-medium">Conversations: wait at most (minutes)</span>
          <Input inputMode="numeric" value={conv} onChange={(e) => setConv(e.target.value)} disabled={!manage || !q.data} />
          {convErr && q.data ? <span className="block text-xs text-red-600 dark:text-red-400">{convErr}</span>
            : <span className="block text-xs text-muted-foreground">How long a message waits for the machine holding the conversation, busy or not; then another machine answers with the recent history. Default 10.</span>}
        </label>
      </div>
      {manage && <Button type="submit" className="h-10" disabled={busy || !changed || !!releaseErr || !!convErr}>Save</Button>}
      {q.error ? <ErrorState error={q.error} /> : null}
    </form>
  );
}

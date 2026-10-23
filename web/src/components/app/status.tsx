import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

// Tone of a task status, run phase or outcome; every screen colours states through these.
const tones: Record<string, string> = {
  BACKLOG: "muted", READY: "info", IN_PROGRESS: "accent", IN_REVIEW: "violet", REWORK: "warn", BLOCKED: "bad",
  DONE: "ok", CANCELLED: "muted", QUEUED: "info", CLAIMED: "info", RUNNING: "accent", CANCELLING: "warn", ENDED: "muted",
  succeeded: "ok", failed: "bad", timed_out: "bad", idle_timeout: "warn", max_turns: "warn", setup_failed: "bad",
  paused_out: "warn", revoked: "muted", lost: "bad", stale: "muted",
};
const styles: Record<string, string> = {
  muted: "bg-muted text-muted-foreground",
  info: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  accent: "bg-blue-500/15 text-blue-700 dark:text-blue-300",
  violet: "bg-violet-500/15 text-violet-700 dark:text-violet-300",
  warn: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  bad: "bg-red-500/15 text-red-700 dark:text-red-300",
  ok: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
};
export const toneOf = (s?: string) => tones[s ?? ""] ?? "muted";
export const toneClass = (s?: string) => styles[toneOf(s)];

export function StatusBadge({ value, className }: { value?: string; className?: string }) {
  if (!value) return null;
  return (
    <Badge variant="secondary" className={cn("rounded-full border-0 font-medium", toneClass(value), className)}>
      {value.replace(/_/g, " ").toLowerCase()}
    </Badge>
  );
}

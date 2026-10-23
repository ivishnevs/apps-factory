import { cn } from "@/lib/utils";

// A stable tint per agent name (like the terminal UI's agentColor), so an agent looks the same everywhere.
const tints = [
  "bg-sky-500/15 text-sky-700 dark:text-sky-300",
  "bg-violet-500/15 text-violet-700 dark:text-violet-300",
  "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  "bg-rose-500/15 text-rose-700 dark:text-rose-300",
  "bg-teal-500/15 text-teal-700 dark:text-teal-300",
  "bg-indigo-500/15 text-indigo-700 dark:text-indigo-300",
  "bg-orange-500/15 text-orange-700 dark:text-orange-300",
];

function hash(s: string) {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) h = Math.imul(h ^ s.charCodeAt(i), 16777619);
  return h >>> 0;
}

export const agentTint = (name: string) => tints[hash(name) % tints.length];

// initials: "developer-1" → "D1", "pm" → "PM", "tech lead" → "TL", "lead" → "L".
export function initials(name: string) {
  const parts = name.split(/[\s\-_.]+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return (name.length <= 3 ? name : name[0]).toUpperCase();
}

export function AgentAvatar({ name, size = "md", className, live }: { name: string; size?: "sm" | "md" | "lg"; className?: string; live?: boolean }) {
  return (
    <span className={cn("relative inline-flex shrink-0", className)}>
      <span aria-hidden
        className={cn("flex items-center justify-center rounded-full font-semibold tracking-tight", agentTint(name),
          size === "sm" && "size-7 text-[10px]", size === "md" && "size-10 text-xs", size === "lg" && "size-11 text-sm")}>
        {initials(name)}
      </span>
      {live && (
        <span className="absolute -bottom-0.5 -right-0.5 flex size-3 items-center justify-center rounded-full bg-background">
          <span className="size-2 animate-pulse rounded-full bg-emerald-500" />
        </span>
      )}
    </span>
  );
}

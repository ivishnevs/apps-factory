"use client";

import { Hourglass } from "lucide-react";
import { duration, useNow } from "@/components/app/run/use-now";
import { cn } from "@/lib/utils";

// Countdown to a deadline (Unix seconds): calm while far, amber under 2 h, red when overdue.
export function Countdown({ at, verb = "Due" }: { at: number; verb?: string }) {
  const now = useNow(15_000) / 1000;
  const left = at - now;
  const over = left <= 0;
  const soon = !over && left < 2 * 3600;
  return (
    <span title={new Date(at * 1000).toLocaleString()}
      className={cn("inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium tabular-nums",
        over ? "bg-red-500/10 text-red-700 dark:text-red-300" : soon ? "bg-amber-500/10 text-amber-700 dark:text-amber-300" : "bg-muted text-muted-foreground")}>
      <Hourglass className="size-3" />
      {over ? `${verb === "Expires" ? "Expired" : "Overdue"} ${duration(-left)} ago` : `${verb} in ${duration(left).replace(/ \d+s$/, "")}`}
    </span>
  );
}

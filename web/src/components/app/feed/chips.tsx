"use client";

import { cn } from "@/lib/utils";

// Live connection state of a stream.
export function LiveIndicator({ connected }: { connected: boolean }) {
  return (
    <span role="status" className={cn("inline-flex h-8 items-center gap-2 rounded-full border px-3 text-xs font-medium",
      connected ? "text-emerald-700 dark:text-emerald-300" : "text-muted-foreground")}>
      <span className="relative flex size-2">
        {connected && <span className="absolute inline-flex size-full animate-ping rounded-full bg-emerald-500 opacity-50" />}
        <span className={cn("relative inline-flex size-2 rounded-full", connected ? "bg-emerald-500" : "bg-muted-foreground/40")} />
      </span>
      {connected ? "Live" : "Reconnecting…"}
    </span>
  );
}

// Filter chips: one row that scrolls sideways inside itself on narrow screens.
export function Chips<T extends string>({ value, onChange, options, label }: {
  value: T; onChange: (v: T) => void; options: { value: T; label: string; count?: number; tone?: "bad" }[]; label: string;
}) {
  return (
    <div role="radiogroup" aria-label={label} className="-mx-4 flex gap-2 overflow-x-auto px-4 pb-1 [scrollbar-width:none] md:mx-0 md:flex-wrap md:px-0">
      {options.map((o) => {
        const on = o.value === value;
        return (
          <button key={o.value} type="button" role="radio" aria-checked={on} onClick={() => onChange(o.value)}
            className={cn("inline-flex h-10 shrink-0 items-center gap-1.5 rounded-full border px-4 text-sm transition-colors md:h-9",
              on ? "border-foreground bg-foreground text-background" : "bg-background text-muted-foreground hover:bg-accent hover:text-foreground")}>
            {o.label}
            {!!o.count && (
              <span className={cn("rounded-full px-1.5 text-[11px] font-semibold tabular-nums",
                o.tone === "bad" ? "bg-red-500 text-white" : on ? "bg-background/20" : "bg-muted")}>{o.count}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}

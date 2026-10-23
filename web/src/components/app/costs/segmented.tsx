"use client";

import { cn } from "@/lib/utils";

// Segmented is a small radio group styled as a pill switch (period pickers and the like).
export function Segmented<T extends string>({ value, onChange, options, label, className }: {
  value: T; onChange: (v: T) => void; options: { value: T; label: string }[]; label: string; className?: string;
}) {
  return (
    <div role="radiogroup" aria-label={label} className={cn("inline-flex h-10 items-center rounded-full bg-muted p-1", className)}
      onKeyDown={(e) => {
        const i = options.findIndex((o) => o.value === value);
        const d = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
        if (d) { e.preventDefault(); onChange(options[(i + d + options.length) % options.length].value); }
      }}>
      {options.map((o) => {
        const on = o.value === value;
        return (
          <button key={o.value} type="button" role="radio" aria-checked={on} tabIndex={on ? 0 : -1} onClick={() => onChange(o.value)}
            className={cn("relative h-8 min-w-12 rounded-full px-3.5 after:absolute after:-inset-y-1 after:inset-x-0 text-sm font-medium outline-none transition-all focus-visible:ring-[3px] focus-visible:ring-ring/50",
              on ? "bg-background text-foreground shadow-sm dark:bg-input/60" : "text-muted-foreground hover:text-foreground")}>
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

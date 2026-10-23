import { cn } from "@/lib/utils";

// Field: a labelled form control with an optional hint and error, linked for screen readers.
export function Field({ id, label, hint, error, children, className }: {
  id: string; label: string; hint?: React.ReactNode; error?: string; children: React.ReactNode; className?: string;
}) {
  return (
    <div className={cn("space-y-1.5", className)}>
      <label htmlFor={id} className="block text-sm font-medium">{label}</label>
      {children}
      {error ? <p id={`${id}-error`} className="text-xs text-red-600 dark:text-red-400">{error}</p>
        : hint ? <p id={`${id}-hint`} className="text-xs text-muted-foreground">{hint}</p> : null}
    </div>
  );
}

// Inputs on touch screens get 40px+ targets and 16px text (no iOS zoom on focus).
export const touchInput = "h-11 text-base md:h-9 md:text-sm";

import { ChevronRight } from "lucide-react";
import Link from "next/link";
import { cn } from "@/lib/utils";

// Section: a titled card of the dashboard, with an optional "see all" link.
export function Section({ title, count, href, linkLabel = "View all", action, children, className, tone }: {
  title: string; count?: number; href?: string; linkLabel?: string; action?: React.ReactNode; children: React.ReactNode; className?: string; tone?: "attention";
}) {
  return (
    <section aria-label={title} className={cn("overflow-hidden rounded-2xl border bg-card shadow-xs", tone === "attention" && "border-amber-500/40 dark:border-amber-400/25", className)}>
      <header className="flex items-center gap-2 px-4 pt-3.5 pb-2.5 sm:px-5">
        <h2 className="text-sm font-semibold tracking-tight">{title}</h2>
        {count !== undefined && <span className="rounded-full bg-muted px-1.5 text-xs font-medium leading-5 tabular-nums text-muted-foreground">{count}</span>}
        {action && <div className="ml-auto flex items-center">{action}</div>}
        {href && (
          <Link href={href} className={cn("-mr-2 inline-flex h-10 items-center gap-0.5 rounded-md px-2 text-xs font-medium text-muted-foreground transition-colors hover:text-foreground", !action && "ml-auto")}>
            {linkLabel}<ChevronRight className="size-3.5" />
          </Link>
        )}
      </header>
      {children}
    </section>
  );
}

// Row: a tappable list row inside a Section.
export function Row({ href, children, className }: { href: string; children: React.ReactNode; className?: string }) {
  return (
    <Link href={href} className={cn("flex min-h-14 items-center gap-3 border-t px-4 py-3 transition-colors hover:bg-muted/50 focus-visible:bg-muted/60 focus-visible:outline-none sm:px-5", className)}>
      {children}
    </Link>
  );
}

// Stat: one figure of the stats strip.
export function Stat({ label, value, sub, href, icon: Icon, accent }: {
  label: string; value: React.ReactNode; sub?: React.ReactNode; href: string; icon: React.ElementType; accent?: string;
}) {
  return (
    <Link href={href} className="group rounded-2xl border bg-card p-4 shadow-xs transition-colors hover:border-foreground/20 focus-visible:outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50">
      <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
        <Icon aria-hidden className={cn("size-3.5", accent)} />{label}
      </div>
      <div className="mt-2 text-2xl font-semibold tracking-tight tabular-nums">{value}</div>
      {sub && <div className="mt-0.5 truncate text-xs text-muted-foreground">{sub}</div>}
    </Link>
  );
}

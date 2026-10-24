"use client";

import { Activity, Bot, CircleDollarSign, FolderOpen, Home, Inbox, KanbanSquare, MessageSquare, MoreHorizontal, Server } from "lucide-react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useOverview } from "@/lib/api/client";
import { cn } from "@/lib/utils";
import { ConfirmProvider } from "./confirm";

// Navigation: a sidebar on wide screens, a bottom tab bar on phones (the first five; the rest under More).
export const nav = [
  { href: "/", label: "Home", icon: Home },
  { href: "/tasks", label: "Tasks", icon: KanbanSquare },
  { href: "/inbox", label: "Inbox", icon: Inbox, badge: "inbox" as const },
  { href: "/feed", label: "Activity", icon: Activity },
  { href: "/library", label: "Library", icon: FolderOpen },
  { href: "/chat", label: "Chat", icon: MessageSquare },
  { href: "/costs", label: "Costs", icon: CircleDollarSign },
  { href: "/nodes", label: "Nodes", icon: Server },
];
const phoneTabs = ["/", "/tasks", "/inbox", "/feed"];

const active = (path: string, href: string) => (href === "/" ? path === "/" : path === href || path.startsWith(href + "/"));

export function Shell({ children }: { children: React.ReactNode }) {
  const path = usePathname();
  const { data } = useOverview();
  const pending = data?.pending.total ?? 0;
  const stopped = data?.pauses.some((p) => p.scope === "company");
  return (
    <ConfirmProvider>
      <div className="flex min-h-dvh">
        <aside className="sticky top-0 hidden h-dvh w-60 shrink-0 flex-col border-r bg-sidebar px-3 py-4 md:flex">
          <Link href="/" className="mb-6 flex items-center gap-2 px-2">
            <span className="flex size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground"><Bot className="size-4" /></span>
            <span className="min-w-0">
              <span className="block text-sm font-semibold leading-tight">App Factory</span>
              <span className="block truncate text-xs text-muted-foreground">{data?.health.company ?? "…"}</span>
            </span>
          </Link>
          <nav className="flex flex-col gap-1">
            {nav.map((n) => (
              <Link key={n.href} href={n.href}
                className={cn("flex items-center gap-3 rounded-lg px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
                  active(path, n.href) && "bg-accent font-medium text-foreground")}>
                <n.icon className="size-4" />
                <span className="flex-1">{n.label}</span>
                {n.badge && pending > 0 && <span className="rounded-full bg-red-500 px-1.5 text-[11px] font-semibold text-white">{pending}</span>}
              </Link>
            ))}
          </nav>
          <div className="mt-auto px-2 text-xs text-muted-foreground">
            {stopped ? <span className="font-medium text-red-500">● Company stopped</span> : <span>● Running</span>}
          </div>
        </aside>
        <div className="flex min-w-0 flex-1 flex-col">
          {stopped && <div className="bg-red-500 px-4 py-1.5 text-center text-sm font-medium text-white">Company stopped: no new runs start</div>}
          <main className="mx-auto w-full max-w-6xl flex-1 px-4 pb-28 pt-5 md:px-8 md:pb-10 md:pt-8">{children}</main>
        </div>
        <nav className="fixed inset-x-0 bottom-0 z-40 flex border-t bg-background/90 pb-[env(safe-area-inset-bottom)] backdrop-blur md:hidden">
          {[...nav.filter((n) => phoneTabs.includes(n.href)), { href: "/more", label: "More", icon: MoreHorizontal }].map((n) => (
            <Link key={n.href} href={n.href}
              className={cn("relative flex flex-1 flex-col items-center gap-0.5 py-2 text-[11px] text-muted-foreground",
                (active(path, n.href) || (n.href === "/more" && ["/library", "/chat", "/costs", "/nodes", "/more"].some((h) => active(path, h)))) && "text-foreground")}>
              <n.icon className="size-5" />
              {n.label}
              {"badge" in n && n.badge && pending > 0 && (
                <span className="absolute left-1/2 top-1 ml-2 rounded-full bg-red-500 px-1.5 text-[10px] font-semibold leading-4 text-white">{pending}</span>
              )}
            </Link>
          ))}
        </nav>
      </div>
    </ConfirmProvider>
  );
}

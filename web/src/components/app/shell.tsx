"use client";

import { Activity, Bot, Building2, ChevronsUpDown, CircleDollarSign, FolderOpen, Home, Inbox, KanbanSquare, LogOut, MessageSquare, MoreHorizontal, Server, Settings, Users } from "lucide-react";
import Link, { useAppPath } from "@/components/app/link";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { signOut, useCompanyInfo, useMe, useOverview } from "@/lib/api/client";
import { useCompany } from "@/lib/company";
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
  { href: "/team", label: "Team", icon: Users },
  { href: "/settings", label: "Settings", icon: Settings },
  { href: "/companies", label: "Companies", icon: Building2, operator: true },
];

// visibleNav hides the operator's pages from everyone else.
export function useNav() {
  const me = useMe();
  return nav.filter((n) => !("operator" in n && n.operator) || me.data?.kind === "operator");
}
const phoneTabs = ["/", "/tasks", "/inbox", "/feed"];

const active = (path: string, href: string) => (href === "/" ? path === "/" : path === href || path.startsWith(href + "/"));

// CompanyMenu: the company's name; with several companies (the multi-company setting) a switcher; the
// signed-in person's settings and sign-out.
function CompanyMenu() {
  const company = useCompany();
  const info = useCompanyInfo();
  const me = useMe();
  const others = me.data?.multi_company ? me.data.companies.filter((c) => c.company !== company) : [];
  const session = me.data?.kind === "session";
  return (
    <DropdownMenu>
      <DropdownMenuTrigger className="mb-6 flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary text-primary-foreground"><Bot className="size-4" /></span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-semibold leading-tight">{info.data?.name ?? "App Factory"}</span>
          <span className="block truncate text-xs text-muted-foreground">{session ? me.data?.user?.email : info.data?.role ?? "…"}</span>
        </span>
        <ChevronsUpDown className="size-4 shrink-0 text-muted-foreground" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-56">
        {others.length > 0 && (
          <>
            <DropdownMenuLabel className="text-xs text-muted-foreground">Switch company</DropdownMenuLabel>
            {others.map((c) => (
              <DropdownMenuItem key={c.company} onSelect={() => window.location.assign(`/c/${encodeURIComponent(c.company)}`)}>
                <Building2 className="size-4" /><span className="truncate">{c.name}</span>
              </DropdownMenuItem>
            ))}
            <DropdownMenuSeparator />
          </>
        )}
        <DropdownMenuItem asChild><Link href="/settings"><Settings className="size-4" />Settings</Link></DropdownMenuItem>
        {session && <DropdownMenuItem onSelect={signOut}><LogOut className="size-4" />Sign out</DropdownMenuItem>}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function Shell({ children }: { children: React.ReactNode }) {
  const path = useAppPath();
  const { data } = useOverview();
  const items = useNav();
  const pending = data?.pending.total ?? 0;
  const stopped = data?.pauses.some((p) => p.scope === "company");
  return (
    <ConfirmProvider>
      <div className="flex min-h-dvh">
        <aside className="sticky top-0 hidden h-dvh w-60 shrink-0 flex-col border-r bg-sidebar px-3 py-4 md:flex">
          <CompanyMenu />
          <nav className="flex flex-col gap-1">
            {items.map((n) => (
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
          {[...items.filter((n) => phoneTabs.includes(n.href)), { href: "/more", label: "More", icon: MoreHorizontal }].map((n) => (
            <Link key={n.href} href={n.href}
              className={cn("relative flex flex-1 flex-col items-center gap-0.5 py-2 text-[11px] text-muted-foreground",
                (active(path, n.href) || (n.href === "/more" && ["/library", "/chat", "/costs", "/nodes", "/team", "/settings", "/companies", "/more"].some((h) => active(path, h)))) && "text-foreground")}>
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

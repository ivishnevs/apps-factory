"use client";

import { Star } from "lucide-react";
import Link from "next/link";
import { cn } from "@/lib/utils";
import { AgentAvatar } from "./agent-avatar";
import { listTime } from "./time";
import type { Chat } from "./use-chats";

export const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

// preview is a message's first line, the owner's prefixed with "You:" and without the @name it was addressed by.
function preview(c: Chat) {
  const m = c.last;
  if (!m) return null;
  const body = m.body.replace(new RegExp(`^@${escapeRe(c.agent)}[,:]?\\s+`), "").split("\n")[0];
  return m.sender === "owner" ? <><span className="text-foreground/70">You: </span>{body}</> : body;
}

export function ConversationRow({ chat: c, active }: { chat: Chat; active: boolean | "desktop" }) {
  const attention = c.unread > 0 || c.waiting > 0;
  return (
    <Link href={`/chat?c=${encodeURIComponent(c.id)}`} scroll={false} aria-current={active === true ? "page" : undefined}
      className={cn("group flex min-h-[4.25rem] items-center gap-3 rounded-xl px-3 py-2.5 outline-none transition-colors",
        "hover:bg-accent/60 focus-visible:ring-[3px] focus-visible:ring-ring/50", active === true && "bg-accent hover:bg-accent", active === "desktop" && "md:bg-accent md:hover:bg-accent")}>
      <AgentAvatar name={c.agent} live={!!c.replyingRun} />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span className={cn("truncate text-sm", attention ? "font-semibold" : "font-medium")}>{c.agent}</span>
          {c.isDefault && <Star aria-label="Default report" className="size-3 shrink-0 translate-y-px fill-amber-400 text-amber-400" />}
          <span className="truncate text-xs text-muted-foreground">{c.projectName}</span>
          <span className={cn("ml-auto shrink-0 text-xs tabular-nums", attention ? "text-foreground" : "text-muted-foreground")}>{listTime(c.last?.created_at)}</span>
        </div>
        <div className="mt-0.5 flex items-center gap-2">
          <p className={cn("min-w-0 flex-1 truncate text-sm", attention ? "text-foreground" : "text-muted-foreground")}>
            {c.replyingRun ? <span className="text-emerald-600 dark:text-emerald-400">writing a reply…</span>
              : preview(c) ?? <span className="italic text-muted-foreground/70">No messages yet</span>}
          </p>
          {c.waiting > 0 && <span className="shrink-0 rounded-full bg-amber-500/15 px-2 py-0.5 text-[11px] font-medium text-amber-700 dark:text-amber-300">asks you</span>}
          {c.unread > 0 && <span className="flex h-5 min-w-5 shrink-0 items-center justify-center rounded-full bg-primary px-1.5 text-[11px] font-semibold text-primary-foreground">{c.unread}</span>}
        </div>
      </div>
    </Link>
  );
}

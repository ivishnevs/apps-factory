"use client";

import { useQueryClient } from "@tanstack/react-query";
import { MessagesSquare } from "lucide-react";
import { useRouter, useSearchParams } from "next/navigation";
import { Suspense, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { EmptyState, ErrorState } from "@/components/app/page";
import { ConversationRow } from "@/components/app/chat/conversation-list";
import { Thread, type Outgoing } from "@/components/app/chat/thread";
import { useChats } from "@/components/app/chat/use-chats";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { useAction } from "@/lib/api/client";
import { addressee, ownerConversation } from "@/lib/api/types-ops";
import { cn } from "@/lib/utils";

// Chat: the owner's conversations with the direct reports (owner-<agent>-<project>, both directions). A
// message goes to the conversation's report: "say" in its project, with an @name prefix when the report is
// not the default one (POST /messages addresses the default report otherwise); a leading @name writes to
// another report. Phone: the list, then the thread with a back button; desktop: both panes.
export default function Page() {
  return <Suspense><ChatScreen /></Suspense>;
}

function ChatScreen() {
  const params = useSearchParams();
  const router = useRouter();
  const qc = useQueryClient();
  const action = useAction();
  const { overview, chats, reports, def, projects, markSeen } = useChats();
  const [project, setProject] = useState("all");
  const [outgoing, setOutgoing] = useState<Outgoing[]>([]);
  const selected = params.get("c");

  const shown = useMemo(() => chats.filter((c) => project === "all" || c.project === project), [chats, project]);
  const active = chats.find((c) => c.id === selected) ?? shown[0];

  // What the owner has on screen is read: the selected thread (on a phone, only when it is open).
  useEffect(() => {
    const at = active?.last?.created_at;
    if (!active || !at) return;
    if (selected === active.id || matchMedia("(min-width: 768px)").matches) markSeen(active.id, at);
  }, [active, selected, markSeen]);

  const send = async (text: string) => {
    if (!active) return false;
    const body = text.startsWith("@") || active.agent === def ? text : `@${active.agent} ${text}`;
    const to = addressee(body, reports, def);
    if (!to) {
      toast.error(`@name must be one of your direct reports: ${reports.join(", ")}`);
      return false;
    }
    const conv = ownerConversation(to, active.project);
    const key = `${Date.now()}-${Math.random()}`;
    setOutgoing((o) => [...o, { key, conv, text: body, at: Date.now() / 1000 }]);
    if (conv !== active.id) router.replace(`/chat?c=${encodeURIComponent(conv)}`, { scroll: false });
    try {
      await action.mutateAsync({ Kind: "say", Project: active.project, Text: body });
      await qc.refetchQueries({ queryKey: ["messages", conv] });
      return true;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
      return false;
    } finally {
      setOutgoing((o) => o.filter((x) => x.key !== key));
    }
  };

  const loading = overview.isLoading;
  const totalUnread = chats.reduce((n, c) => n + c.unread, 0);

  return (
    <div className={cn(
      // The chat fills the screen between the shell's paddings: full-bleed on a phone, a card on desktop.
      "-mx-4 -mt-5 md:mx-0 md:mt-0",
      selected ? "-mb-28 h-[calc(100dvh-3.5rem-env(safe-area-inset-bottom))]" : "min-h-[50vh]",
      "md:-mb-0 md:grid md:h-[calc(100dvh-4.5rem)] md:grid-cols-[minmax(18rem,22rem)_1fr] md:overflow-hidden md:rounded-2xl md:border md:bg-card md:shadow-xs",
    )}>
      <section aria-label="Conversations" className={cn("flex min-h-0 flex-col md:border-r", selected && "hidden md:flex")}>
        <div className="flex items-end justify-between gap-3 px-4 pb-3 pt-5 md:px-4 md:pt-5">
          <div className="min-w-0">
            <h1 className="text-2xl font-semibold tracking-tight md:text-xl">Chat</h1>
            <p className="mt-0.5 text-sm text-muted-foreground">
              {totalUnread > 0 ? `${totalUnread} unread` : "Your direct reports"}
            </p>
          </div>
          {projects.length > 1 && (
            <Select value={project} onValueChange={setProject}>
              <SelectTrigger className="h-10 w-auto max-w-[45%] rounded-full" aria-label="Project"><SelectValue /></SelectTrigger>
              <SelectContent position="popper" align="end">
                <SelectItem value="all">All projects</SelectItem>
                {projects.map((p) => <SelectItem key={p.id} value={p.id}>{p.name || p.id}</SelectItem>)}
              </SelectContent>
            </Select>
          )}
        </div>
        <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-2 pb-4">
          {overview.error && <div className="px-2"><ErrorState error={overview.error} /></div>}
          {loading && Array.from({ length: 5 }, (_, i) => (
            <div key={i} className="flex items-center gap-3 px-3 py-3">
              <Skeleton className="size-10 rounded-full" />
              <div className="flex-1 space-y-2"><Skeleton className="h-3.5 w-1/3" /><Skeleton className="h-3 w-3/4" /></div>
            </div>
          ))}
          {!loading && !overview.error && reports.length === 0 && (
            <EmptyState className="mx-2" icon={MessagesSquare} title="No direct reports"
              hint="No agent reports to you yet. Set reports_to: owner on an agent (the CEO or a PM) to talk to it here." />
          )}
          {shown.map((c) => <ConversationRow key={c.id} chat={c} active={c.id !== active?.id ? false : selected ? true : "desktop"} />)}
        </div>
      </section>

      <section aria-label="Thread" className={cn("min-h-0 md:flex md:flex-col", selected ? "flex h-full flex-col" : "hidden")}>
        {active ? (
          <Thread chat={active} outgoing={outgoing} onSend={send} onBack="/chat" />
        ) : (
          <div className="hidden h-full items-center justify-center md:flex">
            {loading ? <Skeleton className="h-24 w-64 rounded-2xl" /> : <EmptyState icon={MessagesSquare} title="No conversation selected" className="border-0" />}
          </div>
        )}
      </section>
    </div>
  );
}

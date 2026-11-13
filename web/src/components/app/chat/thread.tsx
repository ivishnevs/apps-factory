"use client";

import { DecisionCard } from "@/components/app/decision-card";
import { MessageVisuals } from "@/components/app/materials/visuals";
import { usePerson } from "@/lib/person";
import { ArrowLeft, ArrowUp, CircleHelp, Hash, Radio, Star, X } from "lucide-react";
import { toast } from "sonner";
import Link from "@/components/app/link";
import { Fragment, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import type { Message, Question } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import { ErrorState } from "@/components/app/page";
import { useOverview, useTopic, useTopics, useTopicWrite } from "@/lib/api/client";
import type { TopicView } from "@/lib/api/types-topics";
import { cleanBody } from "@/lib/people-text";
import { TopicCard } from "@/components/app/inbox/topic-card";
import { useOwnerAction as useTaskAction } from "@/components/app/tasks/use-owner-action";
import { useOwnerAction } from "@/components/app/nodes/owner-action";
import { AgentAvatar } from "./agent-avatar";
import { clock, dayKey, dayLabel } from "./time";
import type { Chat } from "./use-chats";
import { itemAgent, itemTask, OpenItemCard, useOpenItemHandlers, type OpenItem } from "@/components/app/inbox/open-items";

export interface Outgoing { key: string; conv: string; text: string; at: number; failed?: boolean }

// Body text with a leading @name shown as a mention.
function Body({ text, className }: { text: string; className?: string }) {
  const m = /^@[\w.-]+[,:]?/.exec(text);
  return (
    <p className={cn("whitespace-pre-wrap break-words text-[15px] leading-relaxed md:text-sm", className)}>
      {m && <span className="font-medium opacity-70">{m[0]}</span>}
      {m ? text.slice(m[0].length) : text}
    </p>
  );
}

function Questions({ msg, agent }: { msg: Message; agent: string }) {
  const { run, pending } = useOwnerAction();
  const open = msg.sender !== "owner" && msg.message_type === "question" && msg.status === "PENDING";
  const answer = (q: Question, option: string) =>
    run({ Kind: "answer", MessageID: msg.id, Version: msg.version, Text: option, Answers: { [q.id]: option } },
      { title: `Answer ${agent}?`, description: `${q.question}\n\nYour answer: ${option}`, confirm: "Send answer" }, `Answered: ${option}`);
  return (
    <div className="mt-2 space-y-3 border-t border-foreground/10 pt-2.5">
      {(msg.questions ?? []).map((q) => {
        const chosen = msg.answers?.[q.id];
        return (
          <div key={q.id}>
            <p className="flex items-start gap-1.5 text-sm font-medium"><CircleHelp className="mt-0.5 size-3.5 shrink-0 opacity-60" />{q.question}</p>
            {!!q.options?.length && (
              <div className="mt-2 flex flex-wrap gap-1.5">
                {q.options.map((o) => (
                  <Button key={o} type="button" size="sm" variant={chosen === o ? "default" : "outline"} disabled={!open || pending}
                    onClick={() => answer(q, o)}
                    className={cn("h-auto min-h-10 max-w-full whitespace-normal rounded-2xl px-4 py-2 text-left [overflow-wrap:anywhere] disabled:opacity-100", !open && chosen !== o && "opacity-50")}>
                    {o}
                  </Button>
                ))}
              </div>
            )}
          </div>
        );
      })}
      {open && <p className="text-xs font-medium text-amber-700 dark:text-amber-300">Waits on you{msg.blocking ? " · blocks work" : ""}</p>}
    </div>
  );
}

function Bubble({ msg, agent, first, last }: { msg: Message; agent: string; first: boolean; last: boolean }) {
  const person = usePerson();
  const mine = msg.sender === "owner";
  const kind = msg.message_type && !["question", "answer"].includes(msg.message_type) ? msg.message_type.replace(/_/g, " ") : "";
  const answers = Object.entries(msg.answers ?? {}).filter(([k]) => !(msg.questions ?? []).some((q) => q.id === k));
  return (
    <div className={cn("flex items-end gap-2", mine ? "justify-end" : "justify-start", first ? "mt-4" : "mt-1")}>
      {!mine && (last ? <AgentAvatar name={msg.sender} size="sm" className="mb-5" /> : <span className="w-7 shrink-0" />)}
      <div className={cn("flex min-w-0 max-w-[85%] flex-col md:max-w-[70%]", mine ? "items-end" : "items-start")}>
        <div className={cn("min-w-0 max-w-full rounded-2xl px-3.5 py-2",
          mine ? "bg-primary text-primary-foreground" : "bg-muted text-foreground",
          mine ? (last ? "rounded-br-md" : "") : (last ? "rounded-bl-md" : ""))}>
          {msg.topic_id && !mine && <TopicChip id={msg.topic_id} />}
          <Body text={msg.text || (msg.approval_id || msg.escalation_reason ? cleanBody(msg.body) : msg.body)} />
          <MessageVisuals project={msg.project_id} at={msg.created_at} author={msg.sender} attachments={msg.attachments} materials={msg.materials} className="mt-2" />
          {!!msg.questions?.length && <Questions msg={msg} agent={agent} />}
          {msg.decision_id && !mine && msg.message_type === "review_request" && <DecisionCard id={msg.decision_id} compact inChat />}
          {!mine && <CreatedTasks body={msg.body} />}
          {answers.map(([k, v]) => <p key={k} className="mt-1 text-xs opacity-70">↳ {k}: {String(v)}</p>)}
        </div>
        {msg.topic_id && mine && <TopicChip id={msg.topic_id} className="mt-1" />}
        {last && (
          <span className="mt-1 px-1 text-[11px] tabular-nums text-muted-foreground">
            {mine && msg.person && person(msg.person, msg.person_id) !== "you" && `${msg.person} · `}{clock(msg.created_at)}{kind && ` · ${kind}`}{msg.may_be_outdated && " · may be outdated"}
          </span>
        )}
      </div>
    </div>
  );
}

function Typing({ agent }: { agent: string }) {
  return (
    <div className="mt-4 flex items-end gap-2" aria-live="polite">
      <AgentAvatar name={agent} size="sm" />
      <div className="flex items-center gap-1 rounded-2xl rounded-bl-md bg-muted px-4 py-3" aria-label={`${agent} is writing a reply`}>
        {[0, 150, 300].map((d) => <span key={d} className="size-1.5 animate-bounce rounded-full bg-muted-foreground/60" style={{ animationDelay: `${d}ms` }} />)}
      </div>
    </div>
  );
}

export function Thread({ chat, outgoing, onSend, onBack }: {
  chat: Chat; outgoing: Outgoing[]; onSend: (text: string) => Promise<boolean>; onBack: string;
}) {
  const scroller = useRef<HTMLDivElement>(null);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  // people-topics D9: the composer can write into an open topic's thread instead of the chat at large.
  const [topic, setTopic] = useState<TopicView | null>(null);
  const { reply } = useTopicWrite();
  const mine = outgoing.filter((o) => o.conv === chat.id);
  const count = chat.messages.length + mine.length + (chat.replyingRun ? 1 : 0) + chat.asks.length;

  // Stay at the newest message when the conversation changes or a message arrives.
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [chat.id, count]);
  useEffect(() => { setText(""); setTopic(null); }, [chat.id]);

  const send = async () => {
    const t = text.trim();
    if (!t || busy) return;
    setBusy(true);
    setText("");
    const ok = topic ? await reply.mutateAsync({ topic: topic.id, body: t }).then(() => true, (e: Error) => { toast.error(e.message); return false; }) : await onSend(t);
    if (!ok) setText(t); // nothing is lost
    setBusy(false);
  };

  // Groups: consecutive messages of one sender within 5 minutes share a tail and a time.
  const items: (Message | Outgoing)[] = [...chat.messages, ...mine];
  const sender = (x: Message | Outgoing) => ("sender" in x ? x.sender : "owner");
  const at = (x: Message | Outgoing) => ("created_at" in x ? x.created_at : x.at);
  // Each open topic's card follows its first message here; a topic without one here shows at the end.
  const cardAfter = new Map<string, TopicView[]>();
  const loose: TopicView[] = [];
  for (const t of chat.topics) {
    const first = chat.messages.find((m) => m.topic_id === t.id);
    if (first) cardAfter.set(first.id, [...(cardAfter.get(first.id) ?? []), t]);
    else loose.push(t);
  }
  const card = (t: TopicView) => (
    <div key={`topic-${t.id}`} className="mt-4"><TopicCard topic={t} projectName={chat.projectName} compact onReply={setTopic} /></div>
  );

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex h-16 shrink-0 items-center gap-3 border-b px-2 md:px-5">
        <Button asChild variant="ghost" size="icon-lg" className="md:hidden" aria-label="Back to conversations">
          <Link href={onBack} scroll={false}><ArrowLeft className="size-5" /></Link>
        </Button>
        <AgentAvatar name={chat.agent} live={!!chat.replyingRun} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <h2 className="truncate font-semibold leading-tight">{chat.agent}</h2>
            {chat.isDefault && <Star aria-label="Default report" className="size-3.5 fill-amber-400 text-amber-400" />}
          </div>
          <p className="truncate text-xs text-muted-foreground">{[chat.role, chat.projectName].filter(Boolean).join(" · ")}</p>
        </div>
        {chat.replyingRun && (
          <Button asChild variant="outline" size="sm" className="h-10 rounded-full">
            <Link href={`/runs/${chat.replyingRun}`}><Radio className="size-4 text-emerald-500" />Live</Link>
          </Button>
        )}
      </header>

      <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-3 pb-4 md:px-6" role="log" aria-label={`Conversation with ${chat.agent}`}>
        {chat.error ? <div className="pt-4"><ErrorState error={chat.error} /></div> : null}
        {chat.loading && (
          <div className="space-y-4 pt-6">
            <Skeleton className="h-12 w-2/3 rounded-2xl" /><Skeleton className="ml-auto h-10 w-1/2 rounded-2xl" /><Skeleton className="h-16 w-3/5 rounded-2xl" />
          </div>
        )}
        {!chat.loading && !chat.error && items.length === 0 && !chat.replyingRun && chat.asks.length === 0 && (
          <div className="flex h-full flex-col items-center justify-center px-6 text-center">
            <AgentAvatar name={chat.agent} size="lg" />
            <p className="mt-3 font-medium">Start a conversation with {chat.agent}</p>
            <p className="mt-1 max-w-xs text-sm text-muted-foreground">
              Ask about {chat.projectName}, give direction or a new goal. {chat.agent} answers here and hands the work on to the team.
            </p>
          </div>
        )}
        {/* One block per day: its date label sticks only while the day is on screen, so labels do not pile up. */}
        {days(items, at).map((day) => (
          <div key={day[0]}>
            <div className="sticky top-0 z-10 flex justify-center pb-1 pt-4">
              <span className="rounded-full border bg-background px-3 py-1 text-[11px] font-medium text-muted-foreground">{dayLabel(at(items[day[0]]))}</span>
            </div>
            {day.map((i) => {
          const x = items[i];
          const prev = items[i - 1], next = items[i + 1];
          const newDay = i === day[0];
          const first = newDay || !prev || sender(prev) !== sender(x) || at(x) - at(prev) > 300;
          const last = !next || sender(next) !== sender(x) || at(next) - at(x) > 300 || dayKey(at(next)) !== dayKey(at(x));
          return (
            <Fragment key={"id" in x ? x.id : x.key}>
              {"id" in x ? <><Bubble msg={x} agent={chat.agent} first={first} last={last} />{cardAfter.get(x.id)?.map(card)}</> : (
                <div className={cn("flex flex-col items-end", first ? "mt-4" : "mt-1")}>
                  <div className={cn("max-w-[85%] rounded-2xl rounded-br-md bg-primary px-3.5 py-2 text-primary-foreground md:max-w-[70%]", !x.failed && "opacity-60")}>
                    <Body text={x.text} />
                  </div>
                  <span className="mt-1 px-1 text-[11px] text-muted-foreground">{x.failed ? "Not sent" : "Sending…"}</span>
                </div>
              )}
            </Fragment>
          );
            })}
          </div>
        ))}
        {chat.pending && mine.length === 0 && (
          <p className="mt-1 px-1 text-right text-[11px] text-muted-foreground" aria-live="polite">{chat.agent}: {chat.pending}</p>
        )}
        {loose.map(card)}
        {chat.asks.length > 0 && <TeamQuestions items={chat.asks} projectName={chat.projectName} />}
        {chat.replyingRun && <Typing agent={chat.agent} />}
      </div>

      <form className="shrink-0 border-t bg-background/80 p-2 backdrop-blur md:p-3" onSubmit={(e) => { e.preventDefault(); send(); }}>
        {topic && (
          <div className="mb-1.5 flex min-w-0 items-center gap-1.5 px-2 text-xs text-muted-foreground">
            <Hash className="size-3.5 shrink-0" />
            <span className="min-w-0 truncate">In topic: <span className="font-medium text-foreground">{topic.brief.title || "this topic"}</span></span>
            <button type="button" onClick={() => setTopic(null)} aria-label="Write to the chat, not the topic" className="-my-2 ml-auto flex size-9 shrink-0 items-center justify-center rounded-full hover:bg-accent hover:text-foreground"><X className="size-3.5" /></button>
          </div>
        )}
        <div className="flex items-end gap-2 rounded-3xl border bg-card py-1 pl-4 pr-1 shadow-xs transition-[box-shadow,border-color] focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/30">
          <label htmlFor="chat-composer" className="sr-only">Message {chat.agent}</label>
          <textarea id="chat-composer" rows={1} value={text} onChange={(e) => setText(e.target.value)}
            placeholder={topic ? `Ask ${chat.agent} about this topic…` : `Message ${chat.agent}…`} maxLength={16384}
            onKeyDown={(e) => {
              // Enter sends on a keyboard; on a touch screen it is a new line and the button sends.
              if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing && !matchMedia("(pointer: coarse)").matches) {
                e.preventDefault();
                send();
              }
            }}
            className="max-h-40 min-h-10 flex-1 resize-none bg-transparent py-2.5 text-base outline-none [field-sizing:content] placeholder:text-muted-foreground md:text-sm" />
          <Button type="submit" size="icon-lg" disabled={!text.trim() || busy} aria-label="Send" className="shrink-0 rounded-full">
            <ArrowUp className="size-5" />
          </Button>
        </div>
        <p className="mt-1.5 hidden px-4 text-[11px] text-muted-foreground md:block">
          Enter sends · Shift+Enter for a new line · start with @name to write to another report
        </p>
      </form>
    </div>
  );
}


// CreatedTasks (lead-brainstorm-mode 2.2): tasks a lead names in its summary, with their state and a Stop for the
// ones not finished, so a person can stop or open just-created work from the reply.
function CreatedTasks({ body }: { body: string }) {
  const { data: ov } = useOverview();
  const { run, pending } = useTaskAction();
  const ids = Array.from(new Set(body.match(/\b[0-9a-f]{32}\b/g) ?? []));
  const tasks = ids.map((id) => ov?.tasks.find((t) => t.id === id)).filter((t): t is NonNullable<typeof t> => !!t && (t as { kind?: string }).kind !== "conversation");
  if (tasks.length === 0) return null;
  return (
    <ul className="mt-2 space-y-1.5 border-t border-foreground/10 pt-2">
      {tasks.map((t) => {
        const open = t.status !== "DONE" && t.status !== "CANCELLED";
        return (
          <li key={t.id} className="flex items-center gap-2 text-xs">
            <Link href={`/tasks/${t.id}`} className="min-w-0 flex-1 truncate underline-offset-4 hover:underline">{t.title}</Link>
            <span className="shrink-0 opacity-70">{t.status.toLowerCase().replace(/_/g, " ")}</span>
            {open && (
              <Button size="sm" variant="outline" className="h-7 shrink-0 px-2 text-xs" disabled={pending}
                onClick={() => run({ Kind: "cancel", TaskID: t.id, Version: t.version, Text: "stopped from the chat" }, { title: `Stop "${t.title}"?`, confirm: "Stop task", destructive: true })}>Stop</Button>
            )}
          </li>
        );
      })}
    </ul>
  );
}

// days splits items (in time order) into runs of indexes of one calendar day.
function days<T>(items: T[], at: (x: T) => number): number[][] {
  const out: number[][] = [];
  items.forEach((x, i) => {
    if (i === 0 || dayKey(at(items[i - 1])) !== dayKey(at(x))) out.push([i]);
    else out[out.length - 1].push(i);
  });
  return out;
}

// TeamQuestions (agent-questions-in-chat): what the project's other agents ask people, open now, as cards answerable
// here; an answer closes the Inbox item and wakes the asking agent. The lead's brief carries them and their answers.
function TeamQuestions({ items, projectName }: { items: OpenItem[]; projectName: string }) {
  const { data: ov } = useOverview();
  const h = useOpenItemHandlers();
  return (
    <div className="mt-6 space-y-4" aria-label="Your team asks">
      {items.map((i) => {
        const agent = itemAgent(i), id = itemTask(i);
        const task = id && !id.startsWith("conv-") ? ov?.tasks.find((t) => t.id === id) : undefined;
        return (
          <div key={i.key}>
            <p className="mb-1.5 flex min-w-0 items-center gap-2 px-1 text-xs text-muted-foreground">
              <AgentAvatar name={agent} size="sm" />
              <span className="shrink-0 font-medium text-foreground">{agent} asks</span>
              {task && <Link href={`/tasks/${task.id}`} className="min-w-0 truncate underline-offset-4 hover:underline">{task.title}</Link>}
            </p>
            <OpenItemCard item={i} projectName={projectName} h={h} />
          </div>
        );
      })}
    </div>
  );
}

// TopicChip (people-topics D9): a message of a topic's thread names its topic and links to it in the Inbox.
function TopicChip({ id, className }: { id: string; className?: string }) {
  const listed = useTopics().data?.find((t) => t.id === id);
  const one = useTopic(listed ? undefined : id);
  const title = (listed ?? one.data)?.brief.title;
  if (!title) return null;
  return (
    <Link href={`/inbox#topic-${encodeURIComponent(id)}`}
      className={cn("mb-1 inline-flex max-w-full items-center gap-1 rounded-full border border-current/15 px-2 py-0.5 text-[11px] font-medium opacity-80 hover:opacity-100", className)}>
      <Hash className="size-3 shrink-0" /><span className="truncate">{title}</span>
    </Link>
  );
}

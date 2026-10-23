"use client";

import {
  AlertOctagon, Boxes, Brain, Check, ChevronRight, FileText, Flag, Globe, Info, Loader2, LogOut, Pencil,
  Search, ShieldAlert, SquareTerminal, Wrench, X,
} from "lucide-react";
import { useState } from "react";
import { StatusBadge } from "@/components/app/status";
import type { TxEntry } from "@/lib/api/types-live";
import { cn } from "@/lib/utils";
import { CodeBlock, Markdown } from "./markdown";

const clock = (ms: number) => (ms ? new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" }) : "");

function toolIcon(tool = "") {
  if (tool === "Bash") return SquareTerminal;
  if (tool.startsWith("factory_exit_")) return LogOut;
  if (tool.includes("factory_")) return Boxes;
  if (/^(Read|NotebookRead)$/.test(tool)) return FileText;
  if (/^(Edit|Write|MultiEdit|NotebookEdit)$/.test(tool)) return Pencil;
  if (/^(Grep|Glob|LS)$/.test(tool)) return Search;
  if (/^Web/.test(tool)) return Globe;
  return Wrench;
}

// A run of consecutive tool calls shows as one compact list; everything else stands on its own.
type Group = { kind: "tools"; key: string; items: TxEntry[] } | { kind: "one"; key: string; entry: TxEntry };
function group(entries: TxEntry[]): Group[] {
  const out: Group[] = [];
  for (const e of entries) {
    const last = out[out.length - 1];
    if (e.kind === "tool" && last?.kind === "tools") last.items.push(e);
    else if (e.kind === "tool") out.push({ kind: "tools", key: e.key, items: [e] });
    else out.push({ kind: "one", key: e.key, entry: e });
  }
  return out;
}

export function Transcript({ entries, live }: { entries: TxEntry[]; live: boolean }) {
  return (
    <ol className="space-y-4" aria-label="Transcript">
      {group(entries).map((g) => (
        <li key={g.key} className="animate-in fade-in-0 duration-300">
          {g.kind === "tools" ? <ToolList items={g.items} live={live} /> : <Entry e={g.entry} />}
        </li>
      ))}
    </ol>
  );
}

function Entry({ e }: { e: TxEntry }) {
  switch (e.kind) {
    case "say":
      return (
        <div className="flex gap-3">
          <span className="mt-2 size-1.5 shrink-0 rounded-full bg-foreground/70" aria-hidden />
          <div className="min-w-0 flex-1">
            <Markdown text={e.text ?? ""} />
            <time className="mt-1 block text-[11px] text-muted-foreground/70 tabular-nums">{clock(e.at)}</time>
          </div>
        </div>
      );
    case "think":
      return <Thinking e={e} />;
    case "refusal":
      return (
        <Callout tone="warn" icon={ShieldAlert} title={e.tool === "core" ? "Core refused a call" : e.tool === "exit_guard" ? "Exit refused" : "Command refused"} at={e.at}>
          <RefusalText text={e.text ?? ""} />
        </Callout>
      );
    case "error":
      return <Callout tone="bad" icon={AlertOctagon} title="Error" at={e.at}>{e.text}</Callout>;
    case "info":
      return (
        <div className="flex items-center gap-3 text-xs text-muted-foreground">
          <span className="h-px flex-1 bg-border" />
          <span className="flex items-center gap-1.5"><Info className="size-3.5" />{e.text}</span>
          <span className="h-px flex-1 bg-border" />
        </div>
      );
    case "end":
      return <EndCard e={e} />;
  }
  return null;
}

function Callout({ tone, icon: Icon, title, at, children }: { tone: "warn" | "bad"; icon: React.ElementType; title: string; at: number; children: React.ReactNode }) {
  return (
    <div role="note" className={cn("flex gap-3 rounded-xl border p-3 text-sm",
      tone === "warn" ? "border-amber-500/30 bg-amber-500/[0.06]" : "border-red-500/30 bg-red-500/[0.06]")}>
      <Icon className={cn("mt-0.5 size-4 shrink-0", tone === "warn" ? "text-amber-600 dark:text-amber-400" : "text-red-600 dark:text-red-400")} />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline justify-between gap-2">
          <p className="font-medium">{title}</p>
          <time className="shrink-0 text-[11px] text-muted-foreground tabular-nums">{clock(at)}</time>
        </div>
        <div className="mt-0.5 break-words text-[13px] text-muted-foreground">{children}</div>
      </div>
    </div>
  );
}

// RefusalText drops what the title already says; a command refusal shows its rule and the command.
function RefusalText({ text }: { text: string }) {
  const m = text.match(/^Command refused \(([^,]*), rule ([^)]*)\): ([\s\S]*)$/);
  if (m) {
    return (
      <>
        <span className="block">Rule <span className="font-medium text-foreground">{m[2]}</span> ({m[1]})</span>
        <code className="mt-1 block break-all font-mono text-[12.5px]">$ {m[3]}</code>
      </>
    );
  }
  return <>{text.replace(/^(Exit guard refused the turn's end|Core refused)\s*:?\s*/, "") || text}</>;
}

// prettyResult indents a JSON result; anything else stays as it is.
function prettyResult(s: string) {
  const t = s.trim();
  if (!/^[[{]/.test(t)) return s;
  try { return JSON.stringify(JSON.parse(t), null, 2); } catch { return s; }
}

function Thinking({ e }: { e: TxEntry }) {
  const [open, setOpen] = useState(false);
  const text = e.text ?? "";
  return (
    <div className="rounded-xl border border-dashed">
      <button type="button" onClick={() => setOpen(!open)} aria-expanded={open}
        className="flex min-h-11 w-full items-center gap-2.5 px-3 py-2 text-left text-sm text-muted-foreground transition-colors hover:text-foreground">
        <Brain className="size-4 shrink-0" />
        <span className="shrink-0 font-medium">Thinking</span>
        {!open && <span className="min-w-0 flex-1 truncate italic opacity-80">{text.split("\n")[0]}</span>}
        {open && <span className="flex-1" />}
        <ChevronRight className={cn("size-4 shrink-0 transition-transform", open && "rotate-90")} />
      </button>
      {open && <div className="border-t border-dashed px-3 py-3 text-muted-foreground"><Markdown text={text} className="italic" /></div>}
    </div>
  );
}

function ToolList({ items, live }: { items: TxEntry[]; live: boolean }) {
  return (
    <div className="overflow-hidden rounded-xl border bg-card">
      {items.map((e, i) => <ToolRow key={e.key} e={e} live={live} first={i === 0} />)}
    </div>
  );
}

function ToolRow({ e, live, first }: { e: TxEntry; live: boolean; first: boolean }) {
  const [open, setOpen] = useState(false);
  const Icon = toolIcon(e.tool);
  const running = !e.done && live;
  const brief = e.tool === "Bash" ? `$ ${e.brief ?? ""}` : e.brief;
  return (
    <div className={cn(!first && "border-t", e.isError && "bg-red-500/[0.05]")}>
      <button type="button" onClick={() => setOpen(!open)} aria-expanded={open}
        className="flex min-h-11 w-full items-center gap-2.5 px-3 py-2 text-left transition-colors hover:bg-accent/60">
        <Icon className={cn("size-4 shrink-0", e.isError ? "text-red-600 dark:text-red-400" : "text-muted-foreground")} />
        <span className={cn("shrink-0 font-mono text-[12.5px] font-medium", e.isError && "text-red-700 dark:text-red-300")}>{e.tool}</span>
        <span className="min-w-0 flex-1 truncate font-mono text-[12.5px] text-muted-foreground">{brief}</span>
        <span className="flex shrink-0 items-center gap-2">
          <time className="hidden text-[11px] text-muted-foreground/70 tabular-nums sm:block">{clock(e.at)}</time>
          {running ? <Loader2 className="size-3.5 animate-spin text-blue-500" aria-label="running" />
            : e.isError ? <X className="size-3.5 text-red-500" aria-label="failed" />
            : e.done ? <Check className="size-3.5 text-emerald-500" aria-label="done" /> : <span className="size-3.5" />}
          <ChevronRight className={cn("size-4 text-muted-foreground transition-transform", open && "rotate-90")} />
        </span>
      </button>
      {open && (
        <div className="space-y-3 border-t bg-muted/20 px-3 py-3">
          {e.input && <Section label={e.tool === "Bash" ? "Command" : "Input"}><CodeBlock text={e.input} /></Section>}
          <Section label={e.isError ? "Error" : "Result"}>
            {e.done
              ? e.result
                ? <CodeBlock text={prettyResult(e.result)} className={e.isError ? "border-red-500/30 bg-red-500/[0.06] text-red-800 dark:text-red-200" : undefined} />
                : <p className="text-sm text-muted-foreground">No output</p>
              : <p className="text-sm text-muted-foreground">{live ? "Waiting for the result…" : "No result recorded"}</p>}
          </Section>
        </div>
      )}
    </div>
  );
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <p className="mb-1.5 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">{label}</p>
      {children}
    </div>
  );
}

function EndCard({ e }: { e: TxEntry }) {
  const ok = e.outcome === "succeeded";
  return (
    <div className={cn("rounded-xl border p-4", ok ? "border-emerald-500/30 bg-emerald-500/[0.05]" : "bg-card")}>
      <div className="flex flex-wrap items-center gap-2">
        <Flag className="size-4 text-muted-foreground" />
        <span className="font-medium">Run ended</span>
        <StatusBadge value={e.outcome} />
        <time className="ml-auto text-[11px] text-muted-foreground tabular-nums">{clock(e.at)}</time>
      </div>
      {e.text ? <Markdown text={e.text} className="mt-3" /> : <p className="mt-2 text-sm text-muted-foreground">No result text.</p>}
      {e.brief?.includes("(exit") && !e.brief.includes("(exit none)") &&<p className="mt-2 text-xs text-muted-foreground">{e.brief.slice(e.brief.indexOf("(exit"))}</p>}
    </div>
  );
}

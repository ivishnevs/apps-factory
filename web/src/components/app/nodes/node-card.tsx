"use client";

import { CircleAlert, HardDrive, Pause, Play, Cpu } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { FactoryNode, Pause as PauseT } from "@/lib/api/types";
import { nodeHealth, nodeId, type NodeDriver, type NodeFull } from "@/lib/api/types-ops";
import { ago } from "@/lib/format";
import { cn } from "@/lib/utils";

export const gib = (b?: number) => `${((b ?? 0) / 2 ** 30).toFixed(1)} GiB`;

// care: what of a node needs the owner (internal/tui nodeCare), "" when nothing.
export function nodeCare(n: FactoryNode) {
  const h = nodeHealth(n);
  if (!n.online) return "offline";
  if (h.disk === "critical") return "disk critical";
  const bad = ((n as NodeFull).drivers ?? []).find((d) => !d.ok);
  if (bad) return `${bad.name} disabled`;
  if (h.disk === "low") return "disk low";
  return "";
}

function Driver({ d }: { d: NodeDriver }) {
  const now = Date.now() / 1000;
  const state = !d.ok ? "bad" : d.transient_failures ? "warn" : "ok";
  return (
    <li className="flex items-start gap-2.5 py-1.5">
      <span className={cn("mt-1.5 size-2 shrink-0 rounded-full", state === "ok" && "bg-emerald-500", state === "warn" && "bg-amber-500", state === "bad" && "bg-red-500")} />
      <div className="min-w-0 flex-1 text-sm">
        <div className="flex flex-wrap items-baseline gap-x-2">
          <span className="font-medium">{d.name}</span>
          {d.version && <span className="font-mono text-xs text-muted-foreground">{d.version}</span>}
          <span className={cn("text-xs", state === "ok" && "text-muted-foreground", state === "warn" && "text-amber-700 dark:text-amber-300", state === "bad" && "font-medium text-red-600 dark:text-red-400")}>
            {state === "bad" ? `disabled${d.until && d.until > now ? ` until ${new Date(d.until * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}` : ""}`
              : state === "warn" ? `${d.transient_failures} transient failure${d.transient_failures === 1 ? "" : "s"}` : "enabled"}
          </span>
        </div>
        {!d.ok && <p className="mt-0.5 break-words text-xs text-muted-foreground">{d.check || "probe"} check failed{d.detail ? `: ${d.detail}` : ""}</p>}
      </div>
    </li>
  );
}

export function NodeCard({ node, pause, onPause, onResume, busy }: {
  node: FactoryNode; pause?: PauseT; onPause: () => void; onResume: () => void; busy?: boolean;
}) {
  const n = node as NodeFull;
  const h = nodeHealth(node);
  const id = nodeId(node);
  const slots = n.slots ?? 0;
  const used = n.in_use?.runs ?? 0;
  const care = nodeCare(node);
  const paused = !!pause || h.disk === "critical";
  const state = !n.online ? "offline" : paused ? "paused" : "online";
  const resources = Object.entries(n.resources ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const failed = (h.checks ?? []).filter((c) => c.status === "fail");

  return (
    <article className={cn("flex flex-col rounded-2xl border bg-card shadow-xs", !n.online && "border-red-500/30")}>
      <header className="flex items-start gap-3 p-4 pb-3 md:p-5 md:pb-3">
        <span className={cn("relative mt-1.5 flex size-2.5 shrink-0 rounded-full",
          state === "online" && "bg-emerald-500", state === "paused" && "bg-amber-500", state === "offline" && "bg-red-500")}>
          {state === "online" && <span className="absolute inset-0 animate-ping rounded-full bg-emerald-500/50 [animation-duration:2.5s]" />}
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-2">
            <h3 className="truncate font-semibold">{n.name || id}</h3>
            {n.name && n.name !== id && <span className="font-mono text-xs text-muted-foreground">{id}</span>}
          </div>
          <p className={cn("mt-0.5 text-xs", state === "offline" ? "text-red-600 dark:text-red-400" : "text-muted-foreground")}>
            {state === "offline" ? `Silent since ${ago(n.last_seen)}` : `${state === "paused" ? "Paused" : "Online"} · heartbeat ${ago(n.last_seen)}`}
          </p>
        </div>
        {care && (
          <span className={cn("shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium",
            care === "offline" || care.includes("critical") || care.includes("disabled") ? "bg-red-500/15 text-red-700 dark:text-red-300" : "bg-amber-500/15 text-amber-700 dark:text-amber-300")}>
            {care}
          </span>
        )}
      </header>

      <div className="space-y-4 px-4 pb-4 md:px-5">
        {/* Slots: one cell per slot, filled when a run holds it. */}
        <div>
          <div className="mb-1.5 flex items-baseline justify-between text-xs">
            <span className="font-medium text-muted-foreground">Slots</span>
            <span className="tabular-nums"><span className="font-semibold">{used}</span><span className="text-muted-foreground">/{slots} in use</span></span>
          </div>
          <div className="flex gap-1" aria-hidden>
            {Array.from({ length: Math.max(slots, 1) }, (_, i) => (
              <span key={i} className={cn("h-2 flex-1 rounded-full", i < used ? "bg-blue-500" : "bg-muted")} />
            ))}
          </div>
          {resources.length > 0 && (
            <div className="mt-2 flex flex-wrap gap-1.5">
              {resources.map(([k, cap]) => {
                const held = n.in_use?.resources?.[k] ?? 0;
                return (
                  <span key={k} className={cn("rounded-md border px-2 py-0.5 text-xs tabular-nums", held >= cap && "border-amber-500/40 text-amber-700 dark:text-amber-300")}>
                    {k} {held}/{cap}{held >= cap && " full"}
                  </span>
                );
              })}
            </div>
          )}
        </div>

        <div>
          <p className="text-xs font-medium text-muted-foreground">Drivers</p>
          {(n.drivers ?? []).length === 0 ? <p className="mt-1 text-sm text-red-600 dark:text-red-400">No drivers</p> : (
            <ul className="divide-y divide-border/60">{(n.drivers ?? []).map((d) => <Driver key={d.name} d={d} />)}</ul>
          )}
        </div>

        <div className="grid grid-cols-2 gap-3 text-sm">
          {h.free_disk_bytes !== undefined && (
            <div className="min-w-0">
              <p className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground"><HardDrive className="size-3.5" />Disk</p>
              <p className={cn("mt-0.5 tabular-nums", h.disk === "critical" && "font-medium text-red-600 dark:text-red-400", h.disk === "low" && "font-medium text-amber-700 dark:text-amber-300")}>
                {gib(h.free_disk_bytes)} free
              </p>
              {!!h.disk_low_bytes && <p className="truncate text-[11px] text-muted-foreground">low {gib(h.disk_low_bytes)} · critical {gib(h.disk_critical_bytes)}</p>}
            </div>
          )}
          {!!h.cpus && (
            <div className="min-w-0">
              <p className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground"><Cpu className="size-3.5" />Load</p>
              <p className="mt-0.5 tabular-nums">{(h.load1 ?? 0).toFixed(1)} <span className="text-muted-foreground">on {h.cpus} CPUs</span></p>
            </div>
          )}
        </div>

        {failed.map((c) => (
          <p key={c.check} className="flex items-start gap-2 rounded-lg bg-red-500/5 px-3 py-2 text-xs text-red-700 dark:text-red-300">
            <CircleAlert className="mt-px size-3.5 shrink-0" /><span><span className="font-medium">{c.check}</span> check failed{c.detail ? `: ${c.detail}` : ""}</span>
          </p>
        ))}

        {(n.labels ?? []).length > 0 && (
          <div className="flex flex-wrap gap-1.5">{(n.labels ?? []).map((l) => <span key={l} className="rounded-md bg-muted px-2 py-0.5 text-xs text-muted-foreground">{l}</span>)}</div>
        )}
      </div>

      <footer className="mt-auto flex items-center gap-3 border-t px-4 py-3 md:px-5">
        <p className="min-w-0 flex-1 truncate text-xs text-muted-foreground">
          {pause ? <>Paused by {pause.actor}{pause.reason ? `: ${pause.reason}` : ""}</>
            : !n.online ? "Offline: claims no runs" : h.disk === "critical" ? "Claiming paused: disk below critical" : "Claims new runs"}
        </p>
        {pause ? (
          <Button variant="outline" className="h-10" onClick={onResume} disabled={busy}><Play className="size-4" />Resume</Button>
        ) : (
          <Button variant="outline" className="h-10" onClick={onPause} disabled={busy}><Pause className="size-4" />Pause</Button>
        )}
      </footer>
    </article>
  );
}

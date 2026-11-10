"use client";

import { useConfirm } from "@/components/app/confirm";
import { toast } from "sonner";
import { core } from "@/lib/api/client";
import { useState } from "react";

import { CircleAlert, HardDrive, Pause, Play, Cpu, Pin } from "lucide-react";
import Link from "@/components/app/link";
import { Button } from "@/components/ui/button";
import type { FactoryNode, Pause as PauseT } from "@/lib/api/types";
import { nodeHealth, nodeId, type NodeDriver, type NodeFull } from "@/lib/api/types-ops";
import { ago, displayId } from "@/lib/format";
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

function Driver({ d, nodeId }: { d: NodeDriver; nodeId?: string }) {
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
        {!d.ok && d.check?.endsWith(".account") && nodeId && <AcceptAccount nodeId={nodeId} runtime={d.name} />}
      </div>
    </li>
  );
}

// Tied: what only this node may run now (sticky-work-first, node-ties): tasks whose unsaved work is only here, and
// the queued runs tied to it (that work, or an agent session it holds). The node takes these first when a slot frees.
function Tied({ node }: { node: FactoryNode }) {
  const pinned = node.tied?.work ?? node.tied?.pinned ?? [], waiting = node.tied?.waiting ?? [];
  if (pinned.length === 0 && waiting.length === 0) return null;
  const task = (id: string) => (
    <Link href={`/tasks/${encodeURIComponent(id)}`} className="font-mono underline-offset-4 hover:underline" title={id}>{displayId(id)}</Link>
  );
  return (
    <div>
      <p className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground"><Pin className="size-3.5" />Tied to this node</p>
      {pinned.length > 0 && (
        <p className="mt-1 text-sm">
          Unsaved work of {pinned.length} task{pinned.length === 1 ? "" : "s"}:{" "}
          {pinned.map((id, i) => <span key={id}>{i > 0 && ", "}{task(id)}</span>)}
        </p>
      )}
      {pinned.length > 0 && (
        <p className="mt-0.5 text-[11px] text-muted-foreground">Tied until the work is saved remotely or you reset the workspace.</p>
      )}
      {waiting.length > 0 && (
        <ul className="mt-1 space-y-0.5 text-sm">
          {waiting.map((w) => (
            <li key={w.run_id} className="flex flex-wrap items-baseline gap-x-1.5">
              <span className="text-amber-700 dark:text-amber-300">Waiting:</span>{task(w.task_id)}
              <span className="text-muted-foreground">{w.agent} · {(w.reason ?? (w.tie === "pinned" ? "work" : "session")) === "work" ? "tied: its unsaved work is here" : "tied: its agent session is here"}</span>
            </li>
          ))}
        </ul>
      )}
      {waiting.length > 0 && <p className="mt-1 text-[11px] text-muted-foreground">This node takes them first when a slot frees.</p>}
    </div>
  );
}

export function NodeCard({ node, pause, onPause, onResume, onRevoke, busy }: {
  node: FactoryNode; pause?: PauseT; onPause: () => void; onResume: () => void; onRevoke?: () => void; busy?: boolean;
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
            
          </div>
          <p className={cn("mt-0.5 text-xs", state === "offline" ? "text-red-600 dark:text-red-400" : "text-muted-foreground")}>
            {state === "offline" ? `Silent since ${ago(n.last_seen)}` : `${state === "paused" ? "Paused" : "Online"} · checked in ${ago(n.last_seen)}`}
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
            <span className="font-medium text-muted-foreground">Agents at once</span>
            <span className="tabular-nums"><span className="font-semibold">{used}</span><span className="text-muted-foreground"> of {slots} busy</span></span>
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

        <Tied node={n} />

        <div>
          <p className="text-xs font-medium text-muted-foreground">Runtimes</p>
          {(n.drivers ?? []).length === 0 ? <p className="mt-1 text-sm text-red-600 dark:text-red-400">No drivers</p> : (
            <ul className="divide-y divide-border/60">{(n.drivers ?? []).map((d) => <Driver key={d.name} d={d} nodeId={n.node_id} />)}</ul>
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

        <NodeSecurity sec={(n.health as { security?: SecurityInfo } | undefined)?.security} />

        {(n.labels ?? []).length > 0 && (
          <div className="flex flex-wrap gap-1.5">{(n.labels ?? []).map((l) => <span key={l} className="rounded-md bg-muted px-2 py-0.5 text-xs text-muted-foreground">{l}</span>)}</div>
        )}
      </div>

      <footer className="mt-auto flex items-center gap-3 border-t px-4 py-3 md:px-5">
        <p className="min-w-0 flex-1 truncate text-xs text-muted-foreground">
          {pause ? <>Paused by {pause.actor}{pause.reason ? `: ${pause.reason}` : ""}</>
            : !n.online ? "Offline: takes no work" : h.disk === "critical" ? "Takes no work: the disk is almost full" : "Takes new work"}
        </p>
        {pause ? (
          <Button variant="outline" className="h-10" onClick={onResume} disabled={busy}><Play className="size-4" />Resume</Button>
        ) : (
          <Button variant="outline" className="h-10" onClick={onPause} disabled={busy}><Pause className="size-4" />Pause</Button>
        )}
        {onRevoke && <Button variant="ghost" className="h-10 text-red-600 hover:text-red-700 dark:text-red-400" onClick={onRevoke} disabled={busy}>Revoke</Button>}
      </footer>
    </article>
  );
}

// NodeSecurity (connector-trust 2.1): what agents may touch on this machine, as its person set it in node.yaml.
interface SecurityInfo { sandbox: string; locally_paused?: boolean; projects?: string[]; runtimes?: string[]; full_access?: string[]; own_claude_login: boolean; own_codex_login: boolean; keys_private: boolean }
function NodeSecurity({ sec }: { sec?: SecurityInfo }) {
  if (!sec) return null;
  const rows: [string, string, boolean][] = [
    ["Agents", sec.sandbox === "worktree" ? "write only in their task's worktree" : "act as the machine's user everywhere", sec.sandbox === "worktree"],
    ["Projects", sec.projects?.length ? sec.projects.join(", ") : "all of the company", !!sec.projects?.length],
    ["Logins", `Claude ${sec.own_claude_login ? "own" : "shared with the user"}, Codex ${sec.own_codex_login ? "own" : "shared with the user"}`, sec.own_claude_login && sec.own_codex_login],
    ["Node keys", sec.keys_private ? "private" : "readable by others", sec.keys_private],
  ];
  if (sec.full_access?.length) rows.push(["Full access", sec.full_access.join(", "), false]);
  if (sec.locally_paused) rows.push(["Local pause", "paused on the machine", false]);
  return (
    <details className="rounded-lg border px-3 py-2 text-xs">
      <summary className="cursor-pointer font-medium">Security on this machine</summary>
      <dl className="mt-2 space-y-1">
        {rows.map(([k, v, ok]) => (
          <div key={k} className="flex gap-2"><dt className="w-20 shrink-0 text-muted-foreground">{k}</dt><dd className={ok ? "" : "text-amber-700 dark:text-amber-300"}>{v}</dd></div>
        ))}
      </dl>
      <p className="mt-2 text-muted-foreground">Set on the machine in node.yaml; check with factory-node doctor -security.</p>
    </details>
  );
}

// AcceptAccount (node-runtime-accounts 3.3): continue with the runtime's new account; the node records it at its next
// check and runs that runtime's agents again.
function AcceptAccount({ nodeId, runtime }: { nodeId: string; runtime: string }) {
  const confirm = useConfirm();
  const [done, setDone] = useState(false);
  const accept = async () => {
    if (!(await confirm({ title: `Continue with the new ${runtime} account?`, description: "The company's work on this node will use the account the runtime is logged in to now. The node takes it at its next check (within about 10 minutes).", confirm: "Continue" }))) return;
    try {
      await core(`/nodes/${encodeURIComponent(nodeId)}/accounts/${runtime}/accept`, { method: "POST", body: "{}" });
      setDone(true);
      toast.success("The node takes the new account at its next check");
    } catch (e) { toast.error(e instanceof Error ? e.message : String(e)); }
  };
  return done ? <p className="mt-1 text-xs text-muted-foreground">Accepted; waiting for the node&apos;s next check.</p>
    : <Button variant="outline" size="sm" className="mt-1.5 h-8" onClick={accept}>Continue with the new account</Button>;
}

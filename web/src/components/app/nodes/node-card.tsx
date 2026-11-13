"use client";

import { useConfirm } from "@/components/app/confirm";
import { toast } from "sonner";
import { core } from "@/lib/api/client";
import { useState } from "react";

import { CircleAlert, HardDrive, Pause, Play, Cpu, Pin, FolderGit2, Database } from "lucide-react";
import Link from "@/components/app/link";
import { Button } from "@/components/ui/button";
import type { FactoryNode, Pause as PauseT } from "@/lib/api/types";
import { nodeHealth, nodeId, type NodeDriver, type NodeFull, type NodeHealth } from "@/lib/api/types-ops";
import { ago, displayId } from "@/lib/format";
import { NodeSettingsPanel } from "@/components/app/nodes/node-settings";
import { NodeUpdate, behind } from "@/components/app/nodes/node-update";
import { cn } from "@/lib/utils";

export const gib = (b?: number) => `${((b ?? 0) / 2 ** 30).toFixed(1)} GiB`;

// node-runtime-caches: what each runtime directory outside the worktrees is.
const cacheLabel: Record<string, string> = { claude_tmp: "Claude temp", claude_data: "Claude data", codex_data: "Codex data" };

// diskFull (disk-pause-conversations): a critical disk below the conversation floor takes no conversation either; an
// older node without a floor stops everything at critical.
const diskFull = (h: NodeHealth) => h.disk === "critical" && (h.disk_conversation_floor_bytes === undefined || (h.free_disk_bytes ?? 0) < h.disk_conversation_floor_bytes);

// care: what of a node needs the owner (internal/tui nodeCare), "" when nothing.
export function nodeCare(n: FactoryNode) {
  const h = nodeHealth(n);
  if (!n.online) return "offline";
  if (n.update_required) return "update required";
  if (h.disk === "critical") return "disk critical";
  const bad = ((n as NodeFull).drivers ?? []).find((d) => !d.ok);
  if (bad) return `${bad.name} disabled`;
  const unready = (n.readiness ?? []).filter((r) => !r.ready).length;
  if (unready) return `not ready for ${unready} project${unready === 1 ? "" : "s"}`;
  if (h.disk === "low") return "disk low";
  return "";
}

// Readiness (node-readiness): per project, ready or what keeps the node from its runs (no repository access, a
// missing tool, a run that failed here before its agent started) with the fix; Check again asks the node now.
function Readiness({ node, nodeId }: { node: FactoryNode; nodeId: string }) {
  const rs = node.readiness ?? [];
  const [asked, setAsked] = useState(false);
  if (rs.length === 0) return null;
  const bad = rs.filter((r) => !r.ready), ready = rs.filter((r) => r.ready);
  const again = async () => {
    try {
      await core(`/nodes/${encodeURIComponent(nodeId)}/check`, { method: "POST", body: "{}" });
      setAsked(true);
      toast.success("The node checks its projects again now");
    } catch (e) { toast.error(e instanceof Error ? e.message : String(e)); }
  };
  return (
    <div>
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs font-medium text-muted-foreground">Projects</p>
        {bad.length > 0 && <Button variant="outline" size="sm" className="h-8" onClick={again} disabled={asked}>{asked ? "Checking…" : "Check again"}</Button>}
      </div>
      {ready.length > 0 && <p className="mt-1 text-sm"><span className="text-emerald-700 dark:text-emerald-400">Ready</span> for {ready.map((r) => r.project).join(", ")}</p>}
      {bad.map((r) => (
        <div key={r.project} className="mt-1.5 rounded-lg bg-red-500/5 px-3 py-2 text-xs">
          <p className="font-medium text-red-700 dark:text-red-300">Not ready for {r.project}{r.pending ? " · checking" : ""}{r.source === "run" ? " · a run failed here" : ""}</p>
          {r.faults.map((f, i) => (
            <div key={i} className="mt-1">
              <p className="break-words text-red-700 dark:text-red-300">{f.detail}</p>
              {f.fix && <p className="mt-0.5 break-words text-muted-foreground">Fix: {f.fix}</p>}
            </div>
          ))}
        </div>
      ))}
    </div>
  );
}

// ReleaseSessions (node-readiness 4.1): a paused or offline node's held sessions keep their runs waiting until the
// release timeout; release them now so another node takes the runs with a fresh session.
function ReleaseSessions({ nodeId, count }: { nodeId: string; count: number }) {
  const confirm = useConfirm();
  const [done, setDone] = useState(false);
  const release = async () => {
    if (!(await confirm({ title: `Release ${count} waiting run${count === 1 ? "" : "s"}?`, description: "Another node takes them and starts a fresh session from Factory's context; the agents lose what only those sessions remembered.", confirm: "Release now" }))) return;
    try {
      await core(`/nodes/${encodeURIComponent(nodeId)}/sessions/release`, { method: "POST", body: "{}" });
      setDone(true);
      toast.success("Released: other nodes take the runs");
    } catch (e) { toast.error(e instanceof Error ? e.message : String(e)); }
  };
  return <Button variant="outline" size="sm" className="mt-1.5 h-8" onClick={release} disabled={done}>{done ? "Released" : "Release now"}</Button>;
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
function Tied({ node, inactive }: { node: FactoryNode; inactive?: boolean }) {
  const pinned = node.tied?.work ?? node.tied?.pinned ?? [], waiting = node.tied?.waiting ?? [];
  const sessionWaits = waiting.filter((w) => w.tie === "session").length;
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
      {waiting.length > 0 && !inactive && <p className="mt-1 text-[11px] text-muted-foreground">This node takes them first when a slot frees.</p>}
      {inactive && sessionWaits > 0 && <ReleaseSessions nodeId={nodeId(node)} count={sessionWaits} />}
    </div>
  );
}

export function NodeCard({ node, pause, onPause, onResume, onRevoke, busy, manage = false, release, policy }: {
  node: FactoryNode; pause?: PauseT; onPause: () => void; onResume: () => void; onRevoke?: () => void; busy?: boolean;
  manage?: boolean; release?: string; policy?: string;
}) {
  const n = node as NodeFull;
  const h = nodeHealth(node);
  const id = nodeId(node);
  const slots = n.slots ?? 0;
  // work-and-conversation-runs: conversation slots (node.yaml conversation_slots) take conversation runs only.
  const convSlots = n.conversation_slots ?? 0;
  const convUsed = n.in_use?.conversation_slots ?? 0;
  const used = (n.in_use?.runs ?? 0) - convUsed;
  const convRuns = n.in_use?.conversation_runs ?? 0;
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
            <span className="font-mono text-[11px] text-muted-foreground" title="Node version">{n.version || "version unknown"}</span>
            {!n.update_required && behind(n.version, release) && <span className="text-[11px] text-amber-700 dark:text-amber-300">{release} available</span>}
          </div>
          <p className={cn("mt-0.5 text-xs", state === "offline" ? "text-red-600 dark:text-red-400" : "text-muted-foreground")}>
            {state === "offline" ? `Silent since ${ago(n.last_seen)}` : `${state === "paused" ? (pause || diskFull(h) ? "Paused" : "Work paused") : "Online"} · checked in ${ago(n.last_seen)}`}
          </p>
        </div>
        {care && (
          <span className={cn("shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium",
            care === "offline" || care.includes("critical") || care.includes("disabled") || care.includes("update") ? "bg-red-500/15 text-red-700 dark:text-red-300" : "bg-amber-500/15 text-amber-700 dark:text-amber-300")}>
            {care}
          </span>
        )}
      </header>

      <div className="space-y-4 px-4 pb-4 md:px-5">
        <NodeUpdate node={node} release={release} policy={policy} manage={manage} />

        {/* Slots: one cell per slot, filled when a run holds it. */}
        <div>
          <div className="mb-1.5 flex items-baseline justify-between text-xs">
            <span className="font-medium text-muted-foreground">Work slots</span>
            <span className="tabular-nums"><span className="font-semibold">{used}</span><span className="text-muted-foreground"> of {slots} busy{convRuns - convUsed > 0 ? ` (${convRuns - convUsed} by conversations)` : ""}</span></span>
          </div>
          <div className="flex gap-1" aria-hidden>
            {Array.from({ length: Math.max(slots, 1) }, (_, i) => (
              <span key={i} className={cn("h-2 flex-1 rounded-full", i < used ? "bg-blue-500" : "bg-muted")} />
            ))}
          </div>
          <div className="mb-1.5 mt-2.5 flex items-baseline justify-between text-xs">
            <span className="font-medium text-muted-foreground">Conversation slots</span>
            <span className="tabular-nums"><span className="font-semibold">{convUsed}</span><span className="text-muted-foreground"> of {convSlots} busy</span></span>
          </div>
          {convSlots > 0 ? (
            <div className="flex gap-1" aria-hidden>
              {Array.from({ length: convSlots }, (_, i) => (
                <span key={i} className={cn("h-2 flex-1 rounded-full", i < convUsed ? "bg-violet-500" : "bg-muted")} />
              ))}
            </div>
          ) : null}
          <p className="mt-1 text-[11px] text-muted-foreground">
            {convSlots > 0 ? "Conversations answer here even when the work slots are full. " : "No conversation slots: conversations share the work slots. "}
            Change them under Settings below; the node applies them without a restart.
          </p>
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

        <Tied node={n} inactive={!!pause || !n.online} />

        <Readiness node={n} nodeId={id} />

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
          {n.worktree_summary && (
            <div className="min-w-0">
              <p className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground"><FolderGit2 className="size-3.5" />Worktrees</p>
              <p className="mt-0.5 tabular-nums">{n.worktree_summary.count} <span className="text-muted-foreground">· {gib(n.worktree_summary.bytes)}</span></p>
              {n.worktree_summary.removing > 0 && (
                <p className={cn("truncate text-[11px]", n.worktree_summary.stuck > 0 ? "font-medium text-amber-700 dark:text-amber-300" : "text-muted-foreground")}>
                  {n.worktree_summary.removing} being removed{n.worktree_summary.removing_since ? ` · oldest asked ${ago(n.worktree_summary.removing_since)}` : ""}
                </p>
              )}
            </div>
          )}
          {!!h.cpus && (
            <div className="min-w-0">
              <p className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground"><Cpu className="size-3.5" />Load</p>
              <p className="mt-0.5 tabular-nums">{(h.load1 ?? 0).toFixed(1)} <span className="text-muted-foreground">on {h.cpus} CPUs</span></p>
            </div>
          )}
          {!!h.caches?.length && (
            <div className="min-w-0">
              <p className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground"><Database className="size-3.5" />Runtime caches</p>
              <p className="mt-0.5 tabular-nums">{gib(h.caches.reduce((s, c) => s + c.bytes, 0))}</p>
              {h.caches.map((c) => (
                <p key={c.name} className="truncate text-[11px] tabular-nums text-muted-foreground" title={c.path}>
                  {cacheLabel[c.name] ?? c.name} {gib(c.bytes)}{c.agent_bytes !== undefined && c.agent_bytes !== c.bytes ? ` · agents' sessions ${gib(c.agent_bytes)}` : ""}
                </p>
              ))}
            </div>
          )}
        </div>

        {failed.map((c) => (
          <p key={c.check} className="flex items-start gap-2 rounded-lg bg-red-500/5 px-3 py-2 text-xs text-red-700 dark:text-red-300">
            <CircleAlert className="mt-px size-3.5 shrink-0" /><span><span className="font-medium">{c.check}</span> check failed{c.detail ? `: ${c.detail}` : ""}</span>
          </p>
        ))}

        <NodeSecurity sec={(n.health as { security?: SecurityInfo } | undefined)?.security} />

        {/* Labels select which agents' runs the node takes: the owner's (filled) and the ones the node detected (outlined). */}
        {((n.labels ?? []).length > 0 || (n.detected_labels ?? []).length > 0) && (
          <div className="flex flex-wrap gap-1.5">
            {(n.labels ?? []).map((l) => <span key={l} className="rounded-md bg-muted px-2 py-0.5 text-xs text-muted-foreground" title="Set by the owner">{l}</span>)}
            {(n.detected_labels ?? []).filter((l) => !(n.labels ?? []).includes(l)).map((l) => (
              <span key={`d-${l}`} className="rounded-md border border-dashed px-2 py-0.5 text-xs text-muted-foreground" title="Detected by the node">{l}</span>
            ))}
          </div>
        )}

        <NodeSettingsPanel key={`${id}-${n.config?.version ?? 0}`} nodeId={id} config={n.config} manage={manage} />
      </div>

      <footer className="mt-auto flex items-center gap-3 border-t px-4 py-3 md:px-5">
        <p className="min-w-0 flex-1 truncate text-xs text-muted-foreground">
          {pause ? <>Paused by {pause.actor}{pause.reason ? `: ${pause.reason}` : ""}</>
            : !n.online ? "Offline: takes no work"
            : n.update_required ? `Takes no work: Core needs version ${n.min_version} or newer; reinstall with the install command`
            : h.disk === "critical"
              ? (diskFull(h) ? "Takes nothing: the disk is full; free disk on this machine"
                : `Answers conversations only; free disk, work resumes by itself above ${gib(h.disk_critical_bytes)}`)
              : "Takes new work"}
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

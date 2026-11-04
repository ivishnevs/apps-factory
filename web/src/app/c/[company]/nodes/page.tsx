"use client";

import { Bot, FolderKanban, KeyRound, Pause, Play, Server } from "lucide-react";
import { useMemo, useState } from "react";
import { EmptyState, ErrorState, LoadingRows, PageHeader } from "@/components/app/page";
import { KillSwitch } from "@/components/app/nodes/kill-switch";
import { NodeCard } from "@/components/app/nodes/node-card";
import { useOwnerAction } from "@/components/app/nodes/owner-action";
import { PauseDialog, type PauseRequest } from "@/components/app/nodes/pause-dialog";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useOverview } from "@/lib/api/client";
import type { Pause as PauseT } from "@/lib/api/types";
import { companyPause, nodeId, scopeName } from "@/lib/api/types-ops";
import { ago } from "@/lib/format";

// Nodes & control: the kill switch, the active pauses with resume, pausing a project, agent, node or quota
// key (hold/release), and each registered node with its slots, drivers and disk (internal/tui/nodes.go).
const scopeIcon: Record<string, React.ElementType> = { project: FolderKanban, agent: Bot, node: Server, quota_key: KeyRound };

function PauseRow({ p, label, onResume, busy }: { p: PauseT; label: string; onResume: () => void; busy: boolean }) {
  const Icon = scopeIcon[p.scope] ?? Pause;
  return (
    <li className="flex items-center gap-3 px-4 py-3 md:px-5">
      <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-amber-500/15 text-amber-700 dark:text-amber-300"><Icon className="size-4" /></span>
      <div className="min-w-0 flex-1">
        <p className="truncate text-sm"><span className="text-muted-foreground">{scopeName(p.scope)}</span> <span className="font-medium">{label}</span></p>
        {p.reason && <p className="truncate text-sm text-muted-foreground">{p.reason}</p>}
        <p className="mt-0.5 text-xs text-muted-foreground">
          {p.actor} · {ago(p.created_at)}
          {p.until ? ` · until ${new Date(p.until * 1000).toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" })}` : ""}
        </p>
      </div>
      <Button variant="outline" className="h-10 shrink-0" onClick={onResume} disabled={busy}><Play className="size-4" />Resume</Button>
    </li>
  );
}

export default function Page() {
  const { data: ov, isLoading, error } = useOverview();
  const { run, pending } = useOwnerAction();
  const [dialog, setDialog] = useState<{ open: boolean; initial?: Partial<PauseRequest> }>({ open: false });

  const stopped = companyPause(ov?.pauses);
  const pauses = (ov?.pauses ?? []).filter((p) => p.scope !== "company");
  const nodes = ov?.nodes ?? [];
  const nodeName = (id?: string) => nodes.find((n) => nodeId(n) === id)?.name || id || "";
  const label = (p: PauseT) => (p.scope === "node" ? nodeName(p.target) : p.target ?? "");

  const options = useMemo(() => ({
    project: (ov?.status ?? []).map((s) => ({ value: s.project.id, label: s.project.name || s.project.id })),
    agent: (ov?.agents ?? []).map((a) => ({ value: a.name, label: a.role ? `${a.name} · ${a.role}` : a.name })),
    node: (ov?.nodes ?? []).map((n) => ({ value: nodeId(n), label: n.name || nodeId(n) })),
  }), [ov]);

  const hold = (r: PauseRequest) => {
    setDialog({ open: false });
    const what = `${scopeName(r.scope)} ${r.scope === "node" ? nodeName(r.target) : r.target}`;
    run({ Kind: "hold", Scope: r.scope, Target: r.target, Text: r.reason },
      { title: `Pause ${what}?`, description: `New runs stop; live runs continue.${r.reason ? `\n\nReason: ${r.reason}` : ""}`, confirm: "Pause" }, `Paused ${what}`);
  };
  const release = (p: PauseT) => {
    const what = `${scopeName(p.scope)} ${label(p)}`;
    run({ Kind: "release", Scope: p.scope, Target: p.target },
      { title: `Resume ${what}?`, description: p.reason ? `Paused by ${p.actor}: ${p.reason}` : `Paused by ${p.actor}.`, confirm: "Resume" }, `Resumed ${what}`);
  };

  return (
    <>
      <PageHeader title="Nodes & control" description="The machines your agents run on, what is paused, and the kill switch" />
      {error && <div className="mb-4"><ErrorState error={error} /></div>}
      {isLoading ? (
        <div className="space-y-4">
          <Skeleton className="h-24 rounded-2xl" /><LoadingRows rows={2} />
          <div className="grid gap-4 md:grid-cols-2"><Skeleton className="h-80 rounded-2xl" /><Skeleton className="h-80 rounded-2xl" /></div>
        </div>
      ) : ov && (
        <div className="space-y-8">
          <KillSwitch stopped={stopped} />

          <section aria-labelledby="pauses-title">
            <div className="mb-3 flex items-center justify-between gap-3">
              <div>
                <h2 id="pauses-title" className="text-base font-semibold">Pauses <span className="ml-1 text-sm font-normal tabular-nums text-muted-foreground">{pauses.length}</span></h2>
                <p className="text-sm text-muted-foreground">New runs wait; live runs continue.</p>
              </div>
              <Button variant="outline" className="h-10" onClick={() => setDialog({ open: true })}><Pause className="size-4" />Pause…</Button>
            </div>
            {ov.errors?.pauses && <div className="mb-3"><ErrorState error={ov.errors.pauses} /></div>}
            {pauses.length === 0 ? (
              <EmptyState icon={Play} title="Nothing is paused" hint="Pause a project, an agent, a node or a quota key to hold its new runs." className="py-8" />
            ) : (
              <ul className="divide-y overflow-hidden rounded-2xl border bg-card shadow-xs">
                {pauses.map((p) => <PauseRow key={`${p.scope}/${p.target}`} p={p} label={label(p)} busy={pending} onResume={() => release(p)} />)}
              </ul>
            )}
          </section>

          <section aria-labelledby="nodes-title">
            <div className="mb-3">
              <h2 id="nodes-title" className="text-base font-semibold">Nodes <span className="ml-1 text-sm font-normal tabular-nums text-muted-foreground">{nodes.length}</span></h2>
              <p className="text-sm text-muted-foreground">
                {nodes.filter((n) => n.online).length} online · {nodes.reduce((s, n) => s + (n.in_use?.runs ?? 0), 0)} of {nodes.reduce((s, n) => s + (n.slots ?? 0), 0)} slots in use
              </p>
            </div>
            {ov.errors?.nodes && <div className="mb-3"><ErrorState error={ov.errors.nodes} /></div>}
            {nodes.length === 0 ? (
              <EmptyState icon={Server} title="No registered nodes" hint="Start a node (factory node) on a machine to run agents on it." />
            ) : (
              <div className="grid items-start gap-4 md:grid-cols-2 2xl:grid-cols-3">
                {nodes.map((n) => {
                  const id = nodeId(n);
                  const pause = pauses.find((p) => p.scope === "node" && p.target === id);
                  return (
                    <NodeCard key={id} node={n} pause={pause} busy={pending}
                      onPause={() => setDialog({ open: true, initial: { scope: "node", target: id } })}
                      onResume={() => pause && release(pause)} />
                  );
                })}
              </div>
            )}
          </section>
        </div>
      )}
      <PauseDialog open={dialog.open} onOpenChange={(o) => setDialog((d) => ({ ...d, open: o }))} initial={dialog.initial} options={options} onSubmit={hold} />
    </>
  );
}

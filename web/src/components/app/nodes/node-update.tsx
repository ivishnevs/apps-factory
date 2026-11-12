"use client";

import { useState } from "react";
import { toast } from "sonner";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowUpCircle } from "lucide-react";
import { useConfirm } from "@/components/app/confirm";
import { Button } from "@/components/ui/button";
import { core } from "@/lib/api/client";
import type { FactoryNode, NodeUpdateSettings } from "@/lib/api/types";
import { cn } from "@/lib/utils";

// Node updates (node-packaging-updates 3.4): a node behind the release Core hands out downloads it, checks it, runs
// the new version's self-test, waits for its live runs (it takes no new work meanwhile), switches and checks its
// health, rolling back by itself when the new version does not come up. The company chooses automatic updates (the
// default) or approval per node; "Update now" stops waiting for live runs (they are interrupted and queued again).

// behind: version is older than release. Versions are dotted numbers (2026.11.12.1530+commit); anything else ("dev")
// is not compared.
export function behind(version?: string, release?: string) {
  const parts = (v?: string) => (v && /^v?\d+(\.\d+)*([+-].*)?$/.test(v) ? v.replace(/^v/, "").split(/[+-]/)[0].split(".").map(Number) : null);
  const a = parts(version), b = parts(release);
  if (!a || !b) return false;
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    if ((a[i] ?? 0) !== (b[i] ?? 0)) return (a[i] ?? 0) < (b[i] ?? 0);
  }
  return false;
}

export function useNodeUpdates() {
  return useQuery({ queryKey: ["node-updates"], queryFn: () => core<NodeUpdateSettings>("/settings/node-updates"), staleTime: 60_000 });
}

const stateText: Record<string, string> = {
  downloading: "downloading and checking the package",
  testing: "running the new version's self-test",
  switching: "switching and restarting",
};

export function NodeUpdate({ node, release, policy, manage }: { node: FactoryNode; release?: string; policy?: string; manage: boolean }) {
  const confirm = useConfirm();
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const id = node.node_id;
  const u = node.update;
  const due = behind(node.version, release);
  if (!u && !due) return null;
  const act = async (now: boolean, title: string, description: string, ok: string) => {
    if (!(await confirm({ title, description, confirm: now ? "Update now" : "Update" }))) return;
    setBusy(true);
    try {
      await core(`/nodes/${encodeURIComponent(id)}/update`, { method: "POST", body: JSON.stringify({ now }) });
      toast.success(ok);
      await qc.invalidateQueries();
    } catch (e) { toast.error(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(false); }
  };
  const updateNow = () => act(true, `Update ${node.name || id} now?`,
    "Its live runs are interrupted and queued again (their work in progress is saved); the node switches to the new version as soon as they stop.",
    "The node updates now");
  const approve = (retry: boolean) => act(false, `${retry ? "Retry updating" : "Update"} ${node.name || id} to ${release}?`,
    "The node takes no new work, waits for its live runs to end, then switches and checks the new version; it rolls back by itself if the new version does not come up.",
    "Approved: the node updates after its live runs");

  let text: string, tone: "info" | "warn" | "bad" = "info";
  let action: React.ReactNode = null;
  if (u && u.state !== "failed") {
    text = u.state === "draining"
      ? `Updating to ${u.version}: waiting for ${u.runs ?? 0} live run${u.runs === 1 ? "" : "s"} to end; takes no new work`
      : `Updating to ${u.version}: ${stateText[u.state] ?? u.state}`;
    if (manage && u.state === "draining" && node.update_now !== u.version) {
      action = <Button variant="outline" size="sm" className="h-8" onClick={updateNow} disabled={busy}>Update now</Button>;
    }
  } else if (u?.state === "failed" && (!release || u.version === release)) {
    tone = "bad";
    text = `Update to ${u.version} failed: ${u.reason ?? "no reason given"}. It keeps running ${u.from || node.version || "its version"}.`;
    if (manage && due) action = <Button variant="outline" size="sm" className="h-8" onClick={() => approve(true)} disabled={busy}>Retry update</Button>;
  } else if (!node.platform) {
    tone = "warn";
    text = `${release} is available. This node was installed before updates from the web: reinstall it once with the install command.`;
  } else if (policy === "approve" && node.update_approved !== release) {
    tone = "warn";
    text = `${release} is available: updates wait for approval.`;
    if (manage) action = <Button variant="outline" size="sm" className="h-8" onClick={() => approve(false)} disabled={busy}>Update to {release}</Button>;
  } else {
    text = `Updates to ${release} after its live runs.`;
    if (manage && node.update_now !== release) action = <Button variant="outline" size="sm" className="h-8" onClick={updateNow} disabled={busy}>Update now</Button>;
  }
  return (
    <div className={cn("flex flex-wrap items-center gap-2 rounded-lg px-3 py-2 text-xs",
      tone === "info" && "bg-blue-500/5 text-blue-800 dark:text-blue-300", tone === "warn" && "bg-amber-500/5 text-amber-800 dark:text-amber-300",
      tone === "bad" && "bg-red-500/5 text-red-700 dark:text-red-300")}>
      <ArrowUpCircle className="size-3.5 shrink-0" />
      <span className="min-w-0 flex-1 break-words">{text}</span>
      {action}
    </div>
  );
}

// NodeUpdatePolicy: the company's choice between automatic updates (default) and approval per node.
export function NodeUpdatePolicy({ manage }: { manage: boolean }) {
  const { data } = useNodeUpdates();
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  if (!data) return null;
  const set = async (policy: string) => {
    setBusy(true);
    try {
      await core("/settings/node-updates", { method: "PUT", body: JSON.stringify({ policy }) });
      toast.success(policy === "auto" ? "Nodes update automatically" : "Node updates wait for approval");
      await qc.invalidateQueries();
    } catch (e) { toast.error(e instanceof Error ? e.message : String(e)); }
    finally { setBusy(false); }
  };
  return (
    <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
      <span>Node updates{data.release ? ` (release ${data.release})` : ""}:</span>
      {manage ? (
        <select aria-label="Node updates" className="h-9 rounded-md border bg-background px-2 text-sm text-foreground" value={data.policy} disabled={busy} onChange={(e) => set(e.target.value)}>
          <option value="auto">Automatic</option>
          <option value="approve">With approval</option>
        </select>
      ) : <span className="text-foreground">{data.policy === "auto" ? "automatic" : "with approval"}</span>}
    </div>
  );
}

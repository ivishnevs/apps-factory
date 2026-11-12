"use client";

import { useState } from "react";
import { toast } from "sonner";
import { useQueryClient } from "@tanstack/react-query";
import { Settings2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { core } from "@/lib/api/client";
import type { NodeConfigView, NodeSettingState, NodeSettings } from "@/lib/api/types";
import { cn } from "@/lib/utils";

// Settings from the web (node-packaging-updates B): slots, conversation slots, labels, each driver on or off and the
// disk watermarks. Core keeps them per node; the node applies them live over its node.yaml unless node.yaml pins a
// setting (remote: …) or caps it (slots_max, policy.runtimes). Each setting shows applied, pending or refused, and
// node.yaml's own value next to it. Sandbox, policy, paths and logins stay in node.yaml.

const drivers = ["codex", "claude"] as const;

// Form: "" keeps node.yaml's value; drivers "" | "on" | "off".
interface Form { slots: string; conversation_slots: string; labels: string; codex: string; claude: string; low: string; critical: string; floor: string }

const num = (v?: number) => (v === undefined || v === null ? "" : String(v));

function formOf(d: NodeSettings): Form {
  const on = (name: string) => (d.drivers?.[name]?.enabled === undefined ? "" : d.drivers[name].enabled ? "on" : "off");
  return {
    slots: num(d.slots), conversation_slots: num(d.conversation_slots), labels: d.labels ? d.labels.join(", ") : "",
    codex: on("codex"), claude: on("claude"),
    low: num(d.disk?.low_watermark_gib), critical: num(d.disk?.critical_watermark_gib), floor: num(d.disk?.conversation_floor_gib),
  };
}

function settingsOf(f: Form): NodeSettings {
  const s: NodeSettings = {};
  const int = (v: string) => (v.trim() === "" ? undefined : Number(v));
  if (int(f.slots) !== undefined) s.slots = int(f.slots);
  if (int(f.conversation_slots) !== undefined) s.conversation_slots = int(f.conversation_slots);
  if (f.labels.trim() !== "") s.labels = f.labels.split(/[,\s]+/).map((l) => l.trim()).filter(Boolean);
  for (const d of drivers) if (f[d]) (s.drivers ??= {})[d] = { enabled: f[d] === "on" };
  const disk: NonNullable<NodeSettings["disk"]> = {};
  if (int(f.low) !== undefined) disk.low_watermark_gib = int(f.low);
  if (int(f.critical) !== undefined) disk.critical_watermark_gib = int(f.critical);
  if (int(f.floor) !== undefined) disk.conversation_floor_gib = int(f.floor);
  if (Object.keys(disk).length) s.disk = disk;
  return s;
}

function State({ st }: { st?: NodeSettingState }) {
  if (!st) return null;
  return (
    <span title={st.reason} className={cn("min-w-0 truncate text-[11px]",
      st.state === "applied" && "text-emerald-700 dark:text-emerald-400", st.state === "pending" && "text-amber-700 dark:text-amber-300",
      st.state === "refused" && "font-medium text-red-600 dark:text-red-400")}>
      {st.state}{st.reason ? `: ${st.reason}` : ""}
    </span>
  );
}

function Row({ label, local, st, children }: { label: string; local: string; st?: NodeSettingState; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[7.5rem_1fr] items-start gap-x-2 gap-y-0.5 py-1.5">
      <span className="pt-2 text-muted-foreground">{label}</span>
      <div className="min-w-0">
        {children}
        <div className="mt-0.5 flex flex-wrap gap-x-2 text-[11px] text-muted-foreground"><span>node.yaml: {local || "—"}</span><State st={st} /></div>
      </div>
    </div>
  );
}

export function NodeSettingsPanel({ nodeId, config, manage }: { nodeId: string; config?: NodeConfigView; manage: boolean }) {
  const qc = useQueryClient();
  const desired = config?.desired ?? {};
  const [form, setForm] = useState<Form>(() => formOf(desired));
  const [saving, setSaving] = useState(false);
  const local = config?.report?.local;
  const states = config?.states ?? {};
  const pinned = config?.report?.pinned ?? [];
  const set = (k: keyof Form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setForm((f) => ({ ...f, [k]: e.target.value }));
  const save = async (settings: NodeSettings) => {
    setSaving(true);
    try {
      await core(`/nodes/${encodeURIComponent(nodeId)}/config`, { method: "PUT", body: JSON.stringify({ config: settings, base_version: config?.version ?? 0 }) });
      toast.success("Saved: the node applies it within seconds");
      await qc.invalidateQueries();
    } catch (e) { toast.error(e instanceof Error ? e.message : String(e)); }
    finally { setSaving(false); }
  };
  const hasSettings = Object.keys(desired).length > 0;
  const refused = Object.values(states).filter((s) => s.state === "refused").length;
  const pending = Object.values(states).filter((s) => s.state === "pending").length;
  const summary = !hasSettings ? "all from node.yaml" : refused ? `${refused} refused` : pending ? `${pending} pending` : "applied";
  const field = "h-9 w-full rounded-md border bg-background px-2 text-sm";
  const yamlDriver = (d: string) => (local?.drivers?.[d]?.enabled === undefined ? "" : local.drivers[d].enabled ? "on" : "off");
  return (
    <details className="rounded-lg border px-3 py-2 text-xs">
      <summary className="flex cursor-pointer items-center gap-1.5 font-medium">
        <Settings2 className="size-3.5" />Settings
        <span className={cn("font-normal", refused ? "text-red-600 dark:text-red-400" : pending ? "text-amber-700 dark:text-amber-300" : "text-muted-foreground")}>· {summary}{config?.version ? ` · v${config.version}` : ""}</span>
      </summary>
      {!config?.report && <p className="mt-2 text-amber-700 dark:text-amber-300">This node does not take settings from the web yet: update it, then they apply here.</p>}
      <form className="mt-1 divide-y divide-border/60" onSubmit={(e) => { e.preventDefault(); void save(settingsOf(form)); }}>
        <Row label="Work slots" local={num(local?.slots) + (config?.report?.slots_max ? ` (cap ${config.report.slots_max})` : "")} st={states.slots}>
          <Input type="number" min={1} max={64} placeholder="from node.yaml" value={form.slots} onChange={set("slots")} disabled={!manage} className="h-9" />
        </Row>
        <Row label="Conversation slots" local={num(local?.conversation_slots)} st={states.conversation_slots}>
          <Input type="number" min={0} max={64} placeholder="from node.yaml" value={form.conversation_slots} onChange={set("conversation_slots")} disabled={!manage} className="h-9" />
        </Row>
        <Row label="Labels" local={(local?.labels ?? []).join(", ")} st={states.labels}>
          <Input placeholder="from node.yaml, e.g. ios, team-a" value={form.labels} onChange={set("labels")} disabled={!manage} className="h-9" />
        </Row>
        {drivers.map((d) => (
          <Row key={d} label={d === "codex" ? "Codex" : "Claude"} local={yamlDriver(d)} st={states[`drivers.${d}`]}>
            <select className={field} value={form[d]} onChange={set(d)} disabled={!manage} aria-label={`${d} driver`}>
              <option value="">from node.yaml</option><option value="on">on</option><option value="off">off</option>
            </select>
          </Row>
        ))}
        <Row label="Disk (GiB)" local={local?.disk ? `low ${local.disk.low_watermark_gib} · critical ${local.disk.critical_watermark_gib} · floor ${local.disk.conversation_floor_gib}` : ""} st={states.disk}>
          <div className="grid grid-cols-3 gap-1.5">
            <Input type="number" min={0} step="any" placeholder="low" aria-label="low watermark" value={form.low} onChange={set("low")} disabled={!manage} className="h-9" />
            <Input type="number" min={0} step="any" placeholder="critical" aria-label="critical watermark" value={form.critical} onChange={set("critical")} disabled={!manage} className="h-9" />
            <Input type="number" min={0} step="any" placeholder="floor" aria-label="conversation floor" value={form.floor} onChange={set("floor")} disabled={!manage} className="h-9" />
          </div>
        </Row>
        {pinned.length > 0 && <p className="py-1.5 text-muted-foreground">node.yaml keeps {pinned.join(", ")} local.</p>}
        {manage && (
          <div className="flex flex-wrap gap-2 pt-2">
            <Button type="submit" size="sm" className="h-9" disabled={saving}>Save</Button>
            {hasSettings && <Button type="button" variant="outline" size="sm" className="h-9" disabled={saving}
              onClick={() => { setForm(formOf({})); void save({}); }}>Use node.yaml</Button>}
          </div>
        )}
        <p className="pt-2 text-muted-foreground">Empty keeps the machine&apos;s node.yaml value. Sandbox, policy, paths and logins are set on the machine only.</p>
      </form>
    </details>
  );
}

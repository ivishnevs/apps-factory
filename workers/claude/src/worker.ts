// Run lifecycle mirroring internal/worker: fetch → claim → ack → lease-owned execution.
import type { Consumer } from "@nats-io/jetstream";
import type { Bus } from "./bus.js";
import { CoreAPI, HTTPError } from "./core.js";
import { COMMAND_SUBJECT, DURABLE, PROVIDER, parseCommand, type EventKind, type RunEvent, type RunSpec } from "./protocol.js";
import { RuntimeError, runClaude, type QueryFn } from "./runtime.js";
import { acquire, WorkspaceError } from "./workspace.js";
import type { SettingSource } from "@anthropic-ai/claude-agent-sdk";

export interface WorkerConfig {
  bus: Pick<Bus, "commands" | "event">;
  api: CoreAPI;
  workspaceHelper: string;
  mcpCommand: string;
  executable?: string;
  allowApiKey?: boolean;
  settingSources?: SettingSource[];
  query?: QueryFn;
  heartbeatMs?: number;
  log: (msg: string, fields?: Record<string, unknown>) => void;
}

const SCRUBBED = ["FACTORY_TOKEN", "FACTORY_RUN_TOKEN", "FACTORY_URL", "NATS_URL", "NATS_PASSWORD"];

// The runtime receives only a run-scoped Factory credential. Host access remains trusted.
export function runEnvironment(env: NodeJS.ProcessEnv, base: string, token: string, allowApiKey = false): Record<string, string | undefined> {
  const out: Record<string, string | undefined> = {};
  for (const [k, v] of Object.entries(env)) {
    if (SCRUBBED.includes(k) || (!allowApiKey && (k === "ANTHROPIC_API_KEY" || k === "ANTHROPIC_AUTH_TOKEN"))) continue;
    out[k] = v;
  }
  return {
    ...out,
    FACTORY_URL: base,
    FACTORY_RUN_TOKEN: token,
    CLAUDE_AGENT_SDK_CLIENT_APP: "appfactory-claude-worker/0.1.0",
    // A turn is the unit of work: background shells would outlive it and die with the runtime.
    CLAUDE_CODE_DISABLE_BACKGROUND_TASKS: "1",
  };
}

export class Worker {
  constructor(private readonly c: WorkerConfig) {}

  async run(signal: AbortSignal): Promise<void> {
    const consumer: Consumer = await this.c.bus.commands(COMMAND_SUBJECT, DURABLE);
    while (!signal.aborted) {
      const m = await consumer.next({ expires: 1_000 });
      if (!m) continue;
      const e = parseCommand(m.data);
      if (!e) {
        m.term();
        this.c.log("invalid run command");
        continue;
      }
      // Fetch authoritative input from Core, then atomically claim before runtime startup.
      let spec: RunSpec;
      let lease: string;
      try {
        spec = (await this.c.api.getRun(e.payload.id)).spec;
        lease = (await this.c.api.claim(spec.id)).lease;
      } catch (err) {
        if (err instanceof HTTPError && err.status === 409) {
          const saved = await this.c.api.getRun(e.payload.id).catch(() => undefined);
          if (saved && saved.status !== "QUEUED") {
            await m.ackAck();
            continue;
          }
        }
        m.nak(1_000);
        continue;
      }
      await m.ackAck(); // Core's lease now owns recovery, not redelivery.
      this.c.log("run claimed", { run: spec.id });
      await this.execute(spec, lease, e.event_id, signal);
    }
  }

  async execute(spec: RunSpec, lease: string, cause: string, parent: AbortSignal): Promise<void> {
    const emit = (kind: EventKind, p: Omit<RunEvent, "run_id" | "lease">) =>
      this.c.bus.event(kind, cause, { ...p, run_id: spec.id, lease });
    const stop = new AbortController();
    const onParent = () => stop.abort();
    parent.addEventListener("abort", onParent, { once: true });
    let heartbeat: NodeJS.Timeout | undefined;
    let timeout: NodeJS.Timeout | undefined;
    let allocation: Awaited<ReturnType<typeof acquire>> | undefined;
    try {
      if (spec.provider !== PROVIDER || !(spec.timeout_seconds >= 1)) throw new RuntimeError("invalid RunSpec");
      try {
        allocation = await acquire(this.c.workspaceHelper, spec.workspace, spec.openspec_change || undefined);
      } catch (err) {
        if (err instanceof WorkspaceError && err.kind === "openspec") {
          await emit("failed", { status: "FAILED", result: `OpenSpec preflight failed before runtime startup: ${err.message}` });
          return;
        }
        throw err;
      }
      void allocation.lost.then(() => stop.abort(new RuntimeError("workspace lock lost")));
      timeout = setTimeout(() => stop.abort(), spec.timeout_seconds * 1000);
      // Heartbeats also carry cancellation. Failure stops execution instead of running without ownership.
      heartbeat = setInterval(() => {
        this.c.api.heartbeat(spec.id, lease).then(
          (res) => res.cancel && stop.abort(),
          (err) => {
            this.c.log("heartbeat failed", { run: spec.id, error: String(err) });
            stop.abort();
          },
        );
      }, this.c.heartbeatMs ?? 10_000);
      const credential = await this.c.api.credential(spec.id, lease);
      if (!credential.token || !credential.instructions) throw new RuntimeError("missing run credential");
      await runClaude({
        spec,
        cwd: allocation.path,
        instructions: `${spec.instructions}\n${credential.instructions}`,
        env: runEnvironment(process.env, this.c.api.url, credential.token, this.c.allowApiKey),
        mcpCommand: this.c.mcpCommand,
        signal: stop.signal,
        emit,
        executable: this.c.executable,
        allowApiKey: this.c.allowApiKey,
        settingSources: this.c.settingSources,
        query: this.c.query,
        log: (line) => this.c.log("claude stderr", { run: spec.id, line: line.trimEnd() }),
      });
    } catch (err) {
      // Errors after claiming are conservatively unknown; only a result message is terminal evidence.
      this.c.log("runtime run", { run: spec.id, error: String(err) });
      await emit("failed", { status: "UNKNOWN", result: err instanceof Error ? err.message : String(err) }).catch((e) =>
        this.c.log("terminal event publication failed; lease recovery required", { error: String(e) }),
      );
    } finally {
      clearInterval(heartbeat);
      clearTimeout(timeout);
      parent.removeEventListener("abort", onParent);
      await allocation?.release();
    }
  }
}

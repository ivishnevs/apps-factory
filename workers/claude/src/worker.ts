// Run lifecycle mirroring internal/worker: fetch → claim → ack → lease-owned execution.
import type { Consumer } from "@nats-io/jetstream";
import type { Bus } from "./bus.js";
import { CoreAPI, HTTPError } from "./core.js";
import {
  COMMAND_SUBJECT,
  DURABLE,
  PROVIDER,
  parseCommand,
  type EventKind,
  type Outcome,
  type RunEvent,
  type RunSpec,
  type TerminalStatus,
} from "./protocol.js";
import { RuntimeError, StopReason, errorEnd, runClaude, stopOutcome, type QueryFn } from "./runtime.js";
import { acquire, WorkspaceError } from "./workspace.js";
import { clock, type Timer } from "./clock.js";
import { runEnvironment } from "./env.js";
import type { SettingSource } from "@anthropic-ai/claude-agent-sdk";

export { runEnvironment };

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
    const onParent = () => stop.abort(new StopReason("worker stopping"));
    parent.addEventListener("abort", onParent, { once: true });
    // Both run on the sleep-excluding clock: system sleep neither expires the run nor skips beats.
    let heartbeat: Timer | undefined;
    let timeout: Timer | undefined;
    let allocation: Awaited<ReturnType<typeof acquire>> | undefined;
    // Until runClaude starts, a failure is a setup failure: the runtime never saw the prompt.
    let setup = true;
    try {
      if (spec.provider !== PROVIDER || !(spec.timeout_seconds >= 1)) throw new RuntimeError("invalid RunSpec");
      try {
        allocation = await acquire(this.c.workspaceHelper, spec.workspace, spec.openspec_change || undefined);
      } catch (err) {
        if (err instanceof WorkspaceError && err.kind === "openspec") {
          await emit("failed", { status: "FAILED", result: `OpenSpec preflight failed before runtime startup: ${err.message}`, outcome: "setup_failed" });
          return;
        }
        throw err;
      }
      void allocation.lost.then(() => stop.abort(new StopReason("workspace lock lost", "failed")));
      timeout = clock.timeout(spec.timeout_seconds * 1000, () => stop.abort(new StopReason("run timeout", "timed_out")));
      // Heartbeats also carry cancellation (an owner cancel, or Core's idle stop, which Core records
      // idle_timeout itself). Failure stops execution instead of running without ownership.
      heartbeat = clock.interval(this.c.heartbeatMs ?? 10_000, () => {
        this.c.api.heartbeat(spec.id, lease).then(
          (res) => res.cancel && stop.abort(new StopReason("cancelled by Core")),
          (err) => {
            this.c.log("heartbeat failed", { run: spec.id, error: String(err) });
            stop.abort(new StopReason("heartbeat failed"));
          },
        );
      });
      const credential = await this.c.api.credential(spec.id, lease);
      if (!credential.token || !credential.instructions) throw new RuntimeError("missing run credential");
      setup = false;
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
      this.c.log("runtime run", { run: spec.id, error: String(err) });
      const end = failureEnd(err, setup, stop.signal);
      await emit("failed", { ...end, result: err instanceof Error ? err.message : String(err) }).catch((e) =>
        this.c.log("terminal event publication failed; lease recovery required", { error: String(e) }),
      );
    } finally {
      heartbeat?.cancel();
      timeout?.cancel();
      parent.removeEventListener("abort", onParent);
      await allocation?.release();
    }
  }
}

// failureEnd is the terminal status and outcome of a run that ended without a result message
// (docs/runtime-protocol.md, Run outcomes from workers). Before the runtime started (setup: RunSpec,
// workspace, credential) it is FAILED setup_failed, or CANCELLED with the stop's outcome when the run
// was stopped meanwhile; afterwards errorEnd decides, and only errors without any evidence stay UNKNOWN.
export function failureEnd(err: unknown, setup: boolean, signal: AbortSignal): { status: TerminalStatus; outcome?: Outcome } {
  const stop = stopOutcome(signal);
  if (setup) {
    if (signal.aborted) return { status: "CANCELLED", ...(stop ? { outcome: stop } : {}) };
    return { status: "FAILED", outcome: "setup_failed" };
  }
  return errorEnd(err, stop);
}

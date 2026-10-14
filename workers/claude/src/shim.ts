// Per-run Claude Agent SDK shim for internal/driver/claude (runtime-drivers D3): runtime.ts
// without the bus, claim and heartbeat. Protocol (JSON lines):
//   stdin:  one Launch line, then control lines. {"start": true} delivers the prompt; the shim
//           waits for it after writing `session` so the driver can persist the session first.
//           {"interrupt": true} requests interruption and EOF on stdin (the driver went away) is
//           treated the same way; before start either ends the run without a prompt.
//   stdout: normalized events, see ShimEvent; exactly one terminal `result` line, then exit 0.
//   --probe: one `probe` line with the Claude Code version and accountInfo, no model call.
// The run token reaches the shim and factory-mcp only through the environment
// (FACTORY_URL, FACTORY_RUN_TOKEN), never through the launch line. Diagnostics go to stderr.
import { execFile } from "node:child_process";
import { readFileSync, realpathSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { createInterface } from "node:readline";
import type { Readable, Writable } from "node:stream";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";
import {
  query as sdkQuery,
  type AccountInfo,
  type SDKMessage,
  type SDKResultMessage,
  type SDKUserMessage,
  type SettingSource,
} from "@anthropic-ai/claude-agent-sdk";
import { clock, type Timer } from "./clock.js";
import { shimEnvironment } from "./env.js";
import type { EventKind, RunEvent, RunSpec, TerminalStatus } from "./protocol.js";
import { AUTH_ERRORS, RuntimeError, runClaude, type QueryFn } from "./runtime.js";

export interface Launch {
  spec: RunSpec;
  cwd: string;
  // Full appended system prompt: agent instructions plus the Factory MCP instructions.
  instructions: string;
  mcp_command?: string;
  executable?: string;
  allow_api_key?: boolean;
  setting_sources?: SettingSource[];
}

// Normalized events (design D1). usage (per model) and cost (total) are cumulative for the query:
// the last line wins. Running usage after each assistant message has no cost; the result's
// modelUsage then gives the final usage with each model's cost.
export type ShimEvent =
  | { event: "session"; id: string }
  | { event: "activity"; kind: string; data: unknown }
  | { event: "usage"; model: string; input: number; output: number; cache_read: number; cache_write: number; cost_usd?: number }
  | { event: "cost"; usd: number; source: "claude_estimate" }
  | { event: "quota"; resets_at?: string; rate_limit_type?: string }
  | { event: "auth_error"; reason: string; api_key_source?: string }
  | { event: "max_turns"; num_turns: number }
  | { event: "error"; class: string; terminal_reason?: string; api_error_status?: number; message?: string }
  | { event: "result"; status: TerminalStatus; result: string; session_id?: string }
  | { event: "probe"; claude_code_version: string; sdk_version: string; account: AccountInfo };


export type Write = (e: ShimEvent) => Promise<void>;

type Tokens = { input: number; output: number; cache_read: number; cache_write: number };

export function lineWriter(out: Writable): Write {
  return (e) => new Promise((resolve, reject) => out.write(JSON.stringify(e) + "\n", (err) => (err ? reject(err) : resolve())));
}

// Maps runtime.ts callbacks and raw SDK messages onto normalized events. Classification uses
// structured fields only (subtype, is_error, error, rate_limit_info.status), never message text.
export class Normalizer {
  private lastResult: SDKResultMessage | undefined;
  private authReported = false;
  private sessionId: string | undefined;
  // Assistant message usage per model and message id: one API message arrives as several
  // assistant messages (one per content block), so the last one per id counts.
  private readonly running = new Map<string, Map<string, Tokens>>();
  finished = false;

  constructor(private readonly write: Write) {}

  readonly emit = async (kind: EventKind, e: Omit<RunEvent, "run_id" | "lease">): Promise<void> => {
    if (kind === "started" && e.session_id) {
      this.sessionId = e.session_id;
      await this.write({ event: "session", id: e.session_id });
    } else if (kind === "progress") {
      await this.write({ event: "activity", kind: e.method ?? "claude/unknown", data: e.data });
    } else if (kind === "completed" || kind === "failed") {
      await this.finish(e.status ?? "UNKNOWN", e.result ?? "");
    }
  };

  readonly observe = async (m: SDKMessage): Promise<void> => {
    if (m.type === "assistant") await this.runningUsage(m.message);
    if (m.type === "rate_limit_event" && m.rate_limit_info.status === "rejected") {
      const resets = m.rate_limit_info.resetsAt;
      await this.write({
        event: "quota",
        ...(resets ? { resets_at: new Date(resets * 1000).toISOString() } : {}),
        ...(m.rate_limit_info.rateLimitType ? { rate_limit_type: m.rate_limit_info.rateLimitType } : {}),
      });
    } else if (m.type === "assistant" && m.error && AUTH_ERRORS.has(m.error) && !this.authReported) {
      this.authReported = true;
      await this.write({ event: "auth_error", reason: m.error });
    } else if (m.type === "result") {
      this.lastResult = m;
      for (const [model, u] of Object.entries(m.modelUsage ?? {})) {
        await this.write({
          event: "usage",
          model,
          input: u.inputTokens,
          output: u.outputTokens,
          cache_read: u.cacheReadInputTokens,
          cache_write: u.cacheCreationInputTokens,
          cost_usd: u.costUSD,
        });
      }
      await this.write({ event: "cost", usd: m.total_cost_usd, source: "claude_estimate" });
    }
  };

  private async runningUsage(msg: { id?: string; model?: string; usage?: unknown } | undefined) {
    const u = msg?.usage as
      | { input_tokens?: number | null; output_tokens?: number | null; cache_read_input_tokens?: number | null; cache_creation_input_tokens?: number | null }
      | undefined;
    if (!msg?.id || !msg.model || msg.model === "<synthetic>" || !u) return;
    const byId = this.running.get(msg.model) ?? new Map<string, Tokens>();
    this.running.set(msg.model, byId);
    byId.set(msg.id, {
      input: u.input_tokens ?? 0,
      output: u.output_tokens ?? 0,
      cache_read: u.cache_read_input_tokens ?? 0,
      cache_write: u.cache_creation_input_tokens ?? 0,
    });
    const sum: Tokens = { input: 0, output: 0, cache_read: 0, cache_write: 0 };
    for (const t of byId.values()) for (const k of Object.keys(sum) as (keyof Tokens)[]) sum[k] += t[k];
    await this.write({ event: "usage", model: msg.model, ...sum });
  }

  // Failure before or instead of terminal evidence: always UNKNOWN, with a structured class.
  async fail(err: unknown): Promise<void> {
    if (this.finished) return;
    const message = err instanceof Error ? err.message : String(err);
    if (err instanceof RuntimeError && err.cls === "auth") {
      if (!this.authReported) {
        this.authReported = true;
        const source = err.detail["api_key_source"];
        await this.write({ event: "auth_error", reason: "api_key_source", ...(typeof source === "string" ? { api_key_source: source } : {}) });
      }
    } else {
      await this.write({ event: "error", class: err instanceof RuntimeError ? err.cls : "runtime", message });
    }
    await this.finish("UNKNOWN", message);
  }

  private async finish(status: TerminalStatus, result: string) {
    if (this.finished) return;
    this.finished = true;
    const m = this.lastResult;
    if (status === "FAILED" && m) {
      if (m.subtype === "error_max_turns") {
        await this.write({ event: "max_turns", num_turns: m.num_turns });
      } else {
        await this.write({
          event: "error",
          class: m.subtype === "success" ? "api_error" : m.subtype,
          ...(m.terminal_reason ? { terminal_reason: m.terminal_reason } : {}),
          ...(m.subtype === "success" && typeof m.api_error_status === "number" ? { api_error_status: m.api_error_status } : {}),
        });
      }
    }
    await this.write({ event: "result", status, result, ...(this.sessionId ? { session_id: this.sessionId } : {}) });
  }
}

export function parseLaunch(line: string): Launch {
  let l: Launch;
  try {
    l = JSON.parse(line) as Launch;
  } catch {
    throw new RuntimeError("launch line is not JSON", "launch");
  }
  const s = l?.spec;
  if (!s || typeof s.id !== "string" || !s.id || typeof s.prompt !== "string" || typeof l.cwd !== "string" || !l.cwd) {
    throw new RuntimeError("launch requires spec.id, spec.prompt and cwd", "launch");
  }
  if (typeof l.instructions !== "string") throw new RuntimeError("launch requires instructions", "launch");
  return l;
}

export interface ShimIO {
  input: Readable;
  output: Writable;
  env: NodeJS.ProcessEnv;
  // External interruption (signals).
  signal?: AbortSignal;
  query?: QueryFn;
  newSessionId?: () => string;
  log?: (line: string) => void;
  startupTimeoutMs?: number;
  interruptTimeoutMs?: number;
}

export async function runShim(io: ShimIO): Promise<void> {
  const n = new Normalizer(lineWriter(io.output));
  const stop = new AbortController();
  io.signal?.addEventListener("abort", () => stop.abort(), { once: true });
  const lines = createInterface({ input: io.input, crlfDelay: Infinity });
  let onLaunch!: (line: string | undefined) => void;
  const launchLine = new Promise<string | undefined>((resolve) => (onLaunch = resolve));
  // Resolves with the driver's start line; rejects when the run is interrupted first.
  let onStart!: () => void;
  let notStarted!: (err: Error) => void;
  const start = new Promise<void>((resolve, reject) => ((onStart = resolve), (notStarted = reject)));
  start.catch(() => {});
  stop.signal.addEventListener("abort", () => notStarted(new RuntimeError("interrupted before start", "not_started")), { once: true });
  let launched = false;
  lines.on("line", (line) => {
    if (!launched) {
      launched = true;
      onLaunch(line);
      return;
    }
    if (!line.trim()) return;
    try {
      const c = JSON.parse(line) as { interrupt?: unknown; start?: unknown };
      if (c.interrupt === true) stop.abort();
      else if (c.start === true) onStart();
      else io.log?.(`shim: ignored control line ${line.slice(0, 200)}`);
    } catch {
      io.log?.("shim: control line is not JSON");
    }
  });
  lines.once("close", () => {
    onLaunch(undefined);
    stop.abort();
  });
  try {
    const line = await launchLine;
    if (line === undefined) throw new RuntimeError("stdin closed before the launch line", "launch");
    const launch = parseLaunch(line);
    if (launch.mcp_command && !io.env["FACTORY_RUN_TOKEN"]) {
      throw new RuntimeError("FACTORY_RUN_TOKEN must be set in the environment for factory-mcp", "launch");
    }
    await runClaude({
      spec: launch.spec,
      cwd: launch.cwd,
      instructions: launch.instructions,
      env: shimEnvironment(io.env, launch.allow_api_key),
      mcpCommand: launch.mcp_command,
      signal: stop.signal,
      emit: n.emit,
      observe: n.observe,
      beforePrompt: () => start,
      newSessionId: io.newSessionId,
      query: io.query,
      executable: launch.executable ?? (io.env["CLAUDE_CODE_EXECUTABLE"] || undefined),
      allowApiKey: launch.allow_api_key,
      settingSources: launch.setting_sources,
      log: io.log,
      startupTimeoutMs: io.startupTimeoutMs,
      interruptTimeoutMs: io.interruptTimeoutMs,
    });
    if (!n.finished) throw new RuntimeError("runtime ended without terminal evidence", "no_result");
  } catch (err) {
    await n.fail(err);
  } finally {
    lines.close();
  }
}

export interface ProbeIO {
  output: Writable;
  env: NodeJS.ProcessEnv;
  executable?: string;
  query?: QueryFn;
  version?: (executable: string) => Promise<string>;
  timeoutMs?: number;
}

const requireHere = createRequire(import.meta.url);

export function sdkVersion(): string {
  const entry = requireHere.resolve("@anthropic-ai/claude-agent-sdk");
  return (JSON.parse(readFileSync(join(dirname(entry), "package.json"), "utf8")) as { version: string }).version;
}

// The native Claude Code binary bundled with the SDK, resolved like the SDK does.
export function bundledExecutable(): string {
  const base = `@anthropic-ai/claude-agent-sdk-${process.platform}-${process.arch}`;
  const exe = process.platform === "win32" ? "claude.exe" : "claude";
  const candidates = process.platform === "linux" ? [base, `${base}-musl`] : [base];
  for (const c of candidates) {
    try {
      return requireHere.resolve(`${c}/${exe}`);
    } catch {
      // try the next libc variant
    }
  }
  throw new RuntimeError(`no bundled Claude Code for ${process.platform}-${process.arch}`, "executable");
}

async function claudeVersion(executable: string): Promise<string> {
  const { stdout } = await promisify(execFile)(executable, ["--version"], { timeout: 30_000 });
  const v = /^\s*(\d+\.\d+\.\d+)/.exec(stdout)?.[1];
  if (!v) throw new RuntimeError("unrecognized claude --version output", "executable");
  return v;
}

// Version and account without a model call: accountInfo is a control request answered while
// the prompt stream stays empty.
export async function probe(io: ProbeIO): Promise<boolean> {
  const write = lineWriter(io.output);
  try {
    const executable = io.executable ?? bundledExecutable();
    const version = await (io.version ?? claudeVersion)(executable).catch((err: unknown) => {
      throw err instanceof RuntimeError ? err : new RuntimeError(err instanceof Error ? err.message : String(err), "executable");
    });
    let close!: () => void;
    const closed = new Promise<void>((resolve) => (close = resolve));
    async function* prompt(): AsyncGenerator<SDKUserMessage> {
      await closed;
    }
    const q = (io.query ?? sdkQuery)({
      prompt: prompt(),
      options: {
        env: shimEnvironment(io.env, true),
        settingSources: [],
        strictMcpConfig: true,
        mcpServers: {},
        pathToClaudeCodeExecutable: executable,
      },
    });
    let timer: Timer | undefined;
    try {
      const timeout = new Promise<never>((_, reject) => {
        timer = clock.timeout(io.timeoutMs ?? 30_000, () => reject(new RuntimeError("probe timed out", "startup_timeout")));
      });
      const account = await Promise.race([q.accountInfo(), timeout]);
      await write({ event: "probe", claude_code_version: version, sdk_version: sdkVersion(), account });
      return true;
    } finally {
      timer?.cancel();
      close();
      q.close();
    }
  } catch (err) {
    await write({
      event: "error",
      class: err instanceof RuntimeError ? err.cls : "runtime",
      message: err instanceof Error ? err.message : String(err),
    });
    return false;
  }
}

async function main() {
  const log = (line: string) => process.stderr.write(line.endsWith("\n") ? line : line + "\n");
  if (process.argv.includes("--probe")) {
    const ok = await probe({ output: process.stdout, env: process.env, executable: process.env["CLAUDE_CODE_EXECUTABLE"] || undefined });
    process.exit(ok ? 0 : 1);
  }
  const stop = new AbortController();
  for (const s of ["SIGINT", "SIGTERM"] as const) process.on(s, () => stop.abort());
  await runShim({ input: process.stdin, output: process.stdout, env: process.env, signal: stop.signal, log });
  process.exit(0);
}

const entry = process.argv[1];
if (entry && import.meta.url === pathToFileURL(realpathSync(entry)).href) {
  main().catch((err) => {
    process.stderr.write(`shim: ${String(err)}\n`);
    process.exit(1);
  });
}

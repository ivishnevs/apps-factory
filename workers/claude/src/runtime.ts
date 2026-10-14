// Claude Agent SDK adapter of the per-run shim (shim.ts), which internal/driver/claude launches. The SDK owns the agent loop; this module owns only startup checks, event forwarding
// and conservative classification of terminal evidence.
import { randomUUID } from "node:crypto";
import { readFileSync } from "node:fs";
import {
  query as sdkQuery,
  type HookCallback,
  type Options,
  type Query,
  type SDKMessage,
  type SDKResultMessage,
  type SDKUserMessage,
  type SettingSource,
} from "@anthropic-ai/claude-agent-sdk";
import type { EventKind, Outcome, RunEvent, RunSpec, TerminalStatus } from "./protocol.js";
import { Redactor } from "./redact.js";
import { clock, type Timer } from "./clock.js";

export type QueryFn = (params: { prompt: AsyncIterable<SDKUserMessage>; options: Options }) => Query;
export type Emit = (kind: EventKind, event: Omit<RunEvent, "run_id" | "lease">) => Promise<void>;

// Structured failure classes, so callers never classify by message text.
export type ErrorClass =
  | "runtime"
  | "launch"
  | "executable"
  | "auth"
  | "startup_timeout"
  | "mcp_unavailable"
  | "session_mismatch"
  | "no_result"
  | "interrupt_timeout"
  | "interrupt_unknown"
  | "not_started";

// The shim reports a RuntimeError's class; the Go driver maps it to a run outcome (claude.Error.RunOutcome).
export class RuntimeError extends Error {
  constructor(
    message: string,
    readonly cls: ErrorClass = "runtime",
    readonly detail: Record<string, unknown> = {},
  ) {
    super(message);
  }
}

export interface RuntimeInput {
  spec: RunSpec;
  cwd: string;
  instructions: string;
  env: Record<string, string | undefined>;
  mcpCommand?: string;
  // Aborted by execution timeout, owner cancellation or lost lease.
  signal: AbortSignal;
  emit: Emit;
  // Called with every runtime message after its progress event (the shim derives usage, quota
  // and auth signals from it).
  observe?: (m: SDKMessage) => Promise<void> | void;
  // Awaited after `started` and before the prompt is delivered: the shim waits for the driver's
  // start line.
  beforePrompt?: () => Promise<void>;
  // Session ID for a new session; random UUID by default.
  newSessionId?: () => string;
  query?: QueryFn;
  executable?: string;
  allowApiKey?: boolean;
  settingSources?: SettingSource[];
  log?: (line: string) => void;
  startupTimeoutMs?: number;
  interruptTimeoutMs?: number;
  // Exit guard (explicit-exits-authority D5): a Stop hook refuses to end a turn without a factory_exit_*
  // call, at most `retries` times; `file` is the MCP adapter's exit file.
  exitGuard?: { file: string; retries: number };
  // Command guard (runtime-drivers D2): a PreToolUse hook decides every shell command line of the
  // Bash and Monitor tools with the run's command rules (factory-shim --decide, see shim.ts).
  commandGuard?: CommandDecider;
}

export type CommandDecision = { decision: "allow" | "deny"; reason?: string };
export type CommandDecider = (command: string) => Promise<CommandDecision>;

// The tools whose input runs a shell command line.
export const COMMAND_TOOLS = ["Bash", "Monitor"] as const;

// commandGuardHook denies a shell tool call the command rules refuse, with the rule's instruction as the
// reason the model sees; allowed calls pass unchanged. A failing decider denies (fail closed).
export function commandGuardHook(decide: CommandDecider, log?: (line: string) => void): HookCallback {
  return async (input) => {
    if (input.hook_event_name !== "PreToolUse" || !(COMMAND_TOOLS as readonly string[]).includes(input.tool_name)) return {};
    const command = (input.tool_input as { command?: unknown } | undefined)?.command;
    if (typeof command !== "string") return {}; // Monitor on a WebSocket runs no command
    let d: CommandDecision;
    try {
      d = await decide(command);
    } catch (err) {
      log?.(`command guard failed: ${String(err)}`);
      d = { decision: "deny", reason: `Factory command guard failed (${err instanceof Error ? err.message : String(err)}); the command was not run.` };
    }
    if (d.decision === "allow") return {};
    return {
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: d.reason || "Factory command policy refused this command.",
      },
    };
  };
}

// The follow-up instruction of the exit guard (internal/driver.ExitGuardPrompt).
export const EXIT_GUARD_PROMPT =
  "Factory: your turn ended without a factory_exit_* call. End the run now with exactly one exit: factory_exit_submit (deliver for review), factory_exit_done (finished; merge first), factory_exit_handoff, factory_exit_ask, factory_exit_block or factory_exit_wait. Do not start new work.";

// exitStopHook blocks a stop while the adapter has not recorded an accepted or queued exit.
export function exitStopHook(guard: { file: string; retries: number }, read: (file: string) => string | undefined): HookCallback {
  let blocked = 0;
  return async () => {
    let state = "none";
    try {
      const raw = read(guard.file);
      if (raw) state = (JSON.parse(raw) as { state?: string }).state ?? "none";
    } catch {
      // no exit recorded
    }
    if (state === "accepted" || state === "pending" || blocked >= guard.retries) return {};
    blocked++;
    return { decision: "block", reason: EXIT_GUARD_PROMPT };
  };
}

// SDKAssistantMessage.error values that mean the account cannot be used, as opposed to a
// retryable API or quota failure.
export const AUTH_ERRORS: ReadonlySet<string> = new Set(["authentication_failed", "oauth_org_not_allowed", "account_on_hold", "verification_required"]);

// StopReason is the abort reason of a run's signal: why the run was stopped and the outcome that stop
// gives it (timed_out for the run's own timeout; none for an owner cancel, whose CANCELLED end is a
// revoke or, for Core's v1 idle stop, recorded idle_timeout by Core).
export class StopReason {
  constructor(
    readonly why: string,
    readonly outcome?: Outcome,
  ) {}
}

export function stopOutcome(signal: AbortSignal): Outcome | undefined {
  return signal.aborted && signal.reason instanceof StopReason ? signal.reason.outcome : undefined;
}

export interface End {
  status: TerminalStatus;
  result: string;
  outcome?: Outcome;
}

// classify maps the result message to the terminal status and outcome. stop is the outcome of the
// reason the run was interrupted; paused tells that the runtime hit its quota or an auth failure.
export function classify(m: SDKResultMessage, interrupted: boolean, stop?: Outcome, paused = false): End {
  if (m.subtype === "success" && !m.is_error) return { status: "COMPLETED", result: m.result };
  const result = m.subtype === "success" ? m.result : [m.subtype, ...m.errors].join("\n");
  // A requested interrupt yields CANCELLED even when triggered by the execution timeout; the stop's
  // outcome (timed_out) makes Core end it FAILED instead of cancelling the task.
  if (interrupted) return { status: "CANCELLED", result, ...(stop ? { outcome: stop } : {}) };
  const outcome: Outcome = m.subtype === "error_max_turns" ? "max_turns" : paused ? "paused_out" : "failed";
  return { status: "FAILED", result, outcome };
}

export function methodOf(m: SDKMessage): string {
  const subtype = (m as { subtype?: unknown }).subtype;
  return typeof subtype === "string" ? `claude/${m.type}/${subtype}` : `claude/${m.type}`;
}

export function runtimeOptions(input: RuntimeInput, sessionId: string, abortController: AbortController): Options {
  const model = input.spec.runtime_options?.["model"];
  const effort = input.spec.runtime_options?.["effort"];
  const maxTurns = input.spec.runtime_options?.["max_turns"];
  return {
    cwd: input.cwd,
    abortController,
    // Replaces the inherited environment: owner/broker secrets were scrubbed by the Worker.
    env: input.env,
    systemPrompt: { type: "preset", preset: "claude_code", append: input.instructions },
    // Trusted MVP runtime, matching Codex danger-full-access + approvalPolicy never.
    permissionMode: "bypassPermissions",
    allowDangerouslySkipPermissions: true,
    // Questions go to the owner through Factory MCP, never through an interactive prompt.
    disallowedTools: ["AskUserQuestion"],
    // User hooks, plugins and MCP servers do not leak into factory runs.
    settingSources: input.settingSources ?? ["project"],
    strictMcpConfig: true,
    mcpServers: input.mcpCommand ? { factory: { type: "stdio", command: input.mcpCommand } } : {},
    ...(typeof model === "string" && model !== "" && model !== "default" ? { model } : {}),
    ...(typeof effort === "string" && effort !== "" ? { effort: effort as Options["effort"] } : {}),
    ...(typeof maxTurns === "number" && Number.isInteger(maxTurns) && maxTurns > 0 ? { maxTurns } : {}),
    ...(input.spec.session_id ? { resume: input.spec.session_id } : { sessionId }),
    ...(input.executable ? { pathToClaudeCodeExecutable: input.executable } : {}),
    ...(input.log ? { stderr: input.log } : {}),
    ...(input.exitGuard || input.commandGuard ? { hooks: runtimeHooks(input) } : {}),
  };
}

function runtimeHooks(input: RuntimeInput): NonNullable<Options["hooks"]> {
  return {
    ...(input.exitGuard ? { Stop: [{ hooks: [exitStopHook(input.exitGuard, readExitFile)] }] } : {}),
    // The hook timeout covers Core's approval check (10 s) with margin.
    ...(input.commandGuard ? { PreToolUse: [{ matcher: COMMAND_TOOLS.join("|"), hooks: [commandGuardHook(input.commandGuard, input.log)], timeout: 60 }] } : {}),
  };
}

async function within<T>(p: Promise<T>, ms: number, what: string, cls: ErrorClass = "runtime"): Promise<T> {
  let timer: Timer | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = clock.timeout(ms, () => reject(new RuntimeError(`${what} timed out`, cls)));
  });
  try {
    return await Promise.race([p, timeout]);
  } finally {
    timer?.cancel();
  }
}

export async function runClaude(input: RuntimeInput): Promise<void> {
  const sessionId = input.spec.session_id || (input.newSessionId ?? randomUUID)();
  let openGate!: () => void;
  const gate = new Promise<void>((resolve) => (openGate = resolve));
  let endInput!: () => void;
  const inputEnded = new Promise<void>((resolve) => (endInput = resolve));
  // Streaming input enables interrupt(); the prompt is held back until startup checks pass.
  async function* prompt(): AsyncGenerator<SDKUserMessage> {
    await gate;
    yield { type: "user", message: { role: "user", content: input.spec.prompt }, parent_tool_use_id: null };
    await inputEnded;
  }
  const abortController = new AbortController();
  const q = (input.query ?? sdkQuery)({ prompt: prompt(), options: runtimeOptions(input, sessionId, abortController) });
  const iterator = q[Symbol.asyncIterator]();
  const redactor = new Redactor();
  let deadlineTimer: Timer | undefined;
  try {
    const startup = input.startupTimeoutMs ?? 30_000;
    const account = await within(q.accountInfo(), startup, "runtime startup", "startup_timeout");
    if (!input.allowApiKey && account.apiKeySource && account.apiKeySource !== "none") {
      throw new RuntimeError(`Claude subscription login required; API key source ${account.apiKeySource} is not allowed`, "auth", {
        api_key_source: account.apiKeySource,
      });
    }
    // Factory MCP is required, like Codex mcp_servers.factory.required: wait while it connects.
    if (input.mcpCommand) await within(factoryConnected(q), startup, "factory MCP startup", "startup_timeout");
    // Core persists the session before the turn executes.
    await input.emit("started", { session_id: sessionId });
    await input.beforePrompt?.();
    openGate();

    const cancelled = new Promise<"cancel">((resolve) => {
      if (input.signal.aborted) resolve("cancel");
      else input.signal.addEventListener("abort", () => resolve("cancel"), { once: true });
    });
    let interrupted = false;
    let interruptFailed: unknown;
    // A rejected rate limit or an auth failure during the turn: a FAILED result is paused_out.
    let paused = false;
    let deadline: Promise<"deadline"> | undefined;
    for (;;) {
      const next = iterator.next();
      let r = await Promise.race([next, deadline ?? cancelled]);
      if (r === "cancel") {
        interrupted = true;
        // Acknowledgement only means interruption was requested, and a refusal may mean the turn just
        // ended: either way wait for terminal evidence.
        await within(q.interrupt(), 5_000, "interrupt", "interrupt_timeout").catch((err: unknown) => {
          interruptFailed = err;
          input.log?.(`interrupt refused; waiting for terminal evidence: ${String(err)}`);
        });
        deadline = new Promise((resolve) => {
          deadlineTimer = clock.timeout(input.interruptTimeoutMs ?? 10_000, () => resolve("deadline"));
        });
        r = await Promise.race([next, deadline]);
      }
      if (r === "deadline") throw interruptFailed ?? new RuntimeError("interrupt result unknown", "interrupt_unknown");
      if (r.done) throw new RuntimeError("runtime ended without terminal evidence", "no_result");
      const m = r.value;
      await input.emit("progress", { method: methodOf(m), data: redactor.data(m) });
      await input.observe?.(m);
      if (m.type === "system" && m.subtype === "init" && m.session_id !== sessionId) {
        throw new RuntimeError(`runtime session ${m.session_id} differs from persisted ${sessionId}`, "session_mismatch", {
          session_id: m.session_id,
        });
      }
      if ((m.type === "rate_limit_event" && m.rate_limit_info.status === "rejected") || (m.type === "assistant" && m.error && AUTH_ERRORS.has(m.error))) {
        paused = true;
      }
      if (m.type === "result") {
        await input.emit("completed", classify(m, interrupted, stopOutcome(input.signal), paused));
        // Let the CLI flush its transcript so the session can be resumed.
        endInput();
        await within(drain(iterator), 5_000, "runtime shutdown").catch((err) => input.log?.(String(err)));
        return;
      }
    }
  } finally {
    deadlineTimer?.cancel();
    endInput();
    q.close();
  }
}

async function factoryConnected(q: Query) {
  for (;;) {
    const status = (await q.mcpServerStatus()).find((s) => s.name === "factory")?.status;
    if (status === "connected") return;
    if (status !== "pending") throw new RuntimeError(`factory MCP server ${status ?? "missing"}`, "mcp_unavailable", { status: status ?? "missing" });
    await clock.sleep(250);
  }
}

async function drain(iterator: AsyncIterator<SDKMessage>) {
  while (!(await iterator.next()).done) {
    // Messages after the result are informational and not forwarded.
  }
}

function readExitFile(file: string): string | undefined {
  try {
    return readFileSync(file, "utf8");
  } catch {
    return undefined;
  }
}

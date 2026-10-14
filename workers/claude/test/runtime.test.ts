import assert from "node:assert/strict";
import { test } from "node:test";
import type { Options, Query, SDKMessage, SDKUserMessage } from "@anthropic-ai/claude-agent-sdk";
import { Redactor, MAX_EVENT_DATA } from "../src/redact.js";
import { RuntimeError, StopReason, classify, exitStopHook, runClaude, type QueryFn } from "../src/runtime.js";
import { shimEnvironment } from "../src/env.js";
import type { RunSpec } from "../src/protocol.js";

const spec: RunSpec = {
  id: "run-1",
  project: "p",
  agent: "a",
  provider: "claude",
  workspace: "/tmp/w",
  prompt: "do it",
  instructions: "be careful",
  timeout_seconds: 60,
  runtime_options: { model: "default" },
};

const result = (fields: Record<string, unknown>) =>
  ({ type: "result", subtype: "success", is_error: false, result: "ok", errors: [], session_id: "s", ...fields }) as unknown as SDKMessage;

interface Fake {
  query: QueryFn;
  options?: Options;
  prompts: SDKUserMessage[];
  interrupted: boolean;
  closed: boolean;
}

// script receives the delivered prompt and yields runtime messages; it may await interrupt.
function fake(
  script: (f: Fake, interrupt: Promise<void>) => AsyncGenerator<SDKMessage>,
  opts: { apiKeySource?: string; mcp?: string[] } = {},
): Fake {
  const f: Fake = { prompts: [], interrupted: false, closed: false, query: undefined as unknown as QueryFn };
  let onInterrupt!: () => void;
  const interrupt = new Promise<void>((r) => (onInterrupt = r));
  const statuses = [...(opts.mcp ?? ["connected"])];
  f.query = ({ prompt, options }) => {
    f.options = options;
    async function* messages() {
      for await (const p of prompt) {
        f.prompts.push(p);
        break;
      }
      yield* script(f, interrupt);
    }
    const it = messages();
    return Object.assign(it, {
      accountInfo: async () => ({ apiKeySource: opts.apiKeySource ?? "none" }),
      mcpServerStatus: async () => [{ name: "factory", status: statuses.length > 1 ? statuses.shift() : statuses[0] }],
      interrupt: async () => {
        f.interrupted = true;
        onInterrupt();
        return undefined;
      },
      close: () => {
        f.closed = true;
      },
    }) as unknown as Query;
  };
  return f;
}

async function run(f: Fake, extra: { signal?: AbortSignal; interruptTimeoutMs?: number; session_id?: string } = {}) {
  const events: { kind: string; status?: string; session_id?: string; method?: string; result?: string; outcome?: string }[] = [];
  const error = await runClaude({
    spec: { ...spec, ...(extra.session_id ? { session_id: extra.session_id } : {}) },
    cwd: "/tmp/w",
    instructions: "factory",
    env: { PATH: "/bin" },
    mcpCommand: "/bin/factory-mcp",
    signal: extra.signal ?? new AbortController().signal,
    emit: async (kind, e) => void events.push({ kind, ...e }),
    query: f.query,
    interruptTimeoutMs: extra.interruptTimeoutMs ?? 1_000,
    startupTimeoutMs: 1_000,
  }).then(
    () => undefined,
    (err: unknown) => err,
  );
  return { events, error };
}

test("successful turn persists session before prompt and completes", async () => {
  let sessionAtInit = "";
  const f = fake(async function* (f) {
    sessionAtInit = f.options!.sessionId!;
    yield { type: "system", subtype: "init", session_id: sessionAtInit } as unknown as SDKMessage;
    yield result({ result: "FOUNDATION_OK" });
  });
  const { events, error } = await run(f);
  assert.equal(error, undefined);
  assert.deepEqual(
    events.map((e) => e.kind),
    ["started", "progress", "progress", "completed"],
  );
  assert.equal(events[0]!.session_id, sessionAtInit);
  assert.equal(events[3]!.status, "COMPLETED");
  assert.equal(events[3]!.result, "FOUNDATION_OK");
  assert.equal(f.prompts[0]!.message.content, "do it");
  assert.equal(f.options!.permissionMode, "bypassPermissions");
  assert.equal(f.options!.strictMcpConfig, true);
  assert.equal(f.options!.model, undefined, "default model is left to Claude Code");
  assert.ok(f.closed);
});

test("resume uses the persisted session and rejects a forked one", async () => {
  const f = fake(async function* () {
    yield { type: "system", subtype: "init", session_id: "other" } as unknown as SDKMessage;
  });
  const { events, error } = await run(f, { session_id: "persisted" });
  assert.equal(f.options!.resume, "persisted");
  assert.equal(events[0]!.session_id, "persisted");
  assert.ok(error instanceof RuntimeError);
});

test("API key auth is refused before the prompt is delivered", async () => {
  const f = fake(async function* () {
    yield result({});
  }, { apiKeySource: "ANTHROPIC_API_KEY" });
  const { events, error } = await run(f);
  assert.ok(error instanceof RuntimeError);
  assert.equal(events.length, 0);
  assert.equal(f.prompts.length, 0);
});

test("factory MCP must connect; pending is awaited", async () => {
  const ok = fake(async function* () {
    yield result({});
  }, { mcp: ["pending", "pending", "connected"] });
  assert.equal((await run(ok)).error, undefined);
  const failed = fake(async function* () {
    yield result({});
  }, { mcp: ["pending", "failed"] });
  const { events, error } = await run(failed);
  assert.match(String(error), /factory MCP server failed/);
  assert.equal(events.length, 0);
});

test("cancellation interrupts and requires terminal evidence", async () => {
  const stop = new AbortController();
  const f = fake(async function* (_, interrupt) {
    yield { type: "system", subtype: "init", session_id: _.options!.sessionId } as unknown as SDKMessage;
    stop.abort();
    await interrupt;
    yield result({ subtype: "error_during_execution", is_error: true, errors: ["aborted"], terminal_reason: "aborted_streaming" });
  });
  const { events, error } = await run(f, { signal: stop.signal });
  assert.equal(error, undefined);
  assert.ok(f.interrupted);
  assert.equal(events.at(-1)!.status, "CANCELLED");

  const silent = new AbortController();
  const g = fake(async function* (_, interrupt) {
    silent.abort();
    await interrupt;
    await new Promise(() => {});
  });
  const lost = await run(g, { signal: silent.signal, interruptTimeoutMs: 50 });
  assert.match(String(lost.error), /interrupt result unknown/);
});

test("a refused interrupt still waits for the result (the turn ended meanwhile)", async () => {
  const stop = new AbortController();
  let release!: () => void;
  const released = new Promise<void>((r) => (release = r));
  const f = fake(async function* (_) {
    yield { type: "system", subtype: "init", session_id: _.options!.sessionId } as unknown as SDKMessage;
    stop.abort();
    await released;
    yield result({ result: "done anyway" });
  });
  const query = f.query;
  f.query = (p) =>
    Object.assign(query(p), {
      interrupt: async () => {
        release();
        throw new Error("no active turn");
      },
    });
  const { events, error } = await run(f, { signal: stop.signal });
  assert.equal(error, undefined);
  assert.equal(events.at(-1)!.status, "COMPLETED");
  assert.equal(events.at(-1)!.result, "done anyway");
});

test("runtime exit without a result is unknown", async () => {
  const f = fake(async function* () {});
  const { error } = await run(f);
  assert.match(String(error), /without terminal evidence/);
});

test("classification", () => {
  const r = (fields: Record<string, unknown>) => result(fields) as Parameters<typeof classify>[0];
  assert.equal(classify(r({}), false).status, "COMPLETED");
  assert.equal(classify(r({}), true).status, "COMPLETED", "a turn that finished before interrupt keeps its outcome");
  assert.equal(classify(r({ is_error: true }), false).status, "FAILED");
  assert.equal(classify(r({ subtype: "error_max_turns", errors: ["x"] }), false).status, "FAILED");
  assert.equal(classify(r({ subtype: "error_during_execution", errors: ["x"] }), true).status, "CANCELLED");
});

// Run outcomes (reliable-recovery 3.4; docs/runtime-protocol.md, Run outcomes from workers).
test("result outcomes", () => {
  const r = (fields: Record<string, unknown>) => result(fields) as Parameters<typeof classify>[0];
  const end = (c: ReturnType<typeof classify>) => `${c.status}/${c.outcome ?? ""}`;
  const cases: [string, ReturnType<typeof classify>, string][] = [
    ["completed", classify(r({}), false), "COMPLETED/"],
    ["completed after the timeout", classify(r({}), true, "timed_out"), "COMPLETED/"],
    ["failed", classify(r({ is_error: true }), false), "FAILED/failed"],
    ["max turns", classify(r({ subtype: "error_max_turns", errors: ["x"] }), false), "FAILED/max_turns"],
    ["quota or auth", classify(r({ is_error: true }), false, undefined, true), "FAILED/paused_out"],
    ["owner cancel", classify(r({ subtype: "error_during_execution", errors: ["x"] }), true), "CANCELLED/"],
    ["hard timeout", classify(r({ subtype: "error_during_execution", errors: ["x"] }), true, "timed_out"), "CANCELLED/timed_out"],
  ];
  for (const [name, got, want] of cases) assert.equal(end(got), want, name);
});

test("a run stopped by its timeout reports timed_out; max turns and quota refine FAILED", async () => {
  const stop = new AbortController();
  const f = fake(async function* (_, interrupt) {
    yield { type: "system", subtype: "init", session_id: _.options!.sessionId } as unknown as SDKMessage;
    stop.abort(new StopReason("run timeout", "timed_out"));
    await interrupt;
    yield result({ subtype: "error_during_execution", is_error: true, errors: ["aborted"] });
  });
  const timedOut = await run(f, { signal: stop.signal });
  assert.equal(timedOut.error, undefined);
  assert.equal(timedOut.events.at(-1)!.status, "CANCELLED");
  assert.equal(timedOut.events.at(-1)!.outcome, "timed_out");

  const maxTurns = await run(
    fake(async function* () {
      yield result({ subtype: "error_max_turns", is_error: true, errors: ["max turns"] });
    }),
  );
  assert.equal(maxTurns.events.at(-1)!.status, "FAILED");
  assert.equal(maxTurns.events.at(-1)!.outcome, "max_turns");

  const quota = await run(
    fake(async function* () {
      yield { type: "rate_limit_event", rate_limit_info: { status: "rejected", rateLimitType: "five_hour" } } as unknown as SDKMessage;
      yield result({ is_error: true, result: "usage limit" });
    }),
  );
  assert.equal(quota.events.at(-1)!.status, "FAILED");
  assert.equal(quota.events.at(-1)!.outcome, "paused_out");
});

test("OpenSpec bodies are omitted and large events bounded", () => {
  const redactor = new Redactor();
  redactor.data({ type: "assistant", message: { content: [{ type: "tool_use", id: "t1", name: "mcp__factory__factory_openspec_read" }] } });
  const out = redactor.data({
    type: "user",
    message: { content: [{ type: "tool_result", tool_use_id: "t1", content: "SECRET SPEC BODY" }] },
    tool_use_result: "SECRET SPEC BODY",
  });
  assert.doesNotMatch(JSON.stringify(out), /SECRET SPEC BODY/);
  const other = redactor.data({ type: "user", message: { content: [{ type: "tool_result", tool_use_id: "t2", content: "visible" }] } });
  assert.match(JSON.stringify(other), /visible/);
  const big = redactor.data({ type: "assistant", message: { content: "x".repeat(MAX_EVENT_DATA) } }) as { truncated: boolean };
  assert.equal(big.truncated, true);
});

test("runtime environment drops owner secrets and API keys", () => {
  const env = shimEnvironment({
    PATH: "/bin",
    FACTORY_TOKEN: "owner",
    NATS_URL: "secret",
    NATS_PASSWORD: "secret",
    FACTORY_RUN_TOKEN: "scoped",
    ANTHROPIC_API_KEY: "key",
  });
  const joined = JSON.stringify(env);
  for (const secret of ["owner", "secret", "key"]) assert.ok(!joined.includes(`"${secret}"`), secret);
  assert.equal(env.FACTORY_RUN_TOKEN, "scoped");
  assert.equal(env.PATH, "/bin");
  assert.equal(env.CLAUDE_CODE_DISABLE_BACKGROUND_TASKS, "1");
  assert.equal(shimEnvironment({ ANTHROPIC_API_KEY: "key" }, true).ANTHROPIC_API_KEY, "key");
});

test("the exit guard blocks stops without an exit, at most retries times", async () => {
  let file: string | undefined;
  const hook = exitStopHook({ file: "exit.json", retries: 2 }, () => file);
  const signal = new AbortController().signal;
  const stop = () => hook({ hook_event_name: "Stop", stop_hook_active: false } as never, undefined, { signal });
  assert.equal(((await stop()) as { decision?: string }).decision, "block");
  assert.equal(((await stop()) as { decision?: string }).decision, "block");
  assert.deepEqual(await stop(), {}, "bounded by retries");
  const accepted = exitStopHook({ file: "exit.json", retries: 2 }, () => file);
  file = JSON.stringify({ state: "accepted", kind: "submit" });
  assert.deepEqual(await accepted(undefined as never, undefined, { signal }), {});
});

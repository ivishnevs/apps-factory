import assert from "node:assert/strict";
import { test } from "node:test";
import type { Options, Query, SDKMessage, SDKUserMessage } from "@anthropic-ai/claude-agent-sdk";
import { Redactor, MAX_EVENT_DATA } from "../src/redact.js";
import { RuntimeError, classify, runClaude, type QueryFn } from "../src/runtime.js";
import { runEnvironment } from "../src/worker.js";
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
  const events: { kind: string; status?: string; session_id?: string; method?: string; result?: string }[] = [];
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

test("runtime environment carries only the run credential", () => {
  const env = runEnvironment(
    { PATH: "/bin", FACTORY_TOKEN: "owner", NATS_URL: "secret", NATS_PASSWORD: "secret", FACTORY_RUN_TOKEN: "old", ANTHROPIC_API_KEY: "key" },
    "http://localhost:8080",
    "scoped",
  );
  const joined = JSON.stringify(env);
  for (const secret of ["owner", "secret", "old", "key"]) assert.ok(!joined.includes(`"${secret}"`), secret);
  assert.equal(env.FACTORY_RUN_TOKEN, "scoped");
  assert.equal(env.PATH, "/bin");
  assert.equal(env.CLAUDE_CODE_DISABLE_BACKGROUND_TASKS, "1");
  assert.equal(runEnvironment({ ANTHROPIC_API_KEY: "key" }, "u", "t", true).ANTHROPIC_API_KEY, "key");
});

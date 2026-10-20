import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PassThrough } from "node:stream";
import { test } from "node:test";
import type { AccountInfo, Options, Query, SDKMessage, SDKUserMessage } from "@anthropic-ai/claude-agent-sdk";
import { costBaseline, probe, runShim, sdkVersion, type Launch, type ShimEvent } from "../src/shim.js";
import type { QueryFn } from "../src/runtime.js";

// Recorded SDK message streams shared with internal/driver/claude (Go) tests. Each scenario is
// <name>.json (launch line, environment, account, MCP status sequence, interrupt point),
// <name>.sdk.jsonl (SDK messages in delivery order) and <name>.events.jsonl (expected shim stdout).
// UPDATE_TESTDATA=1 npm test rewrites the .events.jsonl files.
const TESTDATA = new URL("../../../../internal/driver/claude/testdata/", import.meta.url);

interface Scenario {
  launch: Launch;
  env: Record<string, string>;
  account: AccountInfo;
  mcp_status: string[];
  new_session_id?: string;
  // Index of the first message delivered only after the shim called interrupt().
  interrupt_after?: number;
}

interface Seen {
  options?: Options;
  prompts: SDKUserMessage[];
  interrupted: boolean;
  closed: boolean;
}

const jsonl = <T>(text: string): T[] =>
  text
    .split("\n")
    .filter((l) => l.trim())
    .map((l) => JSON.parse(l) as T);

// A Query that replays messages after the prompt is delivered. At interrupt_after it sends the
// interrupt control line through the shim's stdin, like the driver, and waits for interrupt().
function replay(messages: SDKMessage[], s: Pick<Scenario, "account" | "mcp_status" | "interrupt_after">, stdin: PassThrough) {
  const seen: Seen = { prompts: [], interrupted: false, closed: false };
  let onInterrupt!: () => void;
  const interrupt = new Promise<void>((r) => (onInterrupt = r));
  const statuses = [...s.mcp_status];
  const query: QueryFn = ({ prompt, options }) => {
    seen.options = options;
    async function* stream() {
      for await (const p of prompt) {
        seen.prompts.push(p);
        break;
      }
      for (const [i, m] of messages.entries()) {
        if (i === s.interrupt_after) {
          stdin.write(JSON.stringify({ interrupt: true }) + "\n");
          await interrupt;
        }
        yield m;
      }
    }
    return Object.assign(stream(), {
      accountInfo: async () => s.account,
      mcpServerStatus: async () => [{ name: "factory", status: statuses.length > 1 ? statuses.shift() : statuses[0] }],
      interrupt: async () => {
        seen.interrupted = true;
        onInterrupt();
        return undefined;
      },
      close: () => {
        seen.closed = true;
      },
    }) as unknown as Query;
  };
  return { query, seen };
}

// Runs the shim like the driver: launch line, then the start line once `session` was written.
async function shim(
  launchLine: string | undefined,
  query: QueryFn,
  env: Record<string, string>,
  stdin = new PassThrough(),
  newSessionId?: string,
  onSession = (stdin: PassThrough) => void stdin.write(JSON.stringify({ start: true }) + "\n"),
) {
  const stdout = new PassThrough();
  let text = "";
  let session = false;
  stdout.on("data", (d: Buffer) => {
    text += d.toString();
    if (!session && jsonl<ShimEvent>(text).some((e) => e.event === "session")) {
      session = true;
      onSession(stdin);
    }
  });
  if (launchLine !== undefined) stdin.write(launchLine + "\n");
  else stdin.end();
  await runShim({
    input: stdin,
    output: stdout,
    env,
    query,
    newSessionId: newSessionId ? () => newSessionId : undefined,
    startupTimeoutMs: 1_000,
    interruptTimeoutMs: 1_000,
  });
  stdin.end();
  return jsonl<ShimEvent>(text);
}

async function scenario(name: string) {
  const s = JSON.parse(readFileSync(new URL(`${name}.json`, TESTDATA), "utf8")) as Scenario;
  const messages = jsonl<SDKMessage>(readFileSync(new URL(`${name}.sdk.jsonl`, TESTDATA), "utf8"));
  const stdin = new PassThrough();
  const { query, seen } = replay(messages, s, stdin);
  const launchLine = JSON.stringify(s.launch);
  const events = await shim(launchLine, query, s.env, stdin, s.new_session_id);
  const golden = new URL(`${name}.events.jsonl`, TESTDATA);
  if (process.env["UPDATE_TESTDATA"] === "1") writeFileSync(golden, events.map((e) => JSON.stringify(e)).join("\n") + "\n");
  assert.ok(existsSync(golden), `${golden.pathname} missing; run UPDATE_TESTDATA=1 npm test`);
  assert.deepEqual(events, jsonl<ShimEvent>(readFileSync(golden, "utf8")), `${name}: shim output differs from ${name}.events.jsonl`);

  // Common protocol invariants.
  assert.equal(events[0]!.event, "session");
  assert.equal(events.filter((e) => e.event === "result").length, 1);
  assert.equal(events.at(-1)!.event, "result");
  assert.equal(seen.prompts[0]!.message.content, s.launch.spec.prompt);
  assert.ok(seen.closed);
  // The run token reaches the runtime (and factory-mcp by inheritance) only through the environment.
  const token = s.env["FACTORY_RUN_TOKEN"]!;
  assert.ok(!launchLine.includes(token));
  assert.equal(seen.options!.env!["FACTORY_RUN_TOKEN"], token);
  assert.deepEqual(seen.options!.mcpServers, { factory: { type: "stdio", command: s.launch.mcp_command } });
  assert.equal(seen.options!.env!["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"], "1");
  return { events, seen, s };
}

const last = (events: ShimEvent[]) => events.at(-1) as Extract<ShimEvent, { event: "result" }>;

test("recorded start: pre-assigned session, activity, usage, cost, completed", async () => {
  const { events, seen, s } = await scenario("start");
  assert.equal(seen.options!.sessionId, s.new_session_id);
  assert.equal(seen.options!.resume, undefined);
  assert.deepEqual(events[0], { event: "session", id: s.new_session_id });
  type UsageEvent = Extract<ShimEvent, { event: "usage" }>;
  const usage = events.filter((e): e is UsageEvent => e.event === "usage");
  // Running usage per assistant message (the last per message id counts), then the result's.
  assert.deepEqual(
    usage.map((e) => [e.model, e.input, e.output, e.cost_usd]),
    [
      ["claude-opus-4-7", 3, 12, undefined],
      ["claude-opus-4-7", 3, 61, undefined],
      ["claude-opus-4-7", 8, 70, undefined],
      ["claude-opus-4-7", 8, 70, 0.0912],
      ["claude-haiku-4-5", 412, 18, 0.0029],
    ],
  );
  assert.deepEqual(events.find((e) => e.event === "cost"), { event: "cost", usd: 0.0941, source: "claude_estimate" });
  assert.deepEqual(last(events), { event: "result", status: "COMPLETED", result: "FOUNDATION_OK", session_id: s.new_session_id });
  assert.ok(!events.some((e) => e.event === "error"));
});

test("recorded resume: persisted session is resumed", async () => {
  const { events, seen, s } = await scenario("resume");
  assert.equal(seen.options!.resume, s.launch.spec.session_id);
  assert.equal(seen.options!.sessionId, undefined);
  assert.deepEqual(events[0], { event: "session", id: s.launch.spec.session_id });
  assert.equal(last(events).status, "COMPLETED");
});

test("resumed session: usage and cost are the run's own, less the transcript's last cost-state", async () => {
  const s = JSON.parse(readFileSync(new URL("resume.json", TESTDATA), "utf8")) as Scenario;
  const id = s.launch.spec.session_id!;
  const messages = jsonl<SDKMessage>(readFileSync(new URL("resume.sdk.jsonl", TESTDATA), "utf8"));
  // Claude Code restores the session's cost state on resume: the result covers earlier runs too,
  // including an auxiliary model only an earlier run used.
  const result = messages.at(-1) as unknown as { total_cost_usd: number; modelUsage: Record<string, unknown> };
  result.total_cost_usd = 0.1397;
  result.modelUsage["claude-haiku-4-5"] = { inputTokens: 1981, outputTokens: 13, cacheReadInputTokens: 0, cacheCreationInputTokens: 0, costUSD: 0.002 };
  const config = mkdtempSync(join(tmpdir(), "claude-config-"));
  const project = join(config, "projects", "-tmp-factory-demo");
  mkdirSync(project, { recursive: true });
  const state = (opus: number[], cost: number) =>
    JSON.stringify({
      type: "cost-state",
      sessionId: id,
      totalCostUSD: cost + 0.002,
      modelUsage: {
        "claude-haiku-4-5": { inputTokens: 1981, outputTokens: 13, cacheReadInputTokens: 0, cacheCreationInputTokens: 0, costUSD: 0.002 },
        "claude-opus-4-7": { inputTokens: opus[0], outputTokens: opus[1], cacheReadInputTokens: opus[2], cacheCreationInputTokens: opus[3], costUSD: cost },
      },
    });
  writeFileSync(
    join(project, `${id}.jsonl`),
    [JSON.stringify({ type: "user", sessionId: id }), state([4, 50, 10000, 9000], 0.05), state([10, 100, 30000, 15000], 0.1), ""].join("\n"),
  );
  const stdin = new PassThrough();
  const { query } = replay(messages, s, stdin);
  const events = await shim(JSON.stringify(s.launch), query, { ...s.env, CLAUDE_CONFIG_DIR: config }, stdin);
  type UsageEvent = Extract<ShimEvent, { event: "usage" }>;
  const usage = events.filter((e): e is UsageEvent => e.event === "usage");
  // Running usage is the run's own already; the final line is the result less the baseline, and the
  // haiku line (unchanged since the baseline) is not this run's.
  assert.deepEqual(usage.at(-1), { event: "usage", model: "claude-opus-4-7", input: 2, output: 4, cache_read: 7016, cache_write: 102, cost_usd: 0.0377 });
  assert.ok(!usage.some((e) => e.model === "claude-haiku-4-5"));
  assert.deepEqual(events.find((e) => e.event === "cost"), { event: "cost", usd: 0.0377, source: "claude_estimate" });
  assert.equal(last(events).status, "COMPLETED");
});

test("cost baseline: none without a transcript or a cost-state entry", async () => {
  const config = mkdtempSync(join(tmpdir(), "claude-config-"));
  assert.equal(await costBaseline({ CLAUDE_CONFIG_DIR: config }, "s1"), undefined);
  mkdirSync(join(config, "projects", "p"), { recursive: true });
  writeFileSync(join(config, "projects", "p", "s1.jsonl"), JSON.stringify({ type: "user" }) + "\n");
  assert.equal(await costBaseline({ CLAUDE_CONFIG_DIR: config }, "s1"), undefined);
  assert.equal(await costBaseline({ HOME: join(config, "nohome") }, "s1"), undefined);
});

test("recorded interrupt: stdin interrupt yields CANCELLED from the result evidence", async () => {
  const { events, seen } = await scenario("interrupt");
  assert.ok(seen.interrupted);
  assert.equal(last(events).status, "CANCELLED");
  assert.ok(events.some((e) => e.event === "usage"), "an interrupted turn still reports usage");
  const result = events.findIndex((e) => e.event === "activity" && e.kind.startsWith("claude/result/"));
  assert.ok(events.findIndex((e) => e.event === "usage") < result, "running usage precedes the result");
  assert.ok(!events.some((e) => e.event === "error"));
});

test("recorded max turns: max_turns event, FAILED", async () => {
  const { events, seen } = await scenario("max_turns");
  assert.equal(seen.options!.maxTurns, 1);
  assert.equal(seen.options!.effort, "low");
  assert.equal(seen.options!.model, "claude-opus-4-7");
  assert.deepEqual(events.at(-2), { event: "max_turns", num_turns: 2 });
  assert.equal(last(events).status, "FAILED");
});

test("recorded rate limit: rejected rate_limit_event is a quota event with the reset time", async () => {
  const { events } = await scenario("rate_limit");
  const quota = events.filter((e) => e.event === "quota");
  assert.deepEqual(quota, [{ event: "quota", resets_at: "2026-10-13T23:00:00.000Z", rate_limit_type: "five_hour" }]);
  assert.deepEqual(events.at(-2), { event: "error", class: "api_error", terminal_reason: "api_error", api_error_status: 429 });
  assert.equal(last(events).status, "FAILED");
});

const baseLaunch = (): Launch => ({
  spec: {
    id: "run-x",
    project: "p",
    agent: "a",
    provider: "claude",
    workspace: "/tmp/w",
    prompt: "do it",
    instructions: "i",
    timeout_seconds: 60,
  },
  cwd: "/tmp/w",
  instructions: "factory",
});

const account: AccountInfo = { apiKeySource: "none" };

test("API key auth is an auth_error before the prompt", async () => {
  const { query, seen } = replay([], { account: { apiKeySource: "ANTHROPIC_API_KEY" }, mcp_status: ["connected"] }, new PassThrough());
  const events = await shim(JSON.stringify(baseLaunch()), query, { ANTHROPIC_API_KEY: "k" });
  assert.deepEqual(events, [
    { event: "auth_error", reason: "api_key_source", api_key_source: "ANTHROPIC_API_KEY" },
    { event: "result", status: "UNKNOWN", result: "Claude subscription login required; API key source ANTHROPIC_API_KEY is not allowed" },
  ]);
  assert.equal(seen.prompts.length, 0);
  assert.equal(seen.options!.env!["ANTHROPIC_API_KEY"], undefined);
});

test("structured assistant authentication error is an auth_error", async () => {
  const msgs = [
    { type: "assistant", error: "authentication_failed", message: { content: [] }, session_id: "s" },
    { type: "result", subtype: "success", is_error: true, result: "x", num_turns: 1, total_cost_usd: 0, modelUsage: {}, session_id: "s" },
  ] as unknown as SDKMessage[];
  const { query } = replay(msgs, { account, mcp_status: ["connected"] }, new PassThrough());
  const events = await shim(JSON.stringify(baseLaunch()), query, {}, undefined, "s");
  assert.deepEqual(
    events.filter((e) => e.event !== "activity").map((e) => e.event),
    ["session", "auth_error", "cost", "error", "result"],
  );
});

test("invalid launch and early EOF are classified launch errors", async () => {
  const { query, seen } = replay([], { account, mcp_status: ["connected"] }, new PassThrough());
  assert.deepEqual((await shim("{not json", query, {}))[0], { event: "error", class: "launch", message: "launch line is not JSON" });
  assert.equal((await shim(undefined, query, {}))[0]!.event, "error");
  const mcp = { ...baseLaunch(), mcp_command: "/bin/factory-mcp" };
  const noToken = await shim(JSON.stringify(mcp), query, {});
  assert.deepEqual(
    noToken.map((e) => e.event),
    ["error", "result"],
  );
  assert.equal(seen.options, undefined, "runtime is not started without a valid launch");
});

test("the prompt waits for the start line; interrupt before start ends without a prompt", async () => {
  const { query, seen } = replay([], { account, mcp_status: ["connected"] }, new PassThrough());
  let prompted = -1;
  const events = await shim(JSON.stringify(baseLaunch()), query, {}, undefined, "s", (stdin) => {
    // Give the runtime a chance to deliver the prompt early before interrupting.
    setTimeout(() => {
      prompted = seen.prompts.length;
      stdin.write(JSON.stringify({ interrupt: true }) + "\n");
    }, 50);
  });
  assert.equal(prompted, 0);
  assert.equal(seen.prompts.length, 0);
  assert.equal(seen.interrupted, false, "no turn to interrupt");
  assert.deepEqual(events, [
    { event: "session", id: "s" },
    { event: "error", class: "not_started", message: "interrupted before start" },
    { event: "result", status: "UNKNOWN", result: "interrupted before start", session_id: "s" },
  ]);
});

test("stdin EOF after launch interrupts the run", async () => {
  const stdin = new PassThrough();
  let onInterrupt!: () => void;
  const interrupted = new Promise<void>((r) => (onInterrupt = r));
  const query: QueryFn = ({ prompt }) => {
    async function* stream() {
      for await (const _ of prompt) break;
      yield { type: "system", subtype: "init", session_id: "s" } as unknown as SDKMessage;
      stdin.end();
      await interrupted;
      yield { type: "result", subtype: "error_during_execution", is_error: true, errors: [], num_turns: 0, total_cost_usd: 0, modelUsage: {}, terminal_reason: "aborted_streaming", session_id: "s" } as unknown as SDKMessage;
    }
    return Object.assign(stream(), {
      accountInfo: async () => account,
      interrupt: async () => void onInterrupt(),
      close: () => {},
    }) as unknown as Query;
  };
  const events = await shim(JSON.stringify(baseLaunch()), query, {}, stdin, "s");
  assert.equal(last(events).status, "CANCELLED");
});

test("probe reports version and account without a prompt", async () => {
  let prompted = false;
  let options: Options | undefined;
  const query: QueryFn = ({ prompt, options: o }) => {
    options = o;
    async function* stream(): AsyncGenerator<SDKMessage> {
      for await (const _ of prompt) prompted = true;
    }
    return Object.assign(stream(), { accountInfo: async () => ({ apiKeySource: "none", subscriptionType: "max" }), close: () => {} }) as unknown as Query;
  };
  const out = new PassThrough();
  let text = "";
  out.on("data", (d: Buffer) => (text += d.toString()));
  const ok = await probe({ output: out, env: {}, executable: "/opt/claude", query, version: async () => "2.1.284" });
  assert.ok(ok);
  assert.deepEqual(jsonl(text), [
    { event: "probe", claude_code_version: "2.1.284", sdk_version: sdkVersion(), account: { apiKeySource: "none", subscriptionType: "max" } },
  ]);
  assert.equal(sdkVersion(), "0.3.284");
  assert.equal(options!.pathToClaudeCodeExecutable, "/opt/claude");
  assert.equal(prompted, false);

  text = "";
  const failed = await probe({ output: out, env: {}, executable: "/missing/claude", query, version: async () => Promise.reject(new Error("ENOENT")) });
  assert.equal(failed, false);
  assert.deepEqual(jsonl(text), [{ event: "error", class: "executable", message: "ENOENT" }]);
});

test("command guard: shell tool calls are decided by factory-shim --decide before they run", async () => {
  const dir = mkdtempSync(join(tmpdir(), "guard-"));
  // A fake factory-shim (CommonJS, no package.json around it): denies cliclick, fails on "crash" and
  // records each request with the environment it ran with.
  const fake = join(dir, "factory-shim");
  writeFileSync(
    fake,
    `#!/usr/bin/env node
let input = "";
process.stdin.on("data", (d) => (input += d)).on("end", () => {
  const { command } = JSON.parse(input);
  require("node:fs").appendFileSync(${JSON.stringify(join(dir, "seen"))}, JSON.stringify([process.argv[2], process.env.FACTORY_SHIM_DIR, process.env.FACTORY_RUN_TOKEN, command]) + "\\n");
  if (command === "crash") process.exit(3);
  console.log(JSON.stringify(command.includes("cliclick") ? { decision: "deny", reason: "cliclick is denied by policy rule host-ui-input." } : { decision: "allow" }));
});
`,
  );
  chmodSync(fake, 0o755);

  const answers: unknown[] = [];
  const query: QueryFn = ({ prompt, options }) => {
    async function* stream(): AsyncGenerator<SDKMessage> {
      for await (const _ of prompt) break;
      const matcher = options.hooks!.PreToolUse![0]!;
      assert.equal(matcher.matcher, "Bash|Monitor");
      const signal = new AbortController().signal;
      const calls = [
        ["Bash", "cd app && cliclick c:1"],
        ["Bash", "git status"],
        ["Monitor", "tail -f log"],
        ["Read", "cliclick"],
        ["Bash", "crash"],
      ];
      for (const [tool, command] of calls) {
        const input = { hook_event_name: "PreToolUse", tool_name: tool, tool_input: { command }, tool_use_id: "t" };
        answers.push(await matcher.hooks[0]!(input as never, "t", { signal }));
      }
      yield { type: "result", subtype: "success", is_error: false, result: "ok", num_turns: 1, total_cost_usd: 0, modelUsage: {}, session_id: "s" } as unknown as SDKMessage;
    }
    return Object.assign(stream(), { accountInfo: async () => account, interrupt: async () => {}, close: () => {} }) as unknown as Query;
  };
  const env = { FACTORY_SHIM_DIR: dir, FACTORY_RUN_TOKEN: "frt", PATH: process.env["PATH"] ?? "" };
  const launch = JSON.stringify({ ...baseLaunch(), guard_command: fake });
  const events = await shim(launch, query, env, undefined, "s");
  assert.equal(last(events).status, "COMPLETED");
  const denied = (a: unknown, reason: RegExp) => {
    const o = (a as { hookSpecificOutput: { hookEventName: string; permissionDecision: string; permissionDecisionReason: string } }).hookSpecificOutput;
    assert.equal(o.hookEventName, "PreToolUse");
    assert.equal(o.permissionDecision, "deny");
    assert.match(o.permissionDecisionReason, reason);
  };
  denied(answers[0], /host-ui-input/);
  assert.deepEqual(answers.slice(1, 4), [{}, {}, {}]);
  denied(answers[4], /command guard failed/);
  // Read is not a shell tool: factory-shim saw only the shell command lines, with the shim's env.
  assert.deepEqual(
    readFileSync(join(dir, "seen"), "utf8")
      .trim()
      .split("\n")
      .map((l) => JSON.parse(l) as string[]),
    ["cd app && cliclick c:1", "git status", "tail -f log", "crash"].map((c) => ["--decide", dir, "frt", c]),
  );

  // The guard needs the run's shim directory.
  const noDir = await shim(launch, query, {}, undefined, "s");
  assert.deepEqual(
    noDir.map((e) => e.event),
    ["error", "result"],
  );
});

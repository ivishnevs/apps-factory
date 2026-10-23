// A run's transcript from Core's run events, parsed like internal/tui/transcript_parse.go: the agent's text,
// its reasoning, every tool call with its full input and result, refusals, errors and the end of the run.
import type { RunEvent, ToolCall } from "@/lib/api/types";
import type { TxEntry } from "@/lib/api/types-live";

type Obj = Record<string, unknown>;
const isObj = (v: unknown): v is Obj => typeof v === "object" && v !== null && !Array.isArray(v);
const str = (m: unknown, k: string): string => {
  if (!isObj(m)) return "";
  const v = m[k];
  return typeof v === "string" ? v : v == null ? "" : String(v);
};

export const isFactoryTool = (name?: string) => !!name && name.includes("factory_");
export const shortTool = (name: string) => {
  const i = name.lastIndexOf("__");
  return i >= 0 && name.startsWith("mcp__") ? name.slice(i + 2) : name;
};
const pretty = (v: unknown): string => (v == null ? "" : typeof v === "string" ? v : JSON.stringify(v, null, 2));
export const oneLine = (s: string) => s.split(/\s+/).filter(Boolean).join(" ");
const clip = (s: string, n: number) => (s.length <= n ? s : s.slice(0, n - 1) + "…");

const commandText = (v: unknown): string => (typeof v === "string" ? v : Array.isArray(v) ? v.map(String).join(" ") : "");

// brief: the one-line form of a tool input (runlog.go brief), keys sorted.
export function brief(v?: Obj | null): string {
  if (!v) return "";
  return Object.keys(v).sort().map((k) => {
    const x = v[k];
    let s: string;
    if (typeof x === "string") s = /[ ,]/.test(x) ? JSON.stringify(clip(x, 60)) : x;
    else if (x == null) s = "-";
    else if (typeof x === "object") s = clip(JSON.stringify(x), 40);
    else s = String(x);
    return `${k}=${s}`;
  }).join(" ");
}

function toolArg(name: string, input: Obj): string {
  if (isFactoryTool(name)) return brief(input);
  let arg = str(input, "file_path");
  if (!arg) {
    const p = str(input, "pattern");
    if (p) {
      arg = JSON.stringify(p);
      const path = str(input, "path");
      if (path) arg += " in " + path;
    }
  }
  if (!arg) arg = str(input, "url") || str(input, "query") || str(input, "description");
  return arg || brief(input);
}

function fullResult(v: unknown): string {
  if (typeof v === "string") return v;
  if (v == null) return "";
  if (Array.isArray(v)) {
    return v.filter(isObj).map((m) => (m.type === "text" || m.type == null ? str(m, "text") : m.type === "image" ? "[image]" : pretty(m))).join("\n");
  }
  return pretty(v);
}

function reasoningText(item: Obj): string {
  for (const k of ["summary", "content"]) {
    const list = item[k];
    if (!Array.isArray(list)) continue;
    const t = list.map((x) => (typeof x === "string" ? x : str(x, "text"))).join("\n").trim();
    if (t) return t;
  }
  return "";
}

class Builder {
  out: TxEntry[] = [];
  calls = new Map<string, number>();
  n = 0;

  add(e: Omit<TxEntry, "key">) {
    if (e.kind === "tool" && e.id) {
      const i = this.calls.get(e.id);
      if (i !== undefined) {
        // A completed item replaces its started form (Codex), keeping the original time and key.
        this.out[i] = { ...e, at: this.out[i].at, key: this.out[i].key };
        return;
      }
      this.calls.set(e.id, this.out.length);
    }
    this.out.push({ ...e, key: `e${this.n++}` });
  }

  result(at: number, id: string, text: string, isError: boolean) {
    const i = this.calls.get(id);
    if (i !== undefined) {
      this.out[i] = { ...this.out[i], result: text, done: true, isError };
      return;
    }
    this.add({ at, kind: "tool", tool: "result", result: text, done: true, isError });
  }

  toolCall(at: number, id: string, name: string, input: Obj): Omit<TxEntry, "key"> {
    if (name === "Bash" || name === "shell" || name === "local_shell") {
      let cmd = commandText(input.command);
      const argv = input.command;
      if (Array.isArray(argv) && argv.length === 3 && (argv[1] === "-lc" || argv[1] === "-c")) cmd = String(argv[2]);
      const d = str(input, "description");
      // The brief skips a leading "cd <dir> &&" (agents prefix most commands with their worktree), so the
      // row shows what the command does; the full command stays in the expanded input.
      const brief = cmd.replace(/^\s*cd\s+("[^"]*"|'[^']*'|\S+)\s*(&&|;|\n)\s*/, "");
      return { at, kind: "tool", tool: "Bash", id, input: cmd + (d ? `\n# ${d}` : ""), brief: oneLine(brief || cmd) };
    }
    return { at, kind: "tool", tool: shortTool(name), id, input: pretty(input), brief: toolArg(name, input) };
  }

  claudeMessage(at: number, data: Obj) {
    const msg = isObj(data.message) ? data.message : {};
    const role = str(msg, "role") || str(data, "type");
    const content = msg.content;
    if (typeof content === "string") {
      if (role === "assistant" && content.trim()) this.add({ at, kind: "say", text: content });
      return;
    }
    if (!Array.isArray(content)) return;
    for (const block of content.filter(isObj)) {
      switch (block.type) {
        case "text":
          if (role === "assistant" && str(block, "text").trim()) this.add({ at, kind: "say", text: str(block, "text") });
          break;
        case "thinking": {
          const t = str(block, "thinking");
          if (t.trim()) this.add({ at, kind: "think", text: t });
          break;
        }
        case "redacted_thinking":
          this.add({ at, kind: "think", text: "(reasoning redacted by the provider)" });
          break;
        case "tool_use":
          this.add(this.toolCall(at, str(block, "id"), str(block, "name"), isObj(block.input) ? block.input : {}));
          break;
        case "tool_result":
          this.result(at, str(block, "tool_use_id"), fullResult(block.content), block.is_error === true);
          break;
      }
    }
  }

  codexItem(at: number, item: Obj, done: boolean) {
    const id = str(item, "id");
    const failed = () => ["failed", "declined"].includes(str(item, "status"));
    switch (item.type) {
      case "agentMessage":
        if (done) this.add({ at, kind: "say", text: str(item, "text") });
        break;
      case "reasoning":
        if (done) {
          const t = reasoningText(item);
          if (t) this.add({ at, kind: "think", text: t });
        }
        break;
      case "commandExecution": {
        const e = this.toolCall(at, id, "Bash", { command: item.command });
        if (done) {
          e.done = true;
          e.result = str(item, "aggregatedOutput");
          const code = item.exitCode;
          if (typeof code === "number" && code !== 0) {
            e.isError = true;
            e.result = e.result.replace(/\n+$/, "") + `\n(exit ${code})`;
          }
          if (failed()) e.isError = true;
        }
        this.add(e);
        break;
      }
      case "mcpToolCall": {
        const e = this.toolCall(at, id, str(item, "tool"), isObj(item.arguments) ? item.arguments : {});
        if (done) {
          e.done = true;
          if (isObj(item.error)) {
            e.isError = true;
            e.result = str(item.error, "message");
          } else if (isObj(item.result)) {
            e.result = fullResult(item.result.content);
            if (item.result.isError === true) e.isError = true;
          }
          if (str(item, "status") === "failed") e.isError = true;
        }
        this.add(e);
        break;
      }
      case "fileChange": {
        const changes = Array.isArray(item.changes) ? item.changes.filter(isObj) : [];
        const paths = changes.map((c) => str(c, "path"));
        const diffs = changes.map((c) => str(c, "diff")).filter(Boolean);
        this.add({ at, kind: "tool", tool: "Edit", id, brief: paths.join(", "), input: paths.join("\n"),
          ...(done ? { done: true, result: diffs.join("\n"), isError: failed() } : {}) });
        break;
      }
      case "webSearch":
        this.add({ at, kind: "tool", tool: "WebSearch", id, input: str(item, "query"), brief: str(item, "query"), done });
        break;
    }
  }

  event(ev: RunEvent) {
    const env = ev.envelope;
    const p = env.payload as RunEvent["envelope"]["payload"] & { quota?: { type?: string } };
    const at = Date.parse(env.occurred_at) || 0;
    if (p.status === "ENDED") {
      let text = "Run ended " + (p.outcome ?? "");
      if (p.exit_state) text += ` (exit ${p.exit_state})`;
      this.add({ at, kind: "end", text: p.result ?? "", outcome: p.outcome, brief: text });
      return;
    }
    if (p.quota) {
      this.add({ at, kind: "error", text: `Quota limit hit (${p.quota.type ?? "unknown"})` });
      return;
    }
    const data: Obj = isObj(p.data) ? p.data : {};
    const m = p.method ?? "";
    if (m === "claude/system/init") this.add({ at, kind: "info", text: "Session started, model " + (str(data, "model") || "unknown") });
    else if (m === "claude/assistant" || m === "claude/user") this.claudeMessage(at, data);
    else if (m.startsWith("claude/shim/")) {
      const e = m.slice("claude/shim/".length);
      if (!["usage", "cost", "quota", "session"].includes(e)) this.add({ at, kind: "error", text: "Runtime " + e });
    } else if (m === "item/started" || m === "item/completed") {
      if (isObj(data.item)) this.codexItem(at, data.item, m === "item/completed");
    } else if (m === "factory/command_refused") {
      const argv = Array.isArray(data.argv) ? data.argv.map(String).join(" ") : "";
      this.add({ at, kind: "refusal", tool: "command", text: `Command refused (${str(data, "effect")}, rule ${str(data, "rule")}): ${argv}` });
    } else if (m.endsWith("/exit_guard")) {
      const r = str(data, "reason");
      this.add({ at, kind: "refusal", tool: "exit_guard", text: "Exit guard refused the turn's end" + (r ? `: ${r}` : "") });
    } else if (m === "codex/quota") this.add({ at, kind: "error", text: "Quota limit hit" });
    else if (m === "error") {
      const msg = str(data, "message") || str(data.error, "message");
      this.add({ at, kind: "error", text: "Runtime error: " + msg });
    }
  }

  coreCalls(calls: ToolCall[]) {
    for (const tc of calls) {
      if (tc.status >= 400) this.out.push({ key: `c${tc.id}`, at: tc.created_at * 1000, kind: "refusal", tool: "core", text: `Core refused ${tc.tool} (${tc.status})` });
    }
  }

  entries() {
    // Stable sort by time (Array.prototype.sort is stable).
    return [...this.out].sort((a, b) => a.at - b.at);
  }
}

export function transcriptFromEvents(events: RunEvent[], calls: ToolCall[] = []): TxEntry[] {
  const b = new Builder();
  for (const e of events) b.event(e);
  b.coreCalls(calls);
  return b.entries();
}

// errorCount counts tool errors and refusals once per incident (transcript_parse.go tallyErrors).
export function errorCount(entries: TxEntry[]): number {
  const core = entries.some((e) => e.kind === "refusal" && e.tool === "core");
  const refusals = entries.filter((e) => e.kind === "refusal" && e.tool === "command").map((e) => e.at);
  let n = 0;
  for (const e of entries) {
    if (e.kind === "refusal") n++;
    else if (e.kind === "tool" && e.isError) {
      if (core && isFactoryTool(e.tool)) continue;
      const i = e.tool === "Bash" ? refusals.findIndex((r) => Math.abs(e.at - r) < 10_000) : -1;
      if (i >= 0) { refusals.splice(i, 1); continue; }
      n++;
    }
  }
  return n;
}

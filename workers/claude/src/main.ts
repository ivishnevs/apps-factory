import { existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { SettingSource } from "@anthropic-ai/claude-agent-sdk";
import { Bus } from "./bus.js";
import { CoreAPI } from "./core.js";
import { Worker } from "./worker.js";

const log = (msg: string, fields: Record<string, unknown> = {}) =>
  process.stderr.write(JSON.stringify({ time: new Date().toISOString(), msg, ...fields }) + "\n");

async function main() {
  const token = process.env["FACTORY_TOKEN"];
  if (!token) throw new Error("FACTORY_TOKEN required");
  // dist/src/main.js → repository bin/ next to the Go executables.
  const bin = process.env["FACTORY_BIN_DIR"] ?? resolve(dirname(fileURLToPath(import.meta.url)), "../../../../bin");
  const helper = join(bin, "factory-workspace");
  const mcp = join(bin, "factory-mcp");
  for (const p of [helper, mcp]) {
    if (!existsSync(p)) throw new Error(`${p} required; run make build`);
  }
  const sources = process.env["CLAUDE_SETTING_SOURCES"];
  const bus = await Bus.connect(process.env["NATS_URL"] ?? "nats://127.0.0.1:4222");
  const stop = new AbortController();
  for (const s of ["SIGINT", "SIGTERM"] as const) process.once(s, () => stop.abort());
  const worker = new Worker({
    bus,
    api: new CoreAPI(process.env["FACTORY_URL"] ?? "http://127.0.0.1:8080", token),
    workspaceHelper: helper,
    mcpCommand: mcp,
    executable: process.env["CLAUDE_CODE_EXECUTABLE"] || undefined,
    allowApiKey: process.env["CLAUDE_ALLOW_API_KEY"] === "1",
    settingSources: sources === undefined ? undefined : (sources.split(",").filter(Boolean) as SettingSource[]),
    log,
  });
  log("claude worker started");
  try {
    await worker.run(stop.signal);
  } finally {
    await bus.close();
  }
}

main().catch((err) => {
  log("worker stopped", { error: String(err) });
  process.exit(1);
});

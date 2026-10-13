// Workspace allocation through bin/factory-workspace, sharing the Go Worker's canonical
// flock and OpenSpec preflight. The helper holds the lock until its stdin closes.
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";

export class WorkspaceError extends Error {
  constructor(readonly kind: string, message: string) {
    super(message);
  }
}

export interface Allocation {
  path: string;
  // Resolves if the helper exits while the run still owns the workspace.
  lost: Promise<void>;
  release(): Promise<void>;
}

export async function acquire(helper: string, workspace: string, openspecChange?: string): Promise<Allocation> {
  const args = openspecChange ? ["-openspec-change", openspecChange, workspace] : [workspace];
  const child = spawn(helper, args, { stdio: ["pipe", "pipe", "inherit"] });
  const exited = new Promise<void>((resolve) => child.once("exit", () => resolve()));
  const spawnError = new Promise<never>((_, reject) => child.once("error", reject));
  const lines = createInterface({ input: child.stdout! });
  const first = new Promise<string>((resolve, reject) => {
    lines.once("line", resolve);
    lines.once("close", () => reject(new WorkspaceError("workspace", "factory-workspace exited without a reply")));
  });
  const reply = JSON.parse(await Promise.race([first, spawnError])) as { path?: string; kind?: string; error?: string };
  if (!reply.path) {
    await exited;
    throw new WorkspaceError(reply.kind ?? "workspace", reply.error ?? "workspace allocation failed");
  }
  let released = false;
  return {
    path: reply.path,
    lost: exited.then(() => {
      if (released) return new Promise<void>(() => {});
    }),
    async release() {
      released = true;
      child.stdin!.end();
      await exited;
    },
  };
}

import assert from "node:assert/strict";
import { test } from "node:test";
import { sandboxOptions } from "../src/runtime.js";

// connector-trust: a confined run's Bash writes only in its worktree, and no tool reads the denied paths.
test("sandbox options confine writes and deny reads", () => {
  const o = sandboxOptions({ allow_write: ["/w/task", "/tmp"], deny_read: ["/home/u/.ssh"] });
  const sb = o.sandbox as { enabled: boolean; autoAllowBashIfSandboxed: boolean; filesystem: { allowWrite: string[]; denyRead: string[] } };
  assert.equal(sb.enabled, true);
  assert.equal(sb.autoAllowBashIfSandboxed, true);
  assert.deepEqual(sb.filesystem.allowWrite, ["/w/task", "/tmp"]);
  assert.deepEqual(sb.filesystem.denyRead, ["/home/u/.ssh"]);
  const deny = (o.settings as { permissions: { deny: string[] } }).permissions.deny;
  assert.ok(deny.includes("Read(/home/u/.ssh/**)") && deny.includes("Edit(/home/u/.ssh/**)"));
});

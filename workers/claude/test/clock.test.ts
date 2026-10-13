import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { test } from "node:test";
import { AwakeClock, clock } from "../src/clock.js";

// The OS clock process.hrtime (and so every Node timer, through uv_now) reads, and its
// sleep-including / sleep-excluding counterpart, by Python time module name.
const OS_CLOCKS: Record<string, { hrtime: string; other: string; includesSleep: boolean }> = {
  // libuv darwin.c: mach_continuous_time() == CLOCK_MONOTONIC_RAW; CLOCK_UPTIME_RAW excludes sleep.
  darwin: { hrtime: "CLOCK_MONOTONIC_RAW", other: "CLOCK_UPTIME_RAW", includesSleep: true },
  // libuv linux.c: CLOCK_MONOTONIC(_COARSE); CLOCK_BOOTTIME includes suspend.
  linux: { hrtime: "CLOCK_MONOTONIC", other: "CLOCK_BOOTTIME", includesSleep: false },
};

const hrNs = () => process.hrtime.bigint();

test("hrtime source per platform is pinned and the clock compensates where it includes sleep", (t) => {
  const os = OS_CLOCKS[process.platform];
  if (!os) return t.skip(`no pinned clock for ${process.platform}`);
  // Sleep-including timers must be compensated; Linux timers already exclude suspend.
  assert.equal(clock.detectGaps, os.includesSleep);
  const before = hrNs();
  const py = spawnSync("python3", ["-c", `import time;print(time.clock_gettime_ns(time.${os.hrtime}), time.clock_gettime_ns(time.${os.other}))`], {
    encoding: "utf8",
  });
  const after = hrNs();
  if (py.error || py.status !== 0) return t.skip("python3 unavailable to read OS clocks");
  const [same, other] = py.stdout.trim().split(" ").map(BigInt) as [bigint, bigint];
  // process.hrtime and the pinned OS clock share origin and rate: the reading falls inside the window.
  assert.ok(before <= same && same <= after, `${os.hrtime}=${same} outside hrtime window [${before}, ${after}]`);
  // When the host has slept since boot the other clock differs and must not match hrtime.
  const slept = other > same ? other - same : same - other;
  if (slept > 1_000_000_000n) assert.ok(other < before || other > after, `hrtime unexpectedly follows ${os.other}`);
  t.diagnostic(`${process.platform}: hrtime = ${os.hrtime}; ${os.other} differs by ${slept / 1_000_000n} ms`);
});

test("gaps between observations are not counted when compensating", () => {
  let hr = 0;
  const c = new AwakeClock({ hrtime: () => hr, detectGaps: true, gapMs: 5_000 });
  hr += 1_000;
  assert.equal(c.now(), 1_000);
  hr += 3_600_000; // system sleep
  assert.equal(c.now(), 1_000);
  hr += 500;
  assert.equal(c.now(), 1_500);
  const linux = new AwakeClock({ hrtime: () => hr, detectGaps: false });
  hr += 3_600_000;
  assert.equal(linux.now(), 3_600_000);
});

test("timeout does not fire for time spent asleep", async () => {
  let offset = 0;
  const real = () => Number(process.hrtime.bigint() / 1_000_000n);
  const c = new AwakeClock({ hrtime: () => real() + offset, detectGaps: true, tickMs: 10, gapMs: 1_000 });
  const start = real();
  let firedAt = 0;
  const fired = new Promise<void>((resolve) =>
    c.timeout(200, () => {
      firedAt = real() - start;
      resolve();
    }),
  );
  await new Promise((r) => setTimeout(r, 100));
  offset += 3_600_000; // the host slept for an hour: a sleep-including timer would be overdue
  await fired;
  // Counting the hour would fire right after the jump (~100 ms); awake time alone reaches 200 ms.
  assert.ok(firedAt >= 190, `fired after ${firedAt} ms; sleep was counted`);
});

test("interval and cancel", async () => {
  const c = new AwakeClock();
  let n = 0;
  const timer = c.interval(10, () => n++);
  await new Promise((r) => setTimeout(r, 75));
  timer.cancel();
  const seen = n;
  assert.ok(seen >= 3, `interval fired ${seen} times`);
  await new Promise((r) => setTimeout(r, 40));
  assert.equal(n, seen);
  let fired = false;
  c.timeout(10, () => (fired = true)).cancel();
  await c.sleep(30);
  assert.equal(fired, false);
});

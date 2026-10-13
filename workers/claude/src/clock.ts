// Sleep-excluding clock for run timeouts, heartbeats and runtime deadlines (reliable-recovery 3.4).
//
// Node timers (setTimeout/setInterval, AbortSignal.timeout) are scheduled on libuv's uv_now(),
// which reads uv__hrtime(), the same source as process.hrtime() and performance.now():
//   - Linux: clock_gettime(CLOCK_MONOTONIC, or CLOCK_MONOTONIC_COARSE for uv_now). It stops while
//     the system is suspended, so Node timers already exclude sleep.
//   - macOS: mach_continuous_time() (libuv ≥ 1.44, every Node ≥ 22). It keeps counting during sleep,
//     so after a wake every timer whose deadline passed while asleep fires at once.
// AwakeClock uses process.hrtime on Linux as is. Elsewhere a ticker observes hrtime every second
// while timers are pending; a jump above gapMs between two observations can only be a suspension
// (or an event loop stalled that long, which this I/O-only process does not do) and is not counted.
// Up to one tick of awake time around each sleep is lost, so deadlines err on the late side.
// test/clock.test.ts pins the per-platform hrtime source against the OS clocks.

export interface Timer {
  cancel(): void;
}

export interface ClockOptions {
  // Monotonic milliseconds; process.hrtime by default (injectable for tests).
  hrtime?: () => number;
  // Subtract gaps between observations; true on every platform except Linux.
  detectGaps?: boolean;
  tickMs?: number;
  gapMs?: number;
}

const hrtimeMs = () => Number(process.hrtime.bigint() / 1_000n) / 1_000;

export class AwakeClock {
  readonly detectGaps: boolean;
  private readonly hr: () => number;
  private readonly tickMs: number;
  private readonly gapMs: number;
  private last: number;
  private awake = 0;
  private pending = 0;
  private ticker: NodeJS.Timeout | undefined;

  constructor(opts: ClockOptions = {}) {
    this.hr = opts.hrtime ?? hrtimeMs;
    this.detectGaps = opts.detectGaps ?? process.platform !== "linux";
    this.tickMs = opts.tickMs ?? 1_000;
    this.gapMs = opts.gapMs ?? 5_000;
    this.last = this.hr();
  }

  // Milliseconds the system has been awake, from an arbitrary origin.
  now(): number {
    const t = this.hr();
    const d = t - this.last;
    this.last = t;
    if (!this.detectGaps || d <= this.gapMs) this.awake += Math.max(0, d);
    return this.awake;
  }

  // Runs fn once after ms of awake time.
  timeout(ms: number, fn: () => void): Timer {
    let native: NodeJS.Timeout | undefined;
    let done = false;
    this.hold();
    const due = this.now() + ms;
    const arm = () => {
      native = setTimeout(() => {
        const left = due - this.now();
        if (left > 0) return arm();
        done = true;
        this.release();
        fn();
      }, Math.max(0, due - this.now()));
    };
    arm();
    return {
      cancel: () => {
        clearTimeout(native);
        if (!done) {
          done = true;
          this.release();
        }
      },
    };
  }

  // Runs fn every ms of awake time until cancelled.
  interval(ms: number, fn: () => void): Timer {
    let current: Timer | undefined;
    let cancelled = false;
    const next = () => {
      current = this.timeout(ms, () => {
        if (cancelled) return;
        next();
        fn();
      });
    };
    next();
    return {
      cancel: () => {
        cancelled = true;
        current?.cancel();
      },
    };
  }

  // Resolves after ms of awake time.
  sleep(ms: number): Promise<void> {
    return new Promise((resolve) => this.timeout(ms, resolve));
  }

  private hold() {
    if (this.pending++ > 0 || !this.detectGaps) return;
    // Observations must be at most tickMs apart while awake, otherwise awake time looks like a gap.
    this.now();
    this.ticker = setInterval(() => this.now(), this.tickMs);
    this.ticker.unref();
  }

  private release() {
    if (--this.pending > 0) return;
    clearInterval(this.ticker);
    this.ticker = undefined;
  }
}

export const clock = new AwakeClock();

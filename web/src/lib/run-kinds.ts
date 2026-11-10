// Work and conversation runs (work-and-conversation-runs): the agent fields the editor validates as Core does
// (internal/config/window.go, config.MaxConcurrentRuns), so an error shows next to its field before saving.

export const MIN_CONTEXT_WINDOW = 100_000;
export const MAX_CONTEXT_WINDOW = 1_000_000;
export const MAX_CONCURRENT_RUNS = 8;

// parseWindow reads 200000, 200k, 1M or max: tokens, 0 for max or empty (the runtime's default), null when invalid.
export function parseWindow(s: string | undefined): number | null {
  let t = (s ?? "").trim();
  if (t === "" || t.toLowerCase() === "max") return 0;
  let mult = 1;
  if (/[kK]$/.test(t)) { mult = 1000; t = t.slice(0, -1); } else if (/[mM]$/.test(t)) { mult = 1_000_000; t = t.slice(0, -1); }
  if (!/^\s*\d+(\.\d+)?\s*$/.test(t)) return null;
  const n = Math.trunc(parseFloat(t) * mult);
  return n > 0 ? n : null;
}

// windowError is why a window value is refused (null when it is fine).
export function windowError(s: string | undefined): string | null {
  const n = parseWindow(s);
  if (n === null) return "Tokens (200000, 200k, 1M) or max";
  if (n !== 0 && (n < MIN_CONTEXT_WINDOW || n > MAX_CONTEXT_WINDOW)) return "Between 100k and 1M tokens, or max";
  return null;
}

// runsError checks a runs-at-a-time input: empty is allowed (inherit or no cap), else min..MAX_CONCURRENT_RUNS.
export function runsError(s: string, min: number): string | null {
  if (s.trim() === "") return null;
  const n = Number(s);
  if (!Number.isInteger(n) || n < min || n > MAX_CONCURRENT_RUNS) return `A whole number from ${min} to ${MAX_CONCURRENT_RUNS}`;
  return null;
}

// windowLabel shows a window for reading: "max" or "200k" as written, "runtime default" when unset.
export function windowLabel(s: string | undefined): string {
  return s && s.trim() ? s.trim() : "runtime default";
}

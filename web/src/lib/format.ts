import { formatDistanceToNowStrict } from "date-fns";

// ago: "3m ago" from Unix seconds or an RFC 3339 string.
export function ago(t?: number | string): string {
  if (!t) return "";
  const d = typeof t === "number" ? new Date(t * 1000) : new Date(t);
  return Number.isNaN(d.getTime()) ? "" : formatDistanceToNowStrict(d, { addSuffix: true });
}
export const usd = (n?: number) => (n ?? 0) < 0.01 && (n ?? 0) > 0 ? "<$0.01" : `$${(n ?? 0).toFixed(2)}`;
export const tokens = (n?: number) => {
  const v = n ?? 0;
  return v >= 1e6 ? `${(v / 1e6).toFixed(1)}M` : v >= 1e3 ? `${(v / 1e3).toFixed(1)}k` : String(v);
};
export const shortId = (id?: string) => (id ?? "").slice(0, 8);

import { differenceInCalendarDays, format, isToday, isYesterday } from "date-fns";

// Messenger-style times from Unix seconds: "14:05" today, "Mon" this week, "Oct 20" before, "Oct 20, 2025" in
// another year.
export function listTime(t?: number) {
  if (!t) return "";
  const d = new Date(t * 1000);
  if (isToday(d)) return format(d, "HH:mm");
  if (isYesterday(d)) return "Yesterday";
  if (differenceInCalendarDays(new Date(), d) < 7) return format(d, "EEE");
  return format(d, d.getFullYear() === new Date().getFullYear() ? "MMM d" : "MMM d, yyyy");
}

export const clock = (t: number) => format(new Date(t * 1000), "HH:mm");

export function dayLabel(t: number) {
  const d = new Date(t * 1000);
  if (isToday(d)) return "Today";
  if (isYesterday(d)) return "Yesterday";
  return format(d, d.getFullYear() === new Date().getFullYear() ? "EEEE, MMM d" : "EEEE, MMM d, yyyy");
}

export const dayKey = (t: number) => format(new Date(t * 1000), "yyyy-MM-dd");

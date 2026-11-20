import type { HistoryEntry } from "./api/types";

const dayKey = (d: Date) => `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;

export function dayLabel(d: Date, now = new Date()) {
  if (dayKey(d) === dayKey(now)) return "Today";
  const yesterday = new Date(now);
  yesterday.setDate(yesterday.getDate() - 1);
  if (dayKey(d) === dayKey(yesterday)) return "Yesterday";
  return d.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "long" });
}

export interface Day {
  key: string;
  label: string;
  entries: HistoryEntry[];
}

// Sorted newest first so a day is a run of neighbours, whatever order the pages arrived in.
export function byDay(entries: HistoryEntry[], now = new Date()): Day[] {
  const days: Day[] = [];
  for (const h of [...entries].sort((a, b) => b.at.localeCompare(a.at))) {
    const at = new Date(h.at);
    const key = dayKey(at);
    const last = days[days.length - 1];
    if (last?.key === key) last.entries.push(h);
    else days.push({ key, label: dayLabel(at, now), entries: [h] });
  }
  return days;
}

export function summaryOf(c: { downloading: number; waiting: number; retryable: number }) {
  const parts = [c.downloading > 0 && `${c.downloading} downloading`, c.waiting > 0 && `${c.waiting} waiting`, c.retryable > 0 && `${c.retryable} can be retried`].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : "All quiet.";
}

// How many 380px cards (plus the waiting posters, two columns once there are more than two) the left column is sized for.
export const galleryColumns = (running: number, waiting: number) => Math.max(running, waiting > 2 ? 2 : 1);

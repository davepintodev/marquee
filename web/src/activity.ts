import type { Download, HistoryEntry, TitleSummary } from "./api/types";
import { isWait, type WaitState } from "./words";

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
  for (const h of [...entries].sort((a, b) => Date.parse(b.at) - Date.parse(a.at))) {
    const at = new Date(h.at);
    const key = dayKey(at);
    const last = days[days.length - 1];
    if (last?.key === key) last.entries.push(h);
    else days.push({ key, label: dayLabel(at, now), entries: [h] });
  }
  return days;
}

export function summaryOf(c: { downloading: number; paused: number; waiting: number; retryable: number; allPaused?: boolean }) {
  const parts = [
    c.allPaused ? "Downloads paused" : c.downloading > 0 && `${c.downloading} downloading`,
    !c.allPaused && c.paused > 0 && `${c.paused} paused`,
    c.waiting > 0 && `${c.waiting} waiting`,
    c.retryable > 0 && `${c.retryable} can be retried`,
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : "All quiet.";
}

// A title that waits for a release: its wait word, and the episodes it waits for (none for a movie).
export interface Waiter {
  id: string;
  name: string;
  label: string;
  state: WaitState;
  episodes: string[];
}

export function waitersOf(library: TitleSummary[]): Waiter[] {
  return library.flatMap((t): Waiter[] => {
    const episodes = t.searchingEpisodes ?? [];
    const state = t.badge && isWait(t.badge.state) ? t.badge.state : null;
    if (episodes.length === 0 && !state) return [];
    return [{ id: t.id, name: t.title, label: state ? (t.badge?.label ?? "Wanted") : "Wanted", state: state ?? "wanted", episodes }];
  });
}

// Everything one title has going on: its unfinished downloads (queued ones too) and how many of its
// episodes wait without one. A title with no download is a group that only waits.
export interface Group {
  id: string;
  name: string;
  downloads: Download[];
  waiting: number;
  wait?: Waiter;
}

export function groupsOf(downloads: Download[], waiters: Waiter[]): Group[] {
  const groups = new Map<string, Group>();
  for (const d of downloads) {
    const g = groups.get(d.titleId) ?? { id: d.titleId, name: d.titleName, downloads: [], waiting: 0 };
    g.downloads.push(d);
    groups.set(d.titleId, g);
  }
  for (const g of groups.values()) {
    const have = new Set(g.downloads.map((d) => d.episode));
    const w = waiters.find((x) => x.id === g.id);
    g.waiting = g.downloads.filter((d) => d.state === "queued").length + (w?.episodes.filter((e) => !have.has(e)).length ?? 0);
  }
  for (const w of waiters) {
    if (!groups.has(w.id)) groups.set(w.id, { id: w.id, name: w.name, downloads: [], waiting: Math.max(1, w.episodes.length), wait: w });
  }
  return [...groups.values()];
}

export const runningOf = (g: Group) => g.downloads.filter((d) => d.state !== "queued");

const SHOWN = 3;

const sayOf = (d: Download) => (d.stalled ? "stalled" : d.state === "downloading" ? `${d.percent}%` : d.state);
const upper = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

// "S01E01 86% · S01E02 82% · 3 paused · 22 waiting": what runs, then how many are held, then how many wait behind it.
export function groupSummary(g: Group): string {
  if (g.downloads.length === 0 && g.wait) {
    const eps = g.wait.episodes;
    const what = eps.length === 0 ? "" : eps.length <= SHOWN ? eps.map((e) => e.toUpperCase()).join(", ") : `${eps.length} episodes`;
    return what ? `${g.wait.label} · ${what}` : g.wait.label;
  }
  const running = runningOf(g);
  if (running.length === 0) {
    const queued = g.downloads.length;
    const extra = g.waiting - queued;
    const first = g.downloads[0];
    const head = queued === 1 ? (first.episode ? `${first.episode.toUpperCase()} queued` : "Queued") : `${queued} queued`;
    return extra > 0 ? `${head} · ${extra} waiting` : head;
  }
  const held = running.filter((d) => d.state === "paused");
  const active = running.filter((d) => d.state !== "paused");
  const parts = active.slice(0, SHOWN).map((d) => {
    if (!d.episode) return d.state === "downloading" && !d.stalled ? `Downloading ${d.percent}%` : upper(sayOf(d));
    return `${d.episode.toUpperCase()} ${sayOf(d)}`;
  });
  if (active.length > SHOWN) parts.push(`+${active.length - SHOWN} more`);
  if (held.length > 0) parts.push(`${held.length} paused`);
  if (g.waiting > 0) parts.push(`${g.waiting} waiting`);
  return parts.join(" · ");
}

export type GroupState = "downloading" | "caching" | "paused" | "stalled" | "verifying" | "queued";

export function groupState(g: Group): GroupState {
  const running = runningOf(g);
  if (running.length === 0) return "queued";
  if (running.every((d) => d.state === "paused")) return "paused";
  if (running.some((d) => d.state === "downloading" && !d.stalled)) return "downloading";
  if (running.some((d) => d.state === "caching")) return "caching";
  if (running.some((d) => d.state === "downloading")) return "stalled";
  return "verifying";
}

export function groupPercent(g: Group): number {
  if (g.downloads.length === 0) return 0;
  return Math.round(g.downloads.reduce((sum, d) => sum + d.percent, 0) / g.downloads.length);
}

// Verifying and importing take no bandwidth and can no longer be stopped; the server refuses them with a 409.
export const settling = (d: Download) => d.state === "verifying" || d.state === "importing";
export const pausable = (d: Download) => d.state === "queued" || d.state === "caching" || d.state === "downloading";

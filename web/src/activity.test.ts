import { expect, test } from "vitest";
import type { Download, HistoryEntry, TitleSummary } from "./api/types";
import { byDay, dayLabel, groupPercent, groupState, groupSummary, groupsOf, summaryOf, waitersOf } from "./activity";

const at = (day: number, hour = 12, month = 10) => new Date(2026, month, day, hour, 0);
const now = at(18);

test.each([
  ["earlier the same day", at(18, 3), "Today"],
  ["late the day before", at(17, 23), "Yesterday"],
])("dayLabel names %s", (_name, d, want) => {
  expect(dayLabel(d, now)).toBe(want);
});

test("dayLabel calls yesterday across a month boundary", () => {
  expect(dayLabel(new Date(2026, 10, 30, 22), new Date(2026, 11, 1, 8))).toBe("Yesterday");
});

test("dayLabel spells out an older day with its weekday and date", () => {
  const label = dayLabel(at(14), now);
  expect(label).toContain("14");
  expect(label).not.toBe("Today");
  expect(label).not.toBe("Yesterday");
});

const entry = (id: string, when: Date): HistoryEntry => ({ id, at: when.toISOString(), kind: "searched", titleId: "t", titleName: "T", message: id, retryable: false });

test("byDay groups newest first, one group per day, whatever order the pages arrived in", () => {
  const days = byDay([entry("old", at(16, 9)), entry("b", at(18, 9)), entry("a", at(18, 11)), entry("y", at(17, 20))], now);
  expect(days.map((d) => [d.label, d.entries.map((e) => e.id)])).toEqual([
    ["Today", ["a", "b"]],
    ["Yesterday", ["y"]],
    [dayLabel(at(16), now), ["old"]],
  ]);
});

test("byDay orders by the instant, not by the timestamp text", () => {
  const stamped = (id: string, text: string): HistoryEntry => ({ ...entry(id, now), at: text });
  const days = byDay([stamped("plain", "2026-11-18T20:00:00Z"), stamped("fraction", "2026-11-18T20:00:00.500Z")], now);
  expect(days[0].entries.map((e) => e.id)).toEqual(["fraction", "plain"]);
});

test("byDay of nothing is nothing", () => {
  expect(byDay([], now)).toEqual([]);
});

test.each([
  [{ downloading: 0, waiting: 0, retryable: 0 }, "All quiet."],
  [{ downloading: 2, waiting: 3, retryable: 1 }, "2 downloading · 3 waiting · 1 can be retried"],
  [{ downloading: 0, waiting: 1, retryable: 0 }, "1 waiting"],
  [{ downloading: 1, waiting: 0, retryable: 4 }, "1 downloading · 4 can be retried"],
])("summaryOf %j", (counts, want) => {
  expect(summaryOf(counts)).toBe(want);
});

const dl = (id: string, over: Partial<Download> = {}): Download => ({
  id,
  titleId: "series-1",
  titleName: "Friends",
  episode: id,
  releaseName: `Friends.${id}`,
  state: "downloading",
  percent: 0,
  bytesDone: 0,
  bytesTotal: 100,
  stalled: false,
  bytesPerSecond: 0,
  ...over,
});

const lib = (over: Partial<TitleSummary> & Pick<TitleSummary, "id" | "title">): TitleSummary => ({ tmdbId: 1, kind: "series", year: 2020, overview: "", posterPath: "", backdropPath: "", inLibrary: true, ...over });

test("groupsOf makes one group per title, in the order its first download comes, with queued ones and waiting episodes counted as waiting", () => {
  const downloads = [
    dl("s01e01", { percent: 86 }),
    dl("s01e02", { percent: 82 }),
    dl("s01e03", { state: "queued" }),
    dl("s01e04", { state: "queued" }),
    dl("m", { titleId: "movie-1", titleName: "Dune", episode: undefined, percent: 43 }),
  ];
  const waiters = waitersOf([lib({ id: "series-1", title: "Friends", searchingEpisodes: ["s01e03", "s01e05", "s01e06"], badge: { state: "wanted", label: "Wanted" } })]);

  const groups = groupsOf(downloads, waiters);

  expect(groups.map((g) => [g.id, g.downloads.length, g.waiting])).toEqual([
    ["series-1", 4, 4],
    ["movie-1", 1, 0],
  ]);
});

test("a title that only waits is a group without downloads, counted by its episodes or as one", () => {
  const waiters = waitersOf([
    lib({ id: "series-2", title: "House", searchingEpisodes: ["s04e02", "s04e03"], badge: { state: "not_found", label: "Not found" } }),
    lib({ id: "movie-9", title: "Heat", kind: "movie", badge: { state: "searching", label: "Searching" } }),
    lib({ id: "movie-8", title: "Done", kind: "movie", badge: { state: "downloaded", label: "Downloaded" } }),
  ]);

  const groups = groupsOf([], waiters);

  expect(groups.map((g) => [g.name, g.downloads.length, g.waiting])).toEqual([
    ["House", 0, 2],
    ["Heat", 0, 1],
  ]);
});

test.each([
  ["a running season", [dl("s01e01", { percent: 86 }), dl("s01e02", { percent: 82 }), dl("s01e03", { state: "queued" }), dl("s01e04", { state: "queued" })], 2, "S01E01 86% · S01E02 82% · 4 waiting"],
  ["a paused and a caching episode", [dl("s01e01", { state: "paused", percent: 40 }), dl("s01e02", { state: "caching", percent: 10 })], 0, "S01E01 paused · S01E02 caching"],
  ["a stalled and a verifying episode", [dl("s01e01", { stalled: true, percent: 12 }), dl("s01e02", { state: "verifying", percent: 100 })], 0, "S01E01 stalled · S01E02 verifying"],
  ["more running than fit", ["s01e01", "s01e02", "s01e03", "s01e04", "s01e05"].map((id) => dl(id, { percent: 50 })), 0, "S01E01 50% · S01E02 50% · S01E03 50% · +2 more"],
  ["a movie", [dl("m", { episode: undefined, percent: 43, titleId: "movie-1", titleName: "Dune" })], 0, "Downloading 43%"],
  ["a paused movie", [dl("m", { episode: undefined, state: "paused", percent: 43, titleId: "movie-1", titleName: "Dune" })], 0, "Paused"],
  ["only queued episodes", [dl("s01e01", { state: "queued" }), dl("s01e02", { state: "queued" })], 0, "2 queued"],
  ["one queued episode", [dl("s01e04", { state: "queued" })], 0, "S01E04 queued"],
  ["a queued movie", [dl("m", { episode: undefined, state: "queued", titleId: "movie-1", titleName: "Dune" })], 0, "Queued"],
])("groupSummary of %s", (_name, downloads, extra, want) => {
  const waiters = extra > 0 ? waitersOf([lib({ id: "series-1", title: "Friends", searchingEpisodes: Array.from({ length: extra }, (_, i) => `s09e${i + 1}`), badge: { state: "wanted", label: "Wanted" } })]) : [];
  const [g] = groupsOf(downloads, waiters);
  expect(groupSummary(g)).toBe(want);
});

test("groupSummary of a title that only waits says what it waits for", () => {
  const few = groupsOf([], waitersOf([lib({ id: "series-2", title: "House", searchingEpisodes: ["s04e02", "s04e03"], badge: { state: "not_found", label: "Not found" } })]))[0];
  const many = groupsOf([], waitersOf([lib({ id: "series-2", title: "House", searchingEpisodes: ["s04e02", "s04e03", "s04e04", "s04e05"], badge: { state: "wanted", label: "Wanted" } })]))[0];
  const movie = groupsOf([], waitersOf([lib({ id: "movie-9", title: "Heat", kind: "movie", badge: { state: "searching", label: "Searching" } })]))[0];
  expect(groupSummary(few)).toBe("Not found · S04E02, S04E03");
  expect(groupSummary(many)).toBe("Wanted · 4 episodes");
  expect(groupSummary(movie)).toBe("Searching");
});

test("groupState is the state of what runs: paused only when all of it is, stalled when nothing else moves", () => {
  const state = (...ds: Download[]) => groupState(groupsOf(ds, [])[0]);
  expect(state(dl("a"), dl("b", { state: "paused" }))).toBe("downloading");
  expect(state(dl("a", { state: "paused" }), dl("b", { state: "paused" }), dl("c", { state: "queued" }))).toBe("paused");
  expect(state(dl("a", { stalled: true }), dl("b", { state: "caching" }))).toBe("caching");
  expect(state(dl("a", { stalled: true }), dl("b", { state: "paused" }))).toBe("stalled");
  expect(state(dl("a", { state: "verifying" }))).toBe("verifying");
  expect(state(dl("a", { state: "queued" }))).toBe("queued");
});

test("groupPercent is the mean of every episode, queued ones counting as zero", () => {
  const g = groupsOf([dl("a", { percent: 80 }), dl("b", { percent: 40 }), dl("c", { state: "queued" }), dl("d", { state: "queued" })], [])[0];
  expect(groupPercent(g)).toBe(30);
});

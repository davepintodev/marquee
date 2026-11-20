import { expect, test } from "vitest";
import type { HistoryEntry } from "./api/types";
import { byDay, dayLabel, galleryColumns, summaryOf } from "./activity";

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

test.each([
  [0, 0, 1],
  [1, 0, 1],
  [3, 0, 3],
  [0, 2, 1],
  [0, 3, 2],
  [1, 9, 2],
  [4, 9, 4],
])("galleryColumns(running %i, waiting %i) is %i", (running, waiting, want) => {
  expect(galleryColumns(running, waiting)).toBe(want);
});

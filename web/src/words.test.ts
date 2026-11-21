import { expect, test } from "vitest";
import { ago, bytesOf, inHours, reasonLabel, seasonBadgeLabel, stateLabel } from "./words";

const NOW = new Date("2026-11-18T12:00:00Z");

test("every failure reason has a label in words", () => {
  expect(reasonLabel("stalled")).toBe("Stalled");
  expect(reasonLabel("not_video")).toBe("Fake file");
  expect(reasonLabel("wrong_episode")).toBe("Wrong episode");
  expect(reasonLabel("too_small")).toBe("Too small");
  expect(reasonLabel("size_mismatch")).toBe("Wrong size");
  expect(reasonLabel("source_down")).toBe("Source down");
  expect(reasonLabel("local_error")).toBe("Local error");
  expect(reasonLabel(undefined)).toBe("Failed");
});

test("a future time reads as the time left, rounded like the history sentence", () => {
  const at = (ms: number) => new Date(NOW.getTime() + ms).toISOString();
  expect(inHours(at(6 * 3600_000), NOW)).toBe("6h");
  expect(inHours(at(5.6 * 3600_000), NOW)).toBe("6h");
  expect(inHours(at(30 * 60_000), NOW)).toBe("30m");
  expect(inHours(at(20_000), NOW)).toBe("1m");
  expect(inHours(at(-5_000), NOW)).toBe("moments");
});

test("a past time reads as how long ago, switching to a short date after 7 days", () => {
  const before = (ms: number) => new Date(NOW.getTime() - ms).toISOString();
  expect(ago(before(20_000), NOW)).toBe("just now");
  expect(ago(before(5 * 60_000), NOW)).toBe("5 min ago");
  expect(ago(before(3 * 3600_000), NOW)).toBe("3 h ago");
  expect(ago(before(30 * 3600_000), NOW)).toBe("1 day ago");
  expect(ago(before(4 * 86400_000), NOW)).toBe("4 days ago");
  expect(ago(before(7 * 86400_000), NOW)).toBe("7 days ago");
  const tenDaysAgo = before(10 * 86400_000);
  expect(ago(tenDaysAgo, NOW)).toBe(new Date(tenDaysAgo).toLocaleDateString(undefined, { month: "short", day: "numeric" }));
  const lastYear = "2025-10-05T12:00:00Z";
  expect(ago(lastYear, NOW)).toBe(new Date(lastYear).toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" }));
});

test("sizes read in the biggest unit that keeps a number small", () => {
  expect(bytesOf(0)).toBe("0 B");
  expect(bytesOf(1536)).toBe("1.5 KB");
  expect(bytesOf(9_234_179_072)).toBe("8.6 GB");
  expect(bytesOf(21_474_836_480)).toBe("20 GB");
});

test("an episode's state reads in words, with the percent while it downloads", () => {
  expect(stateLabel({ state: "downloading", percent: 40 })).toBe("Downloading 40%");
  expect(stateLabel({ state: "unaired" })).toBe("Not aired yet");
  expect(stateLabel({ state: "missing" })).toBe("Missing");
});

test("a season row's badge drops the season name the row already shows", () => {
  expect(seasonBadgeLabel({ state: "partial_season", label: "Season 4 · 3 missing", season: 4, missingCount: 3 })).toBe("3 missing");
  expect(seasonBadgeLabel({ state: "partial_season", label: "Season 4 · 1 missing", season: 4, missingCount: 1 })).toBe("1 missing");
  expect(seasonBadgeLabel({ state: "downloaded", label: "Downloaded" })).toBe("Downloaded");
  expect(seasonBadgeLabel({ state: "searching", label: "Searching" })).toBe("Searching");
  expect(seasonBadgeLabel({ state: "downloading", label: "Downloading 43%", percent: 43 })).toBe("Downloading 43%");
});

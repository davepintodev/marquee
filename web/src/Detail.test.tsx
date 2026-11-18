// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { Title } from "./api/types";
import { FakeEventSource, json, setupApp } from "./testkit";

const base = {
  id: "series-2",
  tmdbId: 2,
  kind: "series" as const,
  title: "House",
  year: 2004,
  overview: "A diagnostician.",
  posterPath: "/api/images/poster/series-2",
  backdropPath: "/api/images/backdrop/series-2",
  inLibrary: true,
  monitored: true,
};

function house(over: Partial<Title> = {}): Title {
  return {
    ...base,
    badge: { state: "partial_season", label: "Season 4 · 2 missing", season: 4, missingCount: 2 },
    preferredQuality: null,
    nextSearchAt: "2026-11-18T18:00:00Z",
    historySummary: "Tried 3 releases, 1 stalled, 2 fake, retrying in 6h.",
    seasons: [
      {
        number: 3,
        badge: { state: "downloaded", label: "Downloaded" },
        episodes: [{ id: "s03e01", number: 1, name: "Meaning", airDate: "2006-09-05", monitored: true, state: "downloaded" }],
      },
      {
        number: 4,
        badge: { state: "partial_season", label: "Season 4 · 2 missing", season: 4, missingCount: 2 },
        episodes: [
          { id: "s04e01", number: 1, name: "Alone", airDate: "2007-09-25", monitored: true, state: "downloaded" },
          { id: "s04e02", number: 2, name: "Big Baby", airDate: "2007-10-02", monitored: true, state: "missing", nextSearchAt: "2026-11-18T18:00:00Z" },
          { id: "s04e03", number: 3, name: "You Don't Want to Know", airDate: "2007-10-09", monitored: true, state: "downloading", percent: 40 },
          { id: "s04e04", number: 4, name: "Sex Kills", airDate: "2007-10-16", monitored: false, state: "stalled" },
        ],
      },
    ],
    history: [
      { id: "h2", at: "2026-11-18T06:10:00Z", kind: "failed", titleId: "series-2", titleName: "House", episode: "s04e02", message: "House.S04E02.720p.HDTV stalled: no data for 1 minute.", reason: "stalled", retryable: true },
      { id: "h1", at: "2026-11-17T21:05:00Z", kind: "imported", titleId: "series-2", titleName: "House", episode: "s04e01", message: "Imported House - S04E01 - Alone.", retryable: false },
    ],
    ...over,
  };
}

beforeEach(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  vi.useFakeTimers({ toFake: ["Date"], now: new Date("2026-11-18T12:00:00Z") });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
  window.location.hash = "";
});

function serve(title: Title, extra?: (url: URL, init: Request) => Response | undefined) {
  return (url: URL, init: Request) => {
    const special = extra?.(url, init);
    if (special) return special;
    if (url.pathname === "/api/wall") return json({ items: [] });
    if (url.pathname === `/api/titles/${title.id}` && init.method === "GET") return json(title);
    return json({ error: "nope" }, 404);
  };
}

test("a series opens with its seasons and episodes, each with its state, and its history in a sentence", async () => {
  setupApp(serve(house()), "#/title/series-2");

  expect(await screen.findByRole("heading", { name: "House", level: 1 })).toBeTruthy();
  expect(screen.getByText("Tried 3 releases, 1 stalled, 2 fake, retrying in 6h.")).toBeTruthy();

  const s4 = screen.getByRole("region", { name: "Season 4" });
  expect(within(s4).getByText("2 missing")).toBeTruthy();
  expect(within(s4).queryByText(/Season 4 ·/)).toBeNull();
  expect(screen.getAllByText("Season 4 · 2 missing")).toHaveLength(1);
  const rows = within(s4).getAllByRole("listitem");
  expect(rows).toHaveLength(4);
  expect(within(rows[0]).getByText("Downloaded")).toBeTruthy();
  expect(within(rows[1]).getByText("Missing")).toBeTruthy();
  expect(within(rows[1]).getByText(/next search in 6h/i)).toBeTruthy();
  expect(within(rows[2]).getByText("Downloading 40%")).toBeTruthy();
  expect(within(rows[3]).getByText("Stalled")).toBeTruthy();
  expect(within(rows[3]).getByText("Unmonitored")).toBeTruthy();
  expect(screen.getByRole("region", { name: "Season 3" })).toBeTruthy();

  const history = screen.getByRole("list", { name: "History" });
  expect(within(history).getByText(/stalled: no data for 1 minute/)).toBeTruthy();
  expect(within(history).getByText("Stalled")).toBeTruthy();
});

test("Search now on an episode and on the title skip the backoff", async () => {
  const { writes } = setupApp(
    serve(house(), (url, init) => {
      if (init.method === "POST" && url.pathname.endsWith("/search")) return json(house({ historySummary: "Searching now." }));
    }),
    "#/title/series-2",
  );
  const s4 = await screen.findByRole("region", { name: "Season 4" });

  fireEvent.click(within(s4).getByRole("button", { name: "Search now for Big Baby" }));
  await screen.findByText("Searching now.");
  fireEvent.click(screen.getByRole("button", { name: "Search now" }));

  await waitFor(() => expect(writes.map((w) => `${w.method} ${w.path}`)).toEqual([
    "POST /api/titles/series-2/episodes/s04e02/search",
    "POST /api/titles/series-2/search",
  ]));
});

test("an episode and the title can be unmonitored, and the page shows the answer", async () => {
  const unmonitored = house({ monitored: false });
  const { writes } = setupApp(
    serve(house(), (url, init) => {
      if (init.method === "PATCH" && url.pathname === "/api/titles/series-2/episodes/s04e02") {
        const t = house();
        t.seasons[1].episodes[1] = { ...t.seasons[1].episodes[1], monitored: false, nextSearchAt: null };
        return json(t);
      }
      if (init.method === "PATCH" && url.pathname === "/api/titles/series-2") return json(unmonitored);
    }),
    "#/title/series-2",
  );
  const s4 = await screen.findByRole("region", { name: "Season 4" });

  fireEvent.click(within(s4).getByRole("button", { name: "Unmonitor Big Baby" }));
  await within(s4).findByRole("button", { name: "Monitor Big Baby" });
  expect(within(within(s4).getAllByRole("listitem")[1]).getByText("Unmonitored")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Unmonitor" }));
  await screen.findByRole("button", { name: "Monitor" });

  expect(writes).toEqual([
    { method: "PATCH", path: "/api/titles/series-2/episodes/s04e02", body: { monitored: false } },
    { method: "PATCH", path: "/api/titles/series-2", body: { monitored: false } },
  ]);
});

test("removing a title asks first, says the files stay, and goes back to the wall", async () => {
  const { writes } = setupApp(
    serve(house(), (url, init) => (init.method === "DELETE" && url.pathname === "/api/titles/series-2" ? new Response(null, { status: 204 }) : undefined)),
    "#/title/series-2",
  );
  fireEvent.click(await screen.findByRole("button", { name: "Remove from marquee" }));

  expect(screen.getByText(/files stay on disk/i)).toBeTruthy();
  expect(writes).toEqual([]);
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(screen.queryByText(/files stay on disk/i)).toBeNull();

  fireEvent.click(screen.getByRole("button", { name: "Remove from marquee" }));
  fireEvent.click(screen.getByRole("button", { name: "Remove" }));

  await waitFor(() => expect(window.location.hash).toBe("#/"));
  expect(writes).toEqual([{ method: "DELETE", path: "/api/titles/series-2", body: undefined }]);
  expect(await screen.findByRole("status")).toBeTruthy();
});

test("a failed removal is said in words and the title stays", async () => {
  setupApp(
    serve(house(), (_url, init) => (init.method === "DELETE" ? json({ error: "that title is not in the library" }, 404) : undefined)),
    "#/title/series-2",
  );
  fireEvent.click(await screen.findByRole("button", { name: "Remove from marquee" }));
  fireEvent.click(screen.getByRole("button", { name: "Remove" }));

  expect((await screen.findByRole("alert")).textContent).toContain("that title is not in the library");
  expect(window.location.hash).toBe("#/title/series-2");
});

test("a movie has no seasons, only its state and history", async () => {
  const movie: Title = {
    ...base,
    id: "movie-5",
    tmdbId: 5,
    kind: "movie",
    title: "Blade Runner 2049",
    badge: { state: "missing", label: "Missing" },
    preferredQuality: null,
    nextSearchAt: "2026-11-18T18:00:00Z",
    historySummary: "Tried 3 releases, 2 fake, 1 stalled, retrying in 6h.",
    seasons: [],
    history: [],
  };
  setupApp(serve(movie), "#/title/movie-5");

  expect(await screen.findByRole("heading", { name: "Blade Runner 2049", level: 1 })).toBeTruthy();
  expect(screen.getByText("Tried 3 releases, 2 fake, 1 stalled, retrying in 6h.")).toBeTruthy();
  expect(screen.queryByRole("region", { name: /season/i })).toBeNull();
  expect(screen.getByText("Missing")).toBeTruthy();
});

test("an unknown title says so and offers to try again", async () => {
  setupApp(() => json({ error: "no such title" }, 404), "#/title/series-9");
  expect((await screen.findByRole("alert")).textContent).toContain("no such title");
  expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
});

test("download progress for this title moves the episode without a reload", async () => {
  const { calls } = setupApp(serve(house()), "#/title/series-2");
  const s4 = await screen.findByRole("region", { name: "Season 4" });
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));
  const before = calls.length;

  FakeEventSource.open()[0].emit("progress", { downloadId: "d", titleId: "series-2", episode: "s04e03", state: "downloading", percent: 61, bytesDone: 61, bytesTotal: 100 });

  expect(await within(s4).findByText("Downloading 61%")).toBeTruthy();
  expect(calls.length).toBe(before);
});

test("Esc and the close button go back to the wall", async () => {
  setupApp(serve(house()), "#/title/series-2");
  await screen.findByRole("heading", { name: "House", level: 1 });
  fireEvent.keyDown(window, { key: "Escape" });
  await waitFor(() => expect(window.location.hash).toBe("#/"));
  expect(screen.queryByRole("heading", { name: "House", level: 1 })).toBeNull();
});

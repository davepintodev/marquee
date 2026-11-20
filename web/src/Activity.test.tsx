// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { Download, HistoryEntry, TitleSummary } from "./api/types";
import { FakeEventSource, json, setupApp } from "./testkit";

const dune: Download = { id: "dl-1", titleId: "movie-1", titleName: "Dune: Part Two", releaseName: "Dune.Part.Two.2024.2160p.WEB-DL", state: "downloading", percent: 43, bytesDone: 9_234_179_072, bytesTotal: 21_474_836_480, stalled: false };
const arrival: Download = { id: "dl-2", titleId: "movie-2", titleName: "Arrival", releaseName: "Arrival.2016.1080p.BluRay", state: "downloading", percent: 12, bytesDone: 1_030_792_151, bytesTotal: 8_589_934_592, stalled: true };

const failed: HistoryEntry = { id: "h-stalled", at: "2026-11-18T09:42:00Z", kind: "failed", titleId: "movie-2", titleName: "Arrival", message: "Arrival.2016.1080p.BluRay stalled: no data for 1 minute. Blocklisted; trying the next release.", reason: "stalled", retryable: true };
const fake: HistoryEntry = { id: "h-fake", at: "2026-11-18T08:15:00Z", kind: "failed", titleId: "movie-3", titleName: "Blade Runner 2049", message: "The release was a .exe file, not a video. Dropped before download.", reason: "not_video", retryable: false };
const imported: HistoryEntry = { id: "h-imported", at: "2026-11-17T21:05:00Z", kind: "imported", titleId: "series-4", titleName: "House", episode: "s04e01", message: "Imported House - S04E01 - Alone [Bluray-1080p].", retryable: false };

function lib(over: Partial<TitleSummary> & Pick<TitleSummary, "id" | "title">): TitleSummary {
  return { tmdbId: 1, kind: "movie", year: 2020, overview: "", posterPath: "", backdropPath: "", inLibrary: true, ...over };
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

interface World {
  downloads: Download[];
  history: HistoryEntry[];
  titles: TitleSummary[];
}

function serve(w: World, extra?: (url: URL, init: Request) => Response | undefined) {
  return (url: URL, init: Request) => {
    const special = extra?.(url, init);
    if (special) return special;
    if (url.pathname === "/api/wall") return json({ items: [] });
    if (url.pathname === "/api/downloads") return json({ downloads: w.downloads });
    if (url.pathname === "/api/history") return json({ history: w.history });
    if (url.pathname === "/api/titles") return json({ titles: w.titles });
    return json({ error: "nope" }, 404);
  };
}

test("Activity shows the queue and the history together, failures in words", async () => {
  setupApp(serve({ downloads: [dune, arrival], history: [failed, fake, imported], titles: [] }), "#/activity");

  const queue = await screen.findByRole("list", { name: "Queue" });
  const rows = within(queue).getAllByRole("listitem");
  expect(rows).toHaveLength(2);
  expect(within(rows[0]).getByText("Dune: Part Two")).toBeTruthy();
  expect(within(rows[0]).getByText("43%")).toBeTruthy();
  expect(within(rows[0]).getByText(/8\.6 GB of 20 GB/)).toBeTruthy();
  expect(within(rows[0]).getByRole("progressbar").getAttribute("aria-valuenow")).toBe("43");
  expect(within(rows[1]).getByText(/stalled/i)).toBeTruthy();

  const history = screen.getByRole("list", { name: "History" });
  const items = within(history).getAllByRole("listitem");
  expect(items).toHaveLength(3);
  expect(within(items[0]).getByText("Stalled")).toBeTruthy();
  expect(within(items[0]).getByText(/no data for 1 minute/)).toBeTruthy();
  expect(within(items[1]).getByText("Fake file")).toBeTruthy();
  expect(within(items[1]).queryByRole("button", { name: /retry/i })).toBeNull();
  expect(within(items[2]).getByText("· S04E01")).toBeTruthy();
  expect(within(items[2]).getByRole("link", { name: "House" }).getAttribute("href")).toBe("#/title/series-4");
});

test("a running download's progress updates from the event stream, not from a reload", async () => {
  const { calls } = setupApp(serve({ downloads: [dune], history: [], titles: [] }), "#/activity");
  const queue = await screen.findByRole("list", { name: "Queue" });
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));
  expect(FakeEventSource.open()[0].url).toBe("http://marquee.test/api/events");
  const before = calls.length;

  FakeEventSource.open()[0].emit("progress", { downloadId: "dl-1", titleId: "movie-1", state: "downloading", percent: 57, bytesDone: 12_240_000_000, bytesTotal: 21_474_836_480 });

  expect(await within(queue).findByText("57%")).toBeTruthy();
  expect(within(queue).getByRole("progressbar").getAttribute("aria-valuenow")).toBe("57");
  expect(calls.length).toBe(before);
});

test("progress for a download the page has not seen refetches the queue; a finished title refetches everything", async () => {
  const world: World = { downloads: [dune], history: [], titles: [] };
  const { calls } = setupApp(serve(world), "#/activity");
  await screen.findByRole("list", { name: "Queue" });
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));
  const es = () => FakeEventSource.open()[0];

  world.downloads = [dune, { ...arrival, stalled: false, id: "dl-9", titleName: "Interstellar" }];
  es().emit("progress", { downloadId: "dl-9", titleId: "movie-2", state: "downloading", percent: 1, bytesDone: 1, bytesTotal: 100 });
  expect(await screen.findByText("Interstellar")).toBeTruthy();

  const asked = calls.length;
  world.downloads = [dune];
  world.history = [imported];
  es().emit("title", { titleId: "movie-2", badge: { state: "downloaded", label: "Downloaded" } });
  await waitFor(() => expect(screen.queryByText("Interstellar")).toBeNull());
  expect(await screen.findByText(/Imported House - S04E01/)).toBeTruthy();
  expect(calls.length).toBeGreaterThan(asked);
});

test("a reconnected stream refetches what it may have missed", async () => {
  const world: World = { downloads: [], history: [], titles: [] };
  setupApp(serve(world), "#/activity");
  await screen.findByText("Nothing has happened yet.");
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));
  const es = FakeEventSource.open()[0];
  es.open();

  world.history = [imported];
  es.open();

  expect(await screen.findByText(/Imported House - S04E01/)).toBeTruthy();
});

test("Retry on a failed item pushes it back into the queue", async () => {
  const world: World = { downloads: [arrival], history: [failed, imported], titles: [] };
  const { writes } = setupApp(
    serve(world, (url, init) => {
      if (init.method === "POST" && url.pathname === "/api/history/h-stalled/retry") {
        world.downloads = [];
        world.history = [{ ...imported, id: "h-s", kind: "searched", message: "Retrying after a failure." }, { ...failed, retryable: false }, imported];
        world.titles = [lib({ id: "movie-2", title: "Arrival", badge: { state: "searching", label: "Searching" } })];
        return json({ id: "movie-2" });
      }
    }),
    "#/activity",
  );
  const history = await screen.findByRole("list", { name: "History" });

  fireEvent.click(within(history).getByRole("button", { name: "Retry Arrival" }));

  const queue = screen.getByRole("list", { name: "Queue" });
  await within(queue).findByText("Searching");
  expect(within(queue).getByText("Arrival")).toBeTruthy();
  expect(within(history).queryByRole("button", { name: /retry/i })).toBeNull();
  expect(writes).toEqual([{ method: "POST", path: "/api/history/h-stalled/retry", body: undefined }]);
  expect((await screen.findByRole("status")).textContent).toMatch(/Arrival/);
});

test("retrying a series episode puts that episode in the queue, though the title badge says nothing about it", async () => {
  const stalledEp: HistoryEntry = { ...failed, id: "h-ep", titleId: "series-4", titleName: "House", episode: "s04e02" };
  const house = (state: "missing" | "searching") => ({
    ...lib({ id: "series-4", title: "House", kind: "series", tmdbId: 4, badge: { state: "partial_season", label: "Season 4 · 1 missing", season: 4, missingCount: 1 } }),
    seasons: [{ number: 4, episodes: [{ id: "s04e02", number: 2, name: "Big Baby", airDate: "2007-10-02", monitored: true, state }] }],
    historySummary: "",
  });
  let state: "missing" | "searching" = "missing";
  const world: World = { downloads: [], history: [stalledEp], titles: [house("missing")] };
  setupApp(
    serve(world, (url, init) => {
      if (init.method === "POST" && url.pathname === "/api/history/h-ep/retry") {
        state = "searching";
        world.history = [{ ...stalledEp, retryable: false }];
        return json(house("searching"));
      }
      if (url.pathname === "/api/titles/series-4") return json(house(state));
    }),
    "#/activity",
  );
  await screen.findByText("Nothing is downloading.");

  fireEvent.click(screen.getByRole("button", { name: "Retry House" }));

  const queue = await screen.findByRole("list", { name: "Queue" });
  expect(within(queue).getByText("House")).toBeTruthy();
  expect(within(queue).getByText(/S04E02/)).toBeTruthy();
});

test("a retry that is refused says why and keeps the button", async () => {
  setupApp(
    serve({ downloads: [], history: [failed], titles: [] }, (_url, init) =>
      init.method === "POST" ? json({ error: "only a failed item can be retried, and only once" }, 409) : undefined,
    ),
    "#/activity",
  );
  fireEvent.click(await screen.findByRole("button", { name: "Retry Arrival" }));

  expect((await screen.findByRole("alert")).textContent).toContain("only a failed item can be retried");
  expect(screen.getByRole("button", { name: "Retry Arrival" })).toBeTruthy();
});

test("an empty Activity says nothing is happening; a failing API says so with a way to retry", async () => {
  setupApp(serve({ downloads: [], history: [], titles: [] }), "#/activity");
  expect(await screen.findByText("Nothing is downloading.")).toBeTruthy();
  expect(screen.getByText("Nothing has happened yet.")).toBeTruthy();
  cleanup();

  setupApp(() => json({ error: "boom" }, 500), "#/activity");
  expect((await screen.findByRole("alert")).textContent).toContain("boom");
  expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
});

test("the grid links to Activity and a library popup links to the title's detail", async () => {
  const wall = { items: [{ source: "library", title: lib({ id: "series-4", title: "House", kind: "series", tmdbId: 4 }) }] };
  setupApp(() => json(wall), "#/grid");
  const link = await screen.findByRole("link", { name: "Activity" });
  expect(link.getAttribute("href")).toBe("#/activity");

  fireEvent.click((await screen.findAllByRole("button", { name: /house/i }))[0]);
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByRole("link", { name: "Details" }).getAttribute("href")).toBe("#/title/series-4");
});

const newest: HistoryEntry = { id: "h-new", at: "2026-11-18T11:59:00Z", kind: "searched", titleId: "movie-2", titleName: "Arrival", message: "Search now: skipping the backoff.", retryable: false };

// A server that holds `all` newest first and pages it by cursor, as the real one does.
function paged(world: World, all: () => HistoryEntry[], size: number) {
  const asked: string[] = [];
  const handler = serve(world, (url) => {
    if (url.pathname !== "/api/history") return undefined;
    asked.push(url.search);
    const rows = all();
    const cursor = url.searchParams.get("cursor");
    const from = cursor ? rows.findIndex((h) => h.id === cursor) + 1 : 0;
    const page = rows.slice(from, from + size);
    const more = from + size < rows.length;
    return json(more ? { history: page, nextCursor: page[page.length - 1].id } : { history: page });
  });
  return { handler, asked };
}

test("Activity asks for the next page of history with the cursor, and offers it only while there is one", async () => {
  const world: World = { downloads: [], history: [], titles: [] };
  const { handler, asked } = paged(world, () => [failed, fake, imported], 2);
  setupApp(handler, "#/activity");

  await screen.findByText(/no data for 1 minute/);
  expect(asked).toEqual(["?limit=100"]);
  expect(within(screen.getByRole("list", { name: "History" })).getAllByRole("listitem")).toHaveLength(2);

  fireEvent.click(screen.getByRole("button", { name: /older/i }));

  expect(await screen.findByText(/Imported House - S04E01/)).toBeTruthy();
  expect(asked).toEqual(["?limit=100", "?limit=100&cursor=h-fake"]);
  expect(within(screen.getByRole("list", { name: "History" })).getAllByRole("listitem")).toHaveLength(3);
  expect(screen.queryByRole("button", { name: /older/i })).toBeNull();
});

test("a live refetch keeps the older pages already loaded, without repeating an entry", async () => {
  const world: World = { downloads: [], history: [], titles: [] };
  let rows = [failed, fake, imported];
  const { handler, asked } = paged(world, () => rows, 2);
  setupApp(handler, "#/activity");
  await screen.findByText(/no data for 1 minute/);
  fireEvent.click(screen.getByRole("button", { name: /older/i }));
  await screen.findByText(/Imported House - S04E01/);
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));

  rows = [newest, failed, fake, imported];
  FakeEventSource.open()[0].emit("title", { titleId: "movie-2", badge: { state: "searching", label: "Searching" } });

  expect(await screen.findByText(/Search now: skipping the backoff/)).toBeTruthy();
  const items = within(screen.getByRole("list", { name: "History" })).getAllByRole("listitem");
  expect(items).toHaveLength(4);
  expect(screen.getByText(/Imported House - S04E01/)).toBeTruthy();
  expect(asked.at(-1)).toBe("?limit=100");
  expect(screen.queryByRole("button", { name: /older/i })).toBeNull();
});

test("a live refetch that does not reach the older pages starts over instead of hiding what lies between", async () => {
  const world: World = { downloads: [], history: [], titles: [] };
  const more = ["a", "b", "c"].map((id): HistoryEntry => ({ ...newest, id: `h-${id}`, message: `Searched ${id}.` }));
  let rows = [failed, fake, imported];
  const { handler, asked } = paged(world, () => rows, 2);
  setupApp(handler, "#/activity");
  await screen.findByText(/no data for 1 minute/);
  fireEvent.click(screen.getByRole("button", { name: /older/i }));
  await screen.findByText(/Imported House - S04E01/);
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));

  rows = [...more, failed, fake, imported];
  FakeEventSource.open()[0].emit("title", { titleId: "movie-2", badge: { state: "searching", label: "Searching" } });

  expect(await screen.findByText(/Searched a\./)).toBeTruthy();
  expect(within(screen.getByRole("list", { name: "History" })).getAllByRole("listitem")).toHaveLength(2);
  expect(screen.queryByText(/Imported House - S04E01/)).toBeNull();

  fireEvent.click(screen.getByRole("button", { name: /older/i }));
  expect(await screen.findByText(/Searched c\./)).toBeTruthy();
  expect(asked.at(-1)).toBe("?limit=100&cursor=h-b");
});

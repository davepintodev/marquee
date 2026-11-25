// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { Download, HistoryEntry, TitleSummary } from "./api/types";
import { FakeEventSource, json, setupApp } from "./testkit";

const dune: Download = { id: "dl-1", titleId: "movie-1", titleName: "Dune: Part Two", releaseName: "Dune.Part.Two.2024.2160p.WEB-DL", state: "downloading", percent: 43, bytesDone: 9_234_179_072, bytesTotal: 21_474_836_480, stalled: false };
const arrival: Download = { id: "dl-2", titleId: "movie-2", titleName: "Arrival", releaseName: "Arrival.2016.1080p.BluRay", state: "downloading", percent: 12, bytesDone: 1_030_792_151, bytesTotal: 8_589_934_592, stalled: true };
const sicario: Download = { id: "dl-3", titleId: "movie-5", titleName: "Sicario", releaseName: "Sicario.2015.1080p.WEB", state: "verifying", percent: 100, bytesDone: 5_000_000_000, bytesTotal: 5_000_000_000, stalled: false };
const house: Download = { id: "dl-4", titleId: "series-4", titleName: "House", episode: "s04e03", releaseName: "House.S04E03.1080p", state: "queued", percent: 0, bytesDone: 0, bytesTotal: 0, stalled: false };

const local = (day: number, hour: number, minute: number) => new Date(2026, 10, day, hour, minute).toISOString();

const failed: HistoryEntry = { id: "h-stalled", at: local(18, 9, 42), kind: "failed", titleId: "movie-2", titleName: "Arrival", message: "Arrival.2016.1080p.BluRay stalled: no data for 1 minute. Blocklisted; trying the next release.", reason: "stalled", retryable: true };
const fake: HistoryEntry = { id: "h-fake", at: local(18, 8, 15), kind: "failed", titleId: "movie-3", titleName: "Blade Runner 2049", message: "The release was a .exe file, not a video. Dropped before download.", reason: "not_video", retryable: false };
const imported: HistoryEntry = { id: "h-imported", at: local(17, 21, 5), kind: "imported", titleId: "series-4", titleName: "House", episode: "s04e01", message: "Imported House - S04E01 - Alone [Bluray-1080p].", retryable: false };

function lib(over: Partial<TitleSummary> & Pick<TitleSummary, "id" | "title">): TitleSummary {
  return { tmdbId: 1, kind: "movie", year: 2020, overview: "", posterPath: "", backdropPath: "", inLibrary: true, ...over };
}

beforeEach(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  vi.useFakeTimers({ toFake: ["Date"], now: new Date(2026, 10, 18, 12, 0) });
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

const section = (name: string) => screen.getByRole("region", { name });

test("Activity is a board of Downloading, Waiting and History, each with its hint", async () => {
  setupApp(serve({ downloads: [dune], history: [imported], titles: [] }), "#/activity");

  await screen.findByRole("region", { name: "Downloading" });
  expect(screen.getByRole("heading", { level: 1, name: "Activity" })).toBeTruthy();
  expect(within(section("Downloading")).getByText("Files arriving right now.")).toBeTruthy();
  expect(within(section("History")).getByText("What finished and what failed.")).toBeTruthy();
  expect(screen.queryByRole("region", { name: "Waiting" })).toBeNull();
  expect(screen.queryByRole("heading", { name: "Queue" })).toBeNull();
});

test("each download state shows its own word, state attribute and numbers", async () => {
  const world: World = { downloads: [dune, arrival, sicario, house], history: [], titles: [lib({ id: "movie-9", title: "Heat", badge: { state: "searching", label: "Searching" } })] };
  setupApp(serve(world), "#/activity");

  const cards = within(await screen.findByRole("list", { name: "Downloads" })).getAllByRole("listitem");
  expect(cards).toHaveLength(3);
  expect(cards.map((c) => c.getAttribute("data-state"))).toEqual(["downloading", "stalled", "verifying"]);

  expect(within(cards[0]).getByRole("link", { name: "Dune: Part Two" }).getAttribute("href")).toBe("#/title/movie-1");
  expect(within(cards[0]).getByText("43%")).toBeTruthy();
  expect(within(cards[0]).getByText("Downloading")).toBeTruthy();
  expect(within(cards[0]).getByText("8.6 GB of 20 GB")).toBeTruthy();
  expect(within(cards[0]).getByText("Dune.Part.Two.2024.2160p.WEB-DL")).toBeTruthy();
  expect(within(cards[0]).getByRole("progressbar", { name: "Dune: Part Two progress" }).getAttribute("aria-valuenow")).toBe("43");

  expect(within(cards[1]).getByText("Stalled")).toBeTruthy();
  expect(within(cards[1]).getByText(/No data has arrived for a while/)).toBeTruthy();
  expect(within(cards[2]).getByText("Verifying")).toBeTruthy();

  const waiting = within(section("Waiting")).getAllByRole("listitem");
  expect(within(section("Waiting")).getByText("Queued behind a download, or searching for a release.")).toBeTruthy();
  expect(waiting.map((w) => w.getAttribute("data-state"))).toEqual(["queued", "searching"]);
  expect(within(waiting[0]).getByRole("link", { name: /^House/ }).getAttribute("href")).toBe("#/title/series-4");
  expect(within(waiting[0]).getByText("S04E03 queued")).toBeTruthy();
  expect(within(waiting[1]).getByText("Heat")).toBeTruthy();
  expect(within(waiting[1]).getByText("Searching")).toBeTruthy();
  expect(within(section("Downloading")).queryByText("House")).toBeNull();
});

test("a title that is both downloading and searching is shown once, as downloading", async () => {
  const world: World = { downloads: [dune], history: [], titles: [lib({ id: "movie-1", title: "Dune: Part Two", badge: { state: "searching", label: "Searching" } })] };
  setupApp(serve(world), "#/activity");
  await screen.findByRole("list", { name: "Downloads" });
  expect(screen.queryByRole("region", { name: "Waiting" })).toBeNull();
});

test("the summary line counts what runs, waits and can be retried", async () => {
  const world: World = { downloads: [dune, arrival, house], history: [failed, fake, imported], titles: [lib({ id: "movie-9", title: "Heat", badge: { state: "searching", label: "Searching" } })] };
  setupApp(serve(world), "#/activity");
  expect(await screen.findByText("2 downloading · 2 waiting · 1 can be retried")).toBeTruthy();
});

test("with nothing going on the page says so in one centred sentence", async () => {
  setupApp(serve({ downloads: [], history: [], titles: [] }), "#/activity");
  expect(await screen.findByText(/Nothing is happening yet/)).toBeTruthy();
});

test("history is grouped by day with the time, the failure's reason in words and a retry only where one is offered", async () => {
  setupApp(serve({ downloads: [], history: [failed, fake, imported], titles: [] }), "#/activity");

  const history = await screen.findByRole("region", { name: "History" });
  const days = within(history).getAllByRole("region");
  expect(days.map((d) => d.getAttribute("aria-label"))).toEqual(["Today", "Yesterday"]);

  const today = within(days[0]).getAllByRole("listitem");
  expect(today).toHaveLength(2);
  expect(today[0].getAttribute("data-kind")).toBe("failed");
  expect(within(today[0]).getByText(/^\d{1,2}:42/)).toBeTruthy();
  expect(within(today[0]).getByRole("link", { name: "Arrival" }).getAttribute("href")).toBe("#/title/movie-2");
  expect(within(today[0]).getByText("Stalled").tagName).toBe("B");
  expect(within(today[0]).getByText(/no data for 1 minute/)).toBeTruthy();
  expect(within(today[0]).getByRole("button", { name: "Retry Arrival" })).toBeTruthy();
  expect(within(today[1]).getByText("Fake file")).toBeTruthy();
  expect(within(today[1]).queryByRole("button", { name: /retry/i })).toBeNull();

  const yesterday = within(days[1]).getAllByRole("listitem");
  expect(yesterday[0].getAttribute("data-kind")).toBe("imported");
  expect(within(yesterday[0]).getByText("S04E01")).toBeTruthy();
  expect(within(yesterday[0]).getByRole("link", { name: "House" }).getAttribute("href")).toBe("#/title/series-4");
  expect(within(yesterday[0]).queryByText("Failed")).toBeNull();
});

test("a running download's progress updates from the event stream, not from a reload", async () => {
  const { calls } = setupApp(serve({ downloads: [dune], history: [], titles: [] }), "#/activity");
  const card = (await within(await screen.findByRole("list", { name: "Downloads" })).findAllByRole("listitem"))[0];
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));
  expect(FakeEventSource.open()[0].url).toBe("http://marquee.test/api/events");
  const before = calls.length;

  FakeEventSource.open()[0].emit("progress", { downloadId: "dl-1", titleId: "movie-1", state: "downloading", percent: 57, bytesDone: 12_240_000_000, bytesTotal: 21_474_836_480 });

  expect(await within(card).findByText("57%")).toBeTruthy();
  expect(within(card).getByRole("progressbar").getAttribute("aria-valuenow")).toBe("57");
  expect(calls.length).toBe(before);
});

test("a progress event that moves a download to verifying changes its state word and attribute", async () => {
  setupApp(serve({ downloads: [dune], history: [], titles: [] }), "#/activity");
  const card = (await within(await screen.findByRole("list", { name: "Downloads" })).findAllByRole("listitem"))[0];
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));

  FakeEventSource.open()[0].emit("progress", { downloadId: "dl-1", titleId: "movie-1", state: "verifying", percent: 100, bytesDone: 21_474_836_480, bytesTotal: 21_474_836_480 });

  expect(await within(card).findByText("Verifying")).toBeTruthy();
  expect(card.getAttribute("data-state")).toBe("verifying");
});

test("progress for a download the page has not seen refetches the queue; a finished title refetches everything", async () => {
  const world: World = { downloads: [dune], history: [], titles: [] };
  const { calls } = setupApp(serve(world), "#/activity");
  await screen.findByRole("list", { name: "Downloads" });
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
  await screen.findByText(/Nothing is happening yet/);
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
  const history = await screen.findByRole("region", { name: "History" });

  fireEvent.click(within(history).getByRole("button", { name: "Retry Arrival" }));

  const queue = await screen.findByRole("region", { name: "Waiting" });
  await within(queue).findByText("Searching");
  expect(within(queue).getByRole("link", { name: /^Arrival/ })).toBeTruthy();
  expect(within(history).queryByRole("button", { name: /retry/i })).toBeNull();
  expect(writes).toEqual([{ method: "POST", path: "/api/history/h-stalled/retry", body: undefined }]);
  expect((await screen.findByRole("status")).textContent).toMatch(/Arrival/);
});

test("a retry that fails raises an error toast whose action tries again", async () => {
  const world: World = { downloads: [], history: [failed], titles: [] };
  let attempts = 0;
  setupApp(
    serve(world, (url, init) => {
      if (init.method === "POST" && url.pathname === "/api/history/h-stalled/retry") {
        attempts += 1;
        return attempts === 1 ? json({ error: "AIOStreams did not answer" }, 502) : json({ id: "movie-2" });
      }
    }),
    "#/activity",
  );
  const history = await screen.findByRole("region", { name: "History" });

  fireEvent.click(within(history).getByRole("button", { name: "Retry Arrival" }));

  expect((await screen.findByRole("alert")).textContent).toContain("AIOStreams did not answer");
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await waitFor(() => expect(attempts).toBe(2));
  expect((await screen.findByRole("status")).textContent).toMatch(/Arrival/);
});

test("the page shows a skeleton of the board while it loads, then the board", async () => {
  let release: (r: Response) => void = () => {};
  const pending = new Promise<Response>((r) => (release = r));
  setupApp((url, init) => (url.pathname === "/api/downloads" ? pending : serve({ downloads: [dune], history: [imported], titles: [] })(url, init)), "#/activity");

  expect((await screen.findByRole("status")).textContent).toBe("Loading activity");
  expect(document.querySelectorAll(".sk").length).toBeGreaterThan(0);

  release(json({ downloads: [dune] }));
  await screen.findByRole("region", { name: "Downloading" });
  expect(document.querySelectorAll(".sk")).toHaveLength(0);
});

test("a page that failed to load says why with one way to try again", async () => {
  let fail = true;
  setupApp((url, init) => (fail && url.pathname === "/api/downloads" ? json({ error: "marquee is starting" }, 503) : serve({ downloads: [dune], history: [], titles: [] })(url, init)), "#/activity");
  expect((await screen.findByRole("alert")).textContent).toContain("marquee is starting");

  fail = false;
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));

  await screen.findByRole("region", { name: "Downloading" });
});

const houseSeries = (over: Partial<TitleSummary> = {}) =>
  lib({ id: "series-4", title: "House", kind: "series", tmdbId: 4, badge: { state: "partial_season", label: "Season 4 · 1 missing", season: 4, missingCount: 1 }, ...over });

test("retrying a series episode puts that episode in the queue, though the title badge says nothing about it", async () => {
  const stalledEp: HistoryEntry = { ...failed, id: "h-ep", titleId: "series-4", titleName: "House", episode: "s04e02" };
  const world: World = { downloads: [], history: [stalledEp], titles: [houseSeries()] };
  setupApp(
    serve(world, (url, init) => {
      if (init.method === "POST" && url.pathname === "/api/history/h-ep/retry") {
        world.history = [{ ...stalledEp, retryable: false }];
        world.titles = [houseSeries({ searchingEpisodes: ["s04e02"] })];
        return json(houseSeries({ searchingEpisodes: ["s04e02"] }));
      }
    }),
    "#/activity",
  );
  await screen.findByText("Nothing is downloading.");

  fireEvent.click(screen.getByRole("button", { name: "Retry House" }));

  const queue = await screen.findByRole("region", { name: "Waiting" });
  const link = within(queue).getByRole("link", { name: /^House/ });
  expect(link.getAttribute("title")).toContain("S04E02");
});

test("a series waiting on episodes shows under Waiting on a fresh load, whatever its badge says", async () => {
  const world: World = { downloads: [], history: [], titles: [houseSeries({ searchingEpisodes: ["s04e02", "s04e03"] }), lib({ id: "series-5", title: "Fargo", kind: "series" })] };
  setupApp(serve(world), "#/activity");

  const queue = await screen.findByRole("region", { name: "Waiting" });
  const items = within(queue).getAllByRole("link");
  expect(items).toHaveLength(1);
  expect(items[0].getAttribute("title")).toContain("S04E02, S04E03");
  expect(items[0].closest("[data-state]")?.getAttribute("data-state")).toBe("searching");
  expect(screen.getByText("1 waiting")).toBeTruthy();
});

test("a series whose badge is searching names its episodes when the list has them, and says so generally when it has none", async () => {
  const searching = { state: "searching", label: "Searching" } as const;
  const world: World = { downloads: [], history: [], titles: [houseSeries({ badge: searching, searchingEpisodes: ["s04e01"] }), lib({ id: "series-5", title: "Fargo", kind: "series", badge: searching })] };
  setupApp(serve(world), "#/activity");

  const queue = await screen.findByRole("region", { name: "Waiting" });
  expect(within(queue).getByRole("link", { name: /^House/ }).getAttribute("title")).toContain("S04E01");
  expect(within(queue).getByRole("link", { name: /^Fargo/ }).getAttribute("title")).toContain("Waiting for a release to appear.");
});

test("Close and Esc both return from Activity to the grid", async () => {
  const wall = { items: [{ source: "library", title: lib({ id: "series-4", title: "House", kind: "series", tmdbId: 4 }) }] };
  setupApp(serve({ downloads: [], history: [], titles: [] }, (url) => (url.pathname === "/api/wall" ? json(wall) : undefined)), "#/activity");
  fireEvent.click(await screen.findByRole("button", { name: "Close" }));
  expect(await screen.findByRole("link", { name: "Activity" })).toBeTruthy();
  expect(screen.queryByRole("heading", { level: 1, name: "Activity" })).toBeNull();

  fireEvent.click(screen.getByRole("link", { name: "Activity" }));
  await screen.findByRole("heading", { level: 1, name: "Activity" });
  fireEvent.keyDown(window, { key: "Escape" });
  await waitFor(() => expect(screen.queryByRole("heading", { level: 1, name: "Activity" })).toBeNull());
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

test("a failing API says so with a way to try again", async () => {
  setupApp(() => json({ error: "boom" }, 500), "#/activity");
  expect((await screen.findByRole("alert")).textContent).toContain("boom");
  expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
});

// jsdom does no layout, so the gallery's size is whatever the test says it is.
function sized(scrollWidth: number, clientWidth: number) {
  const scrollBy = vi.fn();
  Object.defineProperty(HTMLElement.prototype, "scrollWidth", { configurable: true, get: () => scrollWidth });
  Object.defineProperty(HTMLElement.prototype, "clientWidth", { configurable: true, get: () => clientWidth });
  Element.prototype.scrollBy = scrollBy as unknown as typeof Element.prototype.scrollBy;
  return scrollBy;
}

function reducedMotion(on: boolean) {
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: on && q.includes("prefers-reduced-motion"), media: q, addEventListener() {}, removeEventListener() {} }));
}

afterEach(() => {
  delete (HTMLElement.prototype as { scrollWidth?: number }).scrollWidth;
  delete (HTMLElement.prototype as { clientWidth?: number }).clientWidth;
  vi.unstubAllGlobals();
});

test("the gallery has no arrows while every download fits", async () => {
  sized(380, 380);
  setupApp(serve({ downloads: [dune], history: [], titles: [] }), "#/activity");
  await screen.findByRole("list", { name: "Downloads" });
  expect(screen.queryByRole("button", { name: /previous downloads/i })).toBeNull();
  expect(screen.queryByRole("button", { name: /next downloads/i })).toBeNull();
});

test("when downloads overflow, arrows page the gallery sideways and dim at its ends", async () => {
  const scrollBy = sized(1200, 600);
  reducedMotion(false);
  setupApp(serve({ downloads: [dune, arrival, sicario], history: [], titles: [] }), "#/activity");
  const previous = await screen.findByRole("button", { name: "Previous downloads" });
  const next = screen.getByRole("button", { name: "Next downloads" });
  expect((previous as HTMLButtonElement).disabled).toBe(true);
  expect((next as HTMLButtonElement).disabled).toBe(false);

  fireEvent.click(next);
  expect(scrollBy).toHaveBeenCalledWith({ left: 540, behavior: "smooth" });

  const list = screen.getByRole("list", { name: "Downloads" });
  list.scrollLeft = 600;
  fireEvent.scroll(list);
  await waitFor(() => expect((next as HTMLButtonElement).disabled).toBe(true));
  expect((previous as HTMLButtonElement).disabled).toBe(false);
  fireEvent.click(previous);
  expect(scrollBy).toHaveBeenLastCalledWith({ left: -540, behavior: "smooth" });
});

test("the gallery jumps instead of gliding when the person prefers less motion", async () => {
  const scrollBy = sized(1200, 600);
  reducedMotion(true);
  setupApp(serve({ downloads: [dune, arrival], history: [], titles: [] }), "#/activity");
  fireEvent.click(await screen.findByRole("button", { name: "Next downloads" }));
  expect(scrollBy).toHaveBeenCalledWith({ left: 540, behavior: "auto" });
});

test("the grid links to Activity, and a library title opens its own page from there", async () => {
  const wall = { items: [{ source: "library", title: lib({ id: "series-4", title: "House", kind: "series", tmdbId: 4 }) }] };
  setupApp(() => json(wall), "#/grid");
  const link = await screen.findByRole("link", { name: "Activity" });
  expect(link.getAttribute("href")).toBe("#/activity");

  fireEvent.click((await screen.findAllByRole("button", { name: /house/i }))[0]);

  await waitFor(() => expect(window.location.hash).toBe("#/title/series-4"));
  expect(screen.queryByRole("dialog")).toBeNull();
});

const newest: HistoryEntry = { id: "h-new", at: local(18, 11, 59), kind: "searched", titleId: "movie-2", titleName: "Arrival", message: "Search now: skipping the backoff.", retryable: false };

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
  expect(within(section("History")).getAllByRole("listitem")).toHaveLength(2);

  fireEvent.click(screen.getByRole("button", { name: /older/i }));

  expect(await screen.findByText(/Imported House - S04E01/)).toBeTruthy();
  expect(asked).toEqual(["?limit=100", "?limit=100&cursor=h-fake"]);
  expect(within(section("History")).getAllByRole("listitem")).toHaveLength(3);
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
  const items = within(section("History")).getAllByRole("listitem");
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
  expect(within(section("History")).getAllByRole("listitem")).toHaveLength(2);
  expect(screen.queryByText(/Imported House - S04E01/)).toBeNull();

  fireEvent.click(screen.getByRole("button", { name: /older/i }));
  expect(await screen.findByText(/Searched c\./)).toBeTruthy();
  expect(asked.at(-1)).toBe("?limit=100&cursor=h-b");
});

function counted() {
  const asked = { downloads: 0 };
  const world: World = { downloads: [], history: [], titles: [] };
  const handler = serve(world, (url) => {
    if (url.pathname === "/api/downloads") asked.downloads++;
    return undefined;
  });
  return { asked, handler };
}

const later = (ms: number) => new Promise((r) => setTimeout(r, ms));

test("a burst of title events makes one refetch", async () => {
  const { asked, handler } = counted();
  setupApp(handler, "#/activity");
  await screen.findByText(/Nothing is happening yet/);
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));
  const before = asked.downloads;

  for (let i = 0; i < 5; i++) FakeEventSource.open()[0].emit("title", { titleId: `movie-${i}`, badge: { state: "searching", label: "Searching" } });
  await waitFor(() => expect(asked.downloads).toBe(before + 1));
  await later(500);

  expect(asked.downloads).toBe(before + 1);
});

test("leaving Activity cancels a refetch still waiting for its burst to end", async () => {
  const { asked, handler } = counted();
  setupApp(handler, "#/activity");
  await screen.findByText(/Nothing is happening yet/);
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));
  const before = asked.downloads;

  FakeEventSource.open()[0].emit("title", { titleId: "movie-2", badge: { state: "searching", label: "Searching" } });
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  await later(500);

  expect(asked.downloads).toBe(before);
});

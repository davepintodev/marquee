// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { Library } from "./Library";
import { createApi } from "./api/client";
import type { Badge, TitleSummary, Wall } from "./api/types";

// jsdom's Request rejects relative URLs, so tests use an absolute base.
const BASE = "http://marquee.test";

function title(over: Partial<TitleSummary> & Pick<TitleSummary, "tmdbId" | "kind" | "title">): TitleSummary {
  return {
    id: `${over.kind}-${over.tmdbId}`,
    year: 2020,
    overview: "",
    posterPath: `/api/images/poster/${over.kind}-${over.tmdbId}`,
    backdropPath: `/api/images/backdrop/${over.kind}-${over.tmdbId}`,
    inLibrary: false,
    ...over,
  };
}

const downloaded: Badge = { state: "downloaded", label: "Downloaded" };
const dune = title({ tmdbId: 1, kind: "movie", title: "Dune", year: 2021 });
const house = title({ tmdbId: 2, kind: "series", title: "House", year: 2004, inLibrary: true, monitored: true, badge: { state: "partial_season", label: "Season 4 · 3 missing", season: 4, missingCount: 3 } });
const hail = title({ tmdbId: 3, kind: "movie", title: "Project Hail Mary", year: 2026, inLibrary: true, badge: downloaded });

const wall: Wall = {
  items: [
    { source: "library", title: house },
    { source: "library", title: hail },
    { source: "trending", title: dune },
  ],
};

type Handler = (url: URL, init: Request) => Response | Promise<Response>;

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

// The app opens on the poster wall; these tests mostly work in the grid screen behind it.
function setup(handler: Handler, hash = "#/grid") {
  window.location.hash = hash;
  const calls: URL[] = [];
  const writes: { method: string; path: string; body: unknown }[] = [];
  const fetchStub = vi.fn(async (input: Request) => {
    const url = new URL(input.url);
    calls.push(url);
    if (input.method !== "GET") {
      const text = await input.clone().text();
      writes.push({ method: input.method, path: url.pathname, body: text ? JSON.parse(text) : undefined });
    }
    return handler(url, input);
  });
  const api = createApi({ baseUrl: BASE, fetch: fetchStub as unknown as typeof fetch });
  render(<App api={api} />);
  return { calls, writes };
}

const grid = () => within(document.querySelector<HTMLElement>(".grid-screen")!);
const bar = () => screen.getByRole("searchbox", { name: /search movies and series/i });
const type = (v: string) => fireEvent.change(bar(), { target: { value: v } });
const root = () => document.querySelector("[data-mode]") as HTMLElement;

beforeEach(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("the library grid lists the API's titles, library first, each with its state as a bar and no pill", async () => {
  setup((url) => (url.pathname === "/api/wall" ? json(wall) : json({ error: "nope" }, 404)));

  const tiles = await grid().findAllByRole("button", { name: /house|hail mary|dune/i });
  const names = tiles.map((t) => t.getAttribute("aria-label"));
  expect(names).toEqual(["House (2004), Season 4 · 3 missing", "Project Hail Mary (2026), Downloaded", "Dune (2021)"]);
  expect(tiles[0].querySelector<HTMLElement>(".meter")?.dataset.state).toBe("partial_season");
  expect(tiles[1].querySelector(".meter")).toBeNull();
  expect(tiles[2].querySelector(".meter")).toBeNull();
  expect(tiles[0].querySelector(".badge")).toBeNull();
  expect(within(tiles[0]).getByText("House")).toBeTruthy();
  expect(within(tiles[0]).getByText("2004 · Series")).toBeTruthy();
  expect(tiles[0].querySelector("img")!.getAttribute("src")).toBe(`${BASE}/api/images/poster/series-2`);
  expect(root().dataset.mode).toBe("library");
});

test("the filter chips count and narrow the library", async () => {
  setup(() => json(wall));
  await grid().findAllByRole("button", { name: /house/i });
  const chips = within(grid().getByRole("group", { name: /filter the library/i }));

  expect(chips.getAllByRole("button").map((c) => c.textContent)).toEqual(["All3", "Movies2", "Series1", "Downloading0", "Missing1"]);
  fireEvent.click(chips.getByRole("button", { name: /^movies/i }));

  expect(chips.getByRole("button", { name: /^movies/i }).getAttribute("aria-pressed")).toBe("true");
  expect(grid().getAllByRole("button", { name: /\(20\d\d\)/ }).map((t) => t.getAttribute("aria-label"))).toEqual(["Project Hail Mary (2026), Downloaded", "Dune (2021)"]);
  fireEvent.click(chips.getByRole("button", { name: /^missing/i }));
  expect(grid().getAllByRole("button", { name: /\(20\d\d\)/ }).map((t) => t.getAttribute("aria-label"))).toEqual(["House (2004), Season 4 · 3 missing"]);
  fireEvent.click(chips.getByRole("button", { name: /^downloading/i }));
  expect(grid().getByText("Nothing here.")).toBeTruthy();
});

test("typing switches to search mode and shows the results in the same grid, with year, kind and state", async () => {
  const { calls } = setup((url) => {
    if (url.pathname === "/api/wall") return json(wall);
    return json({ results: [dune, house] });
  });
  await grid().findAllByRole("button", { name: /house/i });

  type("du");

  await waitFor(() => expect(root().dataset.mode).toBe("search"));
  const results = await screen.findByRole("list", { name: /results/i });
  const items = within(results).getAllByRole("listitem");
  expect(items).toHaveLength(2);
  expect(screen.getByText("2 results")).toBeTruthy();
  expect(within(items[0]).getByText("Dune")).toBeTruthy();
  expect(within(items[0]).getByText("2021 · Movie")).toBeTruthy();
  expect(within(items[0]).queryByText(/downloaded|missing/i)).toBeNull();
  expect(within(items[1]).getByText("2004 · Series")).toBeTruthy();
  expect(within(items[1]).getByText("Season 4 · 3 missing")).toBeTruthy();
  expect(items[1].querySelector<HTMLElement>(".meter")?.dataset.state).toBe("partial_season");
  expect(items[0].querySelector("img")!.getAttribute("src")).toBe(`${BASE}/api/images/poster/movie-1`);
  expect(calls.filter((u) => u.pathname === "/api/search").map((u) => u.searchParams.get("q"))).toEqual(["du"]);
});

test("Esc clears the search and brings the library back", async () => {
  setup((url) => (url.pathname === "/api/wall" ? json(wall) : json({ results: [dune] })));
  await grid().findAllByRole("button", { name: /house/i });
  type("dune");
  await screen.findByRole("list", { name: /results/i });

  fireEvent.keyDown(window, { key: "Escape" });

  expect(root().dataset.mode).toBe("library");
  expect((bar() as HTMLInputElement).value).toBe("");
  expect(screen.queryByRole("list", { name: /results/i })).toBeNull();
  expect(document.activeElement).toBe(bar());
});

test("a blank query never reaches the API and stays on the library", async () => {
  const { calls } = setup(() => json(wall));
  await grid().findAllByRole("button", { name: /house/i });

  type("   ");
  await new Promise((r) => setTimeout(r, 350));

  expect(root().dataset.mode).toBe("library");
  expect(calls.some((u) => u.pathname === "/api/search")).toBe(false);
});

test("a slow answer to an old query never replaces the newer results", async () => {
  let release!: () => void;
  const slow = new Promise<void>((r) => (release = r));
  setup(async (url) => {
    if (url.pathname === "/api/wall") return json(wall);
    if (url.searchParams.get("q") === "du") {
      await slow;
      return json({ results: [house] });
    }
    return json({ results: [dune] });
  });
  await grid().findAllByRole("button", { name: /house/i });

  type("du");
  await new Promise((r) => setTimeout(r, 300));
  type("dune");
  await screen.findByRole("list", { name: /results/i });
  release();
  await new Promise((r) => setTimeout(r, 50));

  const results = screen.getByRole("list", { name: /results/i });
  expect(within(results).getAllByRole("listitem")).toHaveLength(1);
  expect(within(results).queryByText("House")).toBeNull();
});

test("no matches say so", async () => {
  setup((url) => (url.pathname === "/api/wall" ? json(wall) : json({ results: [] })));
  await grid().findAllByRole("button", { name: /house/i });
  type("zzz");
  expect(await screen.findByText(/nothing found/i)).toBeTruthy();
});

test("a wall error from the API shows its plain message and search still works", async () => {
  setup((url) =>
    url.pathname === "/api/wall" ? json({ error: "this part of marquee is not built yet" }, 501) : json({ results: [dune] }),
  );

  expect((await screen.findByRole("alert")).textContent).toContain("this part of marquee is not built yet");

  type("dune");
  expect(await screen.findByText("Dune")).toBeTruthy();
  expect(screen.queryByRole("alert")).toBeNull();
});

test("a search error shows the API's message, then clears when the query does", async () => {
  setup((url) =>
    url.pathname === "/api/wall" ? json(wall) : json({ error: "TMDB did not answer" }, 502),
  );
  await grid().findAllByRole("button", { name: /house/i });

  type("dune");
  expect((await screen.findByRole("alert")).textContent).toContain("TMDB did not answer");

  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByRole("alert")).toBeNull();
});

test("a network failure and a non-JSON error body both give a plain message, never a crash", async () => {
  setup((url) => {
    if (url.pathname === "/api/wall") throw new TypeError("Failed to fetch");
    return new Response("<html>bad gateway</html>", { status: 502, headers: { "Content-Type": "text/html" } });
  });
  expect((await screen.findByRole("alert")).textContent).toMatch(/can.t reach marquee/i);

  type("dune");
  await waitFor(() => expect(screen.getAllByRole("alert").some((a) => /502/.test(a.textContent ?? ""))).toBe(true));
});

test("an empty library still shows the search bar", async () => {
  setup(() => json({ items: [] }));
  expect(await grid().findByText(/nothing to show yet/i)).toBeTruthy();
  expect(bar()).toBeTruthy();
});

test("the library reads as loading only while it loads, not when it is empty or failed", async () => {
  let answer!: () => void;
  const held = new Promise<void>((r) => (answer = r));
  setup(async (url) => {
    if (url.pathname !== "/api/wall") return json({ checks: [] });
    await held;
    return json({ items: [] });
  });
  expect(grid().getByLabelText("Library").getAttribute("aria-busy")).toBe("true");
  expect(screen.getByLabelText("Posters").getAttribute("aria-busy")).toBe("true");

  answer();
  await grid().findByText(/nothing to show yet/i);
  expect(grid().getByLabelText("Library").getAttribute("aria-busy")).toBe("false");
  expect(screen.getByLabelText("Posters").getAttribute("aria-busy")).toBe("false");
});

test("a failed wall stops reading as loading", async () => {
  setup((url) => (url.pathname === "/api/wall" ? json({ error: "down" }, 502) : json({ checks: [] })));
  await screen.findByRole("alert");
  expect(grid().getByLabelText("Library").getAttribute("aria-busy")).toBe("false");
  expect(screen.getByLabelText("Posters").getAttribute("aria-busy")).toBe("false");
});

test("a poster that fails to load falls back to the title instead of a broken image", async () => {
  setup((url) => (url.pathname === "/api/wall" ? json(wall) : json({ results: [dune] })));
  const [tile] = await grid().findAllByRole("button", { name: /house/i });

  fireEvent.error(tile.querySelector("img")!);

  expect(tile.querySelector("img")).toBeNull();
  expect(tile.querySelector(".art-fallback")?.textContent).toContain("House");
});

test("Try again reloads the wall after an error", async () => {
  let up = false;
  const { calls } = setup(() => (up ? json(wall) : json({ error: "this part of marquee is not built yet" }, 501)));
  await screen.findByRole("alert");
  up = true;

  fireEvent.click(screen.getByRole("button", { name: /try again/i }));

  expect((await screen.findAllByRole("button", { name: /house/i })).length).toBeGreaterThan(0);
  expect(screen.queryByRole("alert")).toBeNull();
  expect(calls.filter((u) => u.pathname === "/api/wall")).toHaveLength(2);
});

test("a new query never shows the old query's results while its own answer is pending", async () => {
  setup((url) => {
    if (url.pathname === "/api/wall") return json(wall);
    if (url.searchParams.get("q") === "dune") return json({ results: [dune] });
    return new Promise<Response>(() => {});
  });
  await grid().findAllByRole("button", { name: /house/i });
  type("dune");
  await screen.findByRole("list", { name: /results/i });

  type("");
  type("x");

  expect(screen.queryByRole("list", { name: /results/i })).toBeNull();
});

test("a new query never shows the old query's error while its own answer is pending", async () => {
  setup((url) => {
    if (url.pathname === "/api/wall") return json(wall);
    if (url.searchParams.get("q") === "dune") return json({ error: "TMDB did not answer" }, 502);
    return new Promise<Response>(() => {});
  });
  await grid().findAllByRole("button", { name: /house/i });
  type("dune");
  await screen.findByRole("alert");

  type("");
  type("x");

  expect(screen.queryByRole("alert")).toBeNull();
});

test("a library tile whose image failed tries the new image when its title gets another poster", async () => {
  const api = createApi({ baseUrl: BASE, fetch: (async () => json(wall)) as unknown as typeof fetch });
  const { rerender } = render(<Library api={api} items={[house, hail]} />);
  const [tile] = screen.getAllByRole("button", { name: /\(20\d\d\)/ });
  fireEvent.error(tile.querySelector("img")!);
  expect(tile.querySelector("img")).toBeNull();

  rerender(<Library api={api} items={[{ ...house, posterPath: "/api/images/poster/series-22" }, hail]} />);

  const again = screen.getAllByRole("button", { name: /\(20\d\d\)/ })[0];
  expect(again.querySelector("img")?.getAttribute("src")).toContain("series-22");
});

const dunePage = { ...dune, overview: "Paul Atreides leads the Fremen." };
const houseOut = { ...house, seasons: [], historySummary: "Nothing tried yet." };
const duneAdded = { ...dunePage, inLibrary: true, monitored: true, badge: { state: "searching" as const, label: "Searching" }, seasons: [], historySummary: "Nothing tried yet." };

const popupWall: Wall = {
  items: [
    { source: "library", title: house },
    { source: "trending", title: dunePage },
  ],
};

function popupApi(extra: Handler = () => json({ error: "nope" }, 404)): Handler {
  return (url, init) => {
    if (url.pathname === "/api/wall") return json(popupWall);
    if (url.pathname === "/api/search") return json({ results: [dunePage, house] });
    return extra(url, init);
  };
}

const openTile = async (name: RegExp) => fireEvent.click((await grid().findAllByRole("button", { name }))[0]);
const dialog = () => screen.getByRole("dialog");

test("clicking a wall tile opens the popup with poster, year, kind and summary, and adds nothing", async () => {
  const { writes } = setup(popupApi());

  await openTile(/^Dune \(2021\)/);

  const d = within(dialog());
  expect(d.getByRole("heading", { name: "Dune" })).toBeTruthy();
  expect(d.getByText("2021 · movie")).toBeTruthy();
  expect(d.getByText("Paul Atreides leads the Fremen.")).toBeTruthy();
  expect(dialog().querySelector("img")!.getAttribute("src")).toBe(`${BASE}/api/images/poster/movie-1`);
  expect(d.getByRole("button", { name: "Add & monitor" })).toBeTruthy();
  expect(d.getByRole("button", { name: "Interactive search" })).toBeTruthy();
  expect(writes).toEqual([]);
});

test("clicking a search result opens the popup and adds nothing", async () => {
  const { writes } = setup(popupApi());
  await grid().findAllByRole("button", { name: /house/i });
  type("dune");
  const results = await screen.findByRole("list", { name: /results/i });

  fireEvent.click(within(results).getByRole("button", { name: /dune/i }));

  expect(within(dialog()).getByRole("heading", { name: "Dune" })).toBeTruthy();
  expect(writes).toEqual([]);
});

test("Add & monitor adds the title, confirms with a toast and turns into Search now", async () => {
  const { writes } = setup(popupApi((url, init) => (init.method === "POST" && url.pathname === "/api/titles" ? json(duneAdded, 201) : json({ error: "nope" }, 404))));
  await openTile(/^Dune \(2021\)/);

  fireEvent.click(within(dialog()).getByRole("button", { name: "Add & monitor" }));

  expect((await screen.findByRole("status")).textContent).toMatch(/added.*dune/i);
  expect(writes).toEqual([{ method: "POST", path: "/api/titles", body: { kind: "movie", tmdbId: 1 } }]);
  expect(within(dialog()).getByRole("button", { name: "Search now" })).toBeTruthy();
  expect(within(dialog()).queryByRole("button", { name: "Add & monitor" })).toBeNull();
});

test("pressing Add & monitor twice quickly sends one request", async () => {
  let release!: () => void;
  const hold = new Promise<void>((r) => (release = r));
  const { writes } = setup(
    popupApi(async (url, init) => {
      if (init.method === "POST" && url.pathname === "/api/titles") {
        await hold;
        return json(duneAdded, 201);
      }
      return json({ error: "nope" }, 404);
    }),
  );
  await openTile(/^Dune \(2021\)/);
  const add = within(dialog()).getByRole("button", { name: "Add & monitor" });

  fireEvent.click(add);
  fireEvent.click(add);
  release();

  await within(dialog()).findByRole("button", { name: "Search now" });
  expect(writes).toHaveLength(1);
});

test("a title already in the library opens with Search now, which searches it", async () => {
  const { writes } = setup(popupApi((url, init) => (init.method === "POST" && url.pathname === "/api/titles/series-2/search" ? json(houseOut) : json({ error: "nope" }, 404))));

  await openTile(/^House \(2004\)/);
  expect(within(dialog()).queryByRole("button", { name: "Add & monitor" })).toBeNull();
  fireEvent.click(within(dialog()).getByRole("button", { name: "Search now" }));

  expect((await screen.findByRole("status")).textContent).toMatch(/searching.*house/i);
  expect(writes).toEqual([{ method: "POST", path: "/api/titles/series-2/search", body: undefined }]);
});

test("Search now on a Missing library title moves its popup and tile badge to Searching", async () => {
  const missing = { ...house, badge: { state: "missing" as const, label: "Missing" } };
  const searching = { ...missing, badge: { state: "searching" as const, label: "Searching" }, seasons: [], historySummary: "Nothing tried yet." };
  setup((url, init) => {
    if (url.pathname === "/api/wall") return json({ items: [{ source: "library", title: missing }] });
    if (init.method === "POST" && url.pathname === "/api/titles/series-2/search") return json(searching);
    return json({ error: "nope" }, 404);
  });
  await openTile(/^House \(2004\), Missing/);
  expect(within(dialog()).getByText("Missing")).toBeTruthy();

  fireEvent.click(within(dialog()).getByRole("button", { name: "Search now" }));

  await screen.findByRole("status");
  expect(within(dialog()).getByText("Searching")).toBeTruthy();
  expect(within(dialog()).queryByText("Missing")).toBeNull();
  fireEvent.click(within(dialog()).getByRole("button", { name: "Close" }));
  expect(screen.getAllByRole("button", { name: /^House \(2004\), Searching/ }).length).toBeGreaterThan(0);
  expect(screen.queryByRole("button", { name: /^House \(2004\), Missing/ })).toBeNull();
});

test("a failed add shows the API's message in the popup and can be tried again", async () => {
  let up = false;
  setup(popupApi((url, init) => (init.method === "POST" && url.pathname === "/api/titles" ? (up ? json(duneAdded, 201) : json({ error: "TMDB did not answer" }, 502)) : json({ error: "nope" }, 404))));
  await openTile(/^Dune \(2021\)/);

  fireEvent.click(within(dialog()).getByRole("button", { name: "Add & monitor" }));

  expect((await within(dialog()).findByRole("alert")).textContent).toContain("TMDB did not answer");
  expect(screen.queryByRole("status")).toBeNull();
  up = true;
  fireEvent.click(within(dialog()).getByRole("button", { name: "Add & monitor" }));
  await within(dialog()).findByRole("button", { name: "Search now" });
  expect(within(dialog()).queryByRole("alert")).toBeNull();
});

test("an added title reads as in the library on its tile and when its popup is reopened", async () => {
  setup(popupApi((url, init) => (init.method === "POST" && url.pathname === "/api/titles" ? json(duneAdded, 201) : json({ error: "nope" }, 404))));
  await openTile(/^Dune \(2021\)/);
  fireEvent.click(within(dialog()).getByRole("button", { name: "Add & monitor" }));
  await within(dialog()).findByRole("button", { name: "Search now" });

  fireEvent.click(within(dialog()).getByRole("button", { name: "Close" }));

  expect(screen.queryByRole("dialog")).toBeNull();
  const tile = screen.getAllByRole("button", { name: /^Dune \(2021\), Searching/ })[0];
  fireEvent.click(tile);
  expect(within(dialog()).getByRole("button", { name: "Search now" })).toBeTruthy();
});

test("Esc closes the popup first and keeps the search; the backdrop and Close also close it", async () => {
  setup(popupApi());
  await grid().findAllByRole("button", { name: /house/i });
  type("dune");
  const results = await screen.findByRole("list", { name: /results/i });
  const opener = within(results).getByRole("button", { name: /dune/i });
  opener.focus();
  fireEvent.click(opener);

  fireEvent.keyDown(window, { key: "Escape" });

  expect(screen.queryByRole("dialog")).toBeNull();
  expect((bar() as HTMLInputElement).value).toBe("dune");
  expect(document.activeElement).toBe(opener);

  fireEvent.click(opener);
  fireEvent.click(screen.getByTestId("popup-backdrop"));
  expect(screen.queryByRole("dialog")).toBeNull();

  fireEvent.click(opener);
  fireEvent.click(within(dialog()).getByRole("button", { name: "Close" }));
  expect(screen.queryByRole("dialog")).toBeNull();
});

test("the toast goes away by itself", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    setup(popupApi((url, init) => (init.method === "POST" && url.pathname === "/api/titles" ? json(duneAdded, 201) : json({ error: "nope" }, 404))));
    await openTile(/^Dune \(2021\)/);
    fireEvent.click(within(dialog()).getByRole("button", { name: "Add & monitor" }));
    await screen.findByRole("status");

    await vi.advanceTimersByTimeAsync(6000);

    await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
  } finally {
    vi.useRealTimers();
  }
});

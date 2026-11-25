// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
import { colsOf, Library } from "./Library";
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

const libraryGrid = () => within(grid().getByRole("list", { name: "Library titles" }));
const trendingGrid = () => within(grid().getByRole("list", { name: "Trending titles" }));
const strip = () => within(grid().getByRole("group", { name: /filter the library/i }));
const chip = (name: RegExp) => strip().getByRole("button", { name });
const pressed = () => strip().getAllByRole("button").filter((b) => b.getAttribute("aria-pressed") === "true").map((b) => b.textContent);
const labels = (list: { getAllByRole: (role: "button") => HTMLElement[] }) => list.getAllByRole("button").map((t) => t.getAttribute("aria-label"));
const severance = title({ tmdbId: 9, kind: "series", title: "Severance", year: 2022 });
const withSeverance: Wall = { items: [...wall.items, { source: "trending", title: severance }] };

test("the library grid lists only the library's titles, each with its state as a bar and no pill", async () => {
  setup((url) => (url.pathname === "/api/wall" ? json(wall) : json({ error: "nope" }, 404)));

  await grid().findByRole("list", { name: "Library titles" });
  const tiles = libraryGrid().getAllByRole("button");
  const names = tiles.map((t) => t.getAttribute("aria-label"));
  expect(names).toEqual(["House (2004), Season 4 · 3 missing", "Project Hail Mary (2026), Downloaded"]);
  expect(tiles[0].querySelector<HTMLElement>(".meter")?.dataset.state).toBe("partial_season");
  expect(tiles[1].querySelector(".meter")).toBeNull();
  expect(tiles[0].querySelector(".badge")).toBeNull();
  expect(within(tiles[0]).getByText("House")).toBeTruthy();
  expect(within(tiles[0]).getByText("2004 · Series")).toBeTruthy();
  expect(grid().queryByRole("button", { name: /dune/i })).toBeNull();
  expect(root().dataset.mode).toBe("library");
});

const kinds = () => within(grid().getByRole("group", { name: /movies and series/i }));
const kind = (name: RegExp) => kinds().getByRole("button", { name });
const pressedKinds = () => kinds().getAllByRole("button").filter((b) => b.getAttribute("aria-pressed") === "true").map((b) => b.textContent);

test("the strip is exactly Library, Trending, Downloading and Missing with their counts, and no Movies or Series chip", async () => {
  setup(() => json(withSeverance));
  await grid().findByRole("list", { name: "Library titles" });

  expect(strip().getAllByRole("button").map((c) => c.textContent)).toEqual(["Library2", "Trending2", "Downloading0", "Missing1"]);
  expect(pressed()).toEqual(["Library2"]);
});

test("Library and Trending carry an All, Movies, Series choice; Downloading and Missing carry none", async () => {
  setup(() => json(withSeverance));
  await grid().findByRole("list", { name: "Library titles" });

  expect(kinds().getAllByRole("button").map((b) => b.textContent)).toEqual(["All2", "Movies1", "Series1"]);
  expect(pressedKinds()).toEqual(["All2"]);
  fireEvent.click(chip(/^trending/i));
  expect(kinds().getAllByRole("button").map((b) => b.textContent)).toEqual(["All2", "Movies1", "Series1"]);
  fireEvent.click(chip(/^downloading/i));
  expect(grid().queryByRole("group", { name: /movies and series/i })).toBeNull();
  fireEvent.click(chip(/^missing/i));
  expect(grid().queryByRole("group", { name: /movies and series/i })).toBeNull();
  fireEvent.click(chip(/^library/i));
  expect(grid().getByRole("group", { name: /movies and series/i })).toBeTruthy();
});

test("Movies narrows Library to movies, Series to series, and All shows both", async () => {
  setup(() => json(wall));
  await grid().findByRole("list", { name: "Library titles" });

  fireEvent.click(kind(/^movies/i));
  expect(pressedKinds()).toEqual(["Movies1"]);
  expect(labels(libraryGrid())).toEqual(["Project Hail Mary (2026), Downloaded"]);
  fireEvent.click(kind(/^series/i));
  expect(labels(libraryGrid())).toEqual(["House (2004), Season 4 · 3 missing"]);
  fireEvent.click(kind(/^all/i));
  expect(labels(libraryGrid())).toHaveLength(2);
  expect(pressed()).toEqual(["Library2"]);
});

test("Downloading and Missing filter the library with their own empty lines, and Library brings the library back", async () => {
  setup(() => json(wall));
  await grid().findByRole("list", { name: "Library titles" });

  fireEvent.click(chip(/^missing/i));
  expect(pressed()).toEqual(["Missing1"]);
  expect(labels(libraryGrid())).toEqual(["House (2004), Season 4 · 3 missing"]);
  fireEvent.click(chip(/^downloading/i));
  expect(grid().getByText("Nothing is downloading right now.")).toBeTruthy();
  fireEvent.click(chip(/^library/i));
  expect(pressed()).toEqual(["Library2"]);
  expect(labels(libraryGrid())).toHaveLength(2);
});

test("Trending is its own section: only trending titles, no library title, and Library brings the library back", async () => {
  setup(() => json(withSeverance));
  await grid().findByRole("list", { name: "Library titles" });

  fireEvent.click(chip(/^trending/i));

  expect(grid().queryByRole("list", { name: "Library titles" })).toBeNull();
  expect(labels(trendingGrid())).toEqual(["Dune (2021)", "Severance (2022)"]);
  expect(pressed()).toEqual(["Trending2"]);
  fireEvent.click(chip(/^library/i));
  expect(grid().queryByRole("list", { name: "Trending titles" })).toBeNull();
  expect(labels(libraryGrid())).toEqual(["House (2004), Season 4 · 3 missing", "Project Hail Mary (2026), Downloaded"]);
});

test("each section remembers its own Movies, Series or All choice while you move between sections", async () => {
  setup(() => json(withSeverance));
  await grid().findByRole("list", { name: "Library titles" });

  fireEvent.click(kind(/^series/i));
  expect(labels(libraryGrid())).toEqual(["House (2004), Season 4 · 3 missing"]);
  fireEvent.click(chip(/^trending/i));
  expect(pressedKinds()).toEqual(["All2"]);
  expect(labels(trendingGrid())).toEqual(["Dune (2021)", "Severance (2022)"]);
  fireEvent.click(kind(/^movies/i));
  expect(labels(trendingGrid())).toEqual(["Dune (2021)"]);
  fireEvent.click(chip(/^missing/i));
  fireEvent.click(chip(/^library/i));
  expect(pressedKinds()).toEqual(["Series1"]);
  expect(labels(libraryGrid())).toEqual(["House (2004), Season 4 · 3 missing"]);
  fireEvent.click(chip(/^trending/i));
  expect(pressedKinds()).toEqual(["Movies1"]);
  expect(labels(trendingGrid())).toEqual(["Dune (2021)"]);
});

test("the chip counts stay the section totals whichever kind is chosen", async () => {
  setup(() => json(withSeverance));
  await grid().findByRole("list", { name: "Library titles" });

  fireEvent.click(kind(/^movies/i));

  expect(strip().getAllByRole("button").map((c) => c.textContent)).toEqual(["Library2", "Trending2", "Downloading0", "Missing1"]);
});

test("an empty library says so in its own section while Trending still holds its titles", async () => {
  setup(() => json({ items: [{ source: "trending", title: dune }] }));

  await grid().findByText("Nothing here yet.");
  expect(grid().queryByRole("list", { name: "Library titles" })).toBeNull();
  fireEvent.click(chip(/^trending/i));
  expect(labels(trendingGrid())).toEqual(["Dune (2021)"]);
});

test("a wall with no trending titles says so in the Trending section, per kind", async () => {
  setup(() => json({ items: [{ source: "library", title: hail }] }));
  await grid().findByRole("list", { name: "Library titles" });

  fireEvent.click(chip(/^trending/i));

  expect(grid().getByText("No trending titles right now.")).toBeTruthy();
  expect(chip(/^trending/i).textContent).toBe("Trending0");
  fireEvent.click(kind(/^series/i));
  expect(grid().getByText("No trending series right now.")).toBeTruthy();
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

test("a library tile of a title with no release year shows no 0 in its meta line or name", () => {
  const api = createApi({ baseUrl: BASE, fetch: (async () => json(wall)) as unknown as typeof fetch });
  const marley = title({ tmdbId: 7, kind: "movie", title: "Marley", year: 0, inLibrary: true, badge: downloaded });
  const bare = title({ tmdbId: 8, kind: "series", title: "Bare", year: 0, inLibrary: true });
  render(<Library api={api} items={[marley, bare]} />);

  expect(screen.getByRole("button", { name: "Marley, Downloaded" }).querySelector(".cell-meta")!.textContent).toBe("Movie");
  expect(screen.getByRole("button", { name: "Bare" }).querySelector(".cell-meta")!.textContent).toBe("Series");
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

const pageWall: Wall = {
  items: [
    { source: "library", title: house },
    { source: "trending", title: dunePage },
  ],
};

const notFound = () => json({ error: "no such title in the library" }, 404);

function pageApi(extra: Handler = notFound): Handler {
  return (url, init) => {
    if (url.pathname === "/api/wall") return json(pageWall);
    if (url.pathname === "/api/search") return json({ results: [dunePage, house] });
    return extra(url, init);
  };
}

const isAdd = (url: URL, init: Request) => init.method === "POST" && url.pathname === "/api/titles";
const addAnswering = (answer: () => Response | Promise<Response>): Handler => (url, init) => (isAdd(url, init) ? answer() : notFound());

const openTile = async (name: RegExp) => {
  await grid().findByRole("list", { name: "Library titles" });
  if (!grid().queryByRole("button", { name })) fireEvent.click(chip(/^trending/i));
  fireEvent.click((await grid().findAllByRole("button", { name }))[0]);
};
const page = () => within(screen.getByRole("main"));

test("clicking a wall tile opens the title's page with poster, year, kind and summary, and adds nothing", async () => {
  const { writes } = setup(pageApi());

  await openTile(/^Dune \(2021\)/);

  expect(await screen.findByRole("heading", { name: "Dune", level: 1 })).toBeTruthy();
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(window.location.hash).toBe("#/title/movie-1");
  expect(page().getByText("2021 · Movie")).toBeTruthy();
  expect(page().getByText("Paul Atreides leads the Fremen.")).toBeTruthy();
  expect(screen.getByRole("main").querySelector(".ts-poster img")!.getAttribute("src")).toBe(`${BASE}/api/images/poster/movie-1`);
  expect(page().getByRole("button", { name: "Add & monitor" })).toBeTruthy();
  expect(page().getByRole("heading", { name: "Releases" })).toBeTruthy();
  expect(writes).toEqual([]);
});

test("a trending title opened from the Trending section offers Add & monitor and no Remove", async () => {
  setup(pageApi());

  fireEvent.click(await grid().findByRole("button", { name: /^trending/i }));
  fireEvent.click(trendingGrid().getByRole("button", { name: /^Dune \(2021\)/ }));

  expect(await screen.findByRole("heading", { name: "Dune", level: 1 })).toBeTruthy();
  expect(page().getByRole("button", { name: "Add & monitor" })).toBeTruthy();
  expect(page().queryByRole("button", { name: "Remove…" })).toBeNull();
  expect(page().queryByRole("group", { name: "Remove from marquee" })).toBeNull();
});

test("a library title removed in this session never turns up in Trending", async () => {
  const gone = (url: URL, init: Request) => {
    if (init.method === "DELETE" && url.pathname === "/api/titles/series-2") return new Response(null, { status: 204 });
    if (url.pathname === "/api/titles/series-2") return json(houseOut);
    return notFound();
  };
  setup(pageApi(gone));
  fireEvent.click(await grid().findByRole("button", { name: /^House \(2004\)/ }));
  const group = await screen.findByRole("group", { name: "Remove from marquee" });
  fireEvent.click(within(group).getByRole("button", { name: "Remove…" }));
  fireEvent.click(within(group).getByRole("button", { name: "Yes, remove “House”" }));
  await waitFor(() => expect(window.location.hash).toBe("#/grid"));

  expect(strip().getAllByRole("button").map((c) => c.textContent)).toEqual(["Library0", "Trending1", "Downloading0", "Missing0"]);
  fireEvent.click(chip(/^trending/i));
  expect(labels(trendingGrid())).toEqual(["Dune (2021)"]);
  expect(grid().queryByRole("button", { name: /house/i })).toBeNull();
});

test("a trending title that is added leaves Trending and joins the library", async () => {
  setup(pageApi(addAnswering(() => json(duneAdded, 201))));
  fireEvent.click(await grid().findByRole("button", { name: /^trending/i }));
  fireEvent.click(trendingGrid().getByRole("button", { name: /^Dune \(2021\)/ }));
  fireEvent.click(await within(await screen.findByRole("main")).findByRole("button", { name: "Add & monitor" }));
  await screen.findByRole("group", { name: "Remove from marquee" });
  fireEvent.keyDown(window, { key: "Escape" });
  await waitFor(() => expect(window.location.hash).toBe("#/grid"));

  expect(grid().getByText("No trending titles right now.")).toBeTruthy();
  fireEvent.click(chip(/^library/i));
  expect(labels(libraryGrid())).toEqual(expect.arrayContaining([expect.stringMatching(/^Dune \(2021\)/)]));
});

test("clicking a search result opens the title's page and adds nothing", async () => {
  const { writes } = setup(pageApi());
  await grid().findAllByRole("button", { name: /house/i });
  type("dune");
  const results = await screen.findByRole("list", { name: /results/i });

  fireEvent.click(within(results).getByRole("button", { name: /dune/i }));

  expect(await screen.findByRole("heading", { name: "Dune", level: 1 })).toBeTruthy();
  expect(writes).toEqual([]);
});

test("Add & monitor adds the title, confirms with a toast and the page becomes the library title's", async () => {
  const { writes } = setup(pageApi(addAnswering(() => json(duneAdded, 201))));
  await openTile(/^Dune \(2021\)/);

  fireEvent.click(await page().findByRole("button", { name: "Add & monitor" }));

  expect((await screen.findByRole("status")).textContent).toMatch(/added.*dune/i);
  expect(writes).toEqual([{ method: "POST", path: "/api/titles", body: { kind: "movie", tmdbId: 1 } }]);
  expect(page().getByRole("button", { name: "Search now" })).toBeTruthy();
  expect(page().queryByRole("button", { name: "Add & monitor" })).toBeNull();
  expect(page().getByRole("group", { name: "Remove from marquee" })).toBeTruthy();
});

test("pressing Add & monitor twice quickly sends one request", async () => {
  let release!: () => void;
  const hold = new Promise<void>((r) => (release = r));
  const { writes } = setup(
    pageApi(
      addAnswering(async () => {
        await hold;
        return json(duneAdded, 201);
      }),
    ),
  );
  await openTile(/^Dune \(2021\)/);
  const add = await page().findByRole("button", { name: "Add & monitor" });

  fireEvent.click(add);
  fireEvent.click(add);
  release();

  await page().findByRole("button", { name: "Search now" });
  expect(writes).toHaveLength(1);
});

test("a title already in the library opens with Search now, which searches it", async () => {
  const { writes } = setup(
    pageApi((url, init) => {
      if (init.method === "POST" && url.pathname === "/api/titles/series-2/search") return json(houseOut);
      if (url.pathname === "/api/titles/series-2") return json(houseOut);
      return notFound();
    }),
  );

  await openTile(/^House \(2004\)/);
  await screen.findByRole("heading", { name: "House", level: 1 });
  expect(page().queryByRole("button", { name: "Add & monitor" })).toBeNull();
  fireEvent.click(page().getByRole("button", { name: "Search now" }));

  expect((await screen.findByRole("status")).textContent).toMatch(/searching.*house/i);
  expect(writes).toEqual([{ method: "POST", path: "/api/titles/series-2/search", body: undefined }]);
});

test("Search now on a Missing library title moves its tile badge to Searching once the page is left", async () => {
  const missing = { ...house, badge: { state: "missing" as const, label: "Missing" } };
  const searching = { ...missing, badge: { state: "searching" as const, label: "Searching" }, seasons: [], historySummary: "Nothing tried yet." };
  setup((url, init) => {
    if (url.pathname === "/api/wall") return json({ items: [{ source: "library", title: missing }] });
    if (init.method === "POST" && url.pathname === "/api/titles/series-2/search") return json(searching);
    if (url.pathname === "/api/titles/series-2") return json({ ...missing, seasons: [], historySummary: "Nothing tried yet." });
    return notFound();
  });
  await openTile(/^House \(2004\), Missing/);
  await screen.findByRole("heading", { name: "House", level: 1 });

  fireEvent.click(page().getByRole("button", { name: "Search now" }));
  await screen.findByRole("status");
  fireEvent.click(screen.getByRole("button", { name: "Back" }));

  await waitFor(() => expect(screen.getAllByRole("button", { name: /^House \(2004\), Searching/ }).length).toBeGreaterThan(0));
  expect(screen.queryByRole("button", { name: /^House \(2004\), Missing/ })).toBeNull();
});

test("a failed add shows the API's message on the page and can be tried again", async () => {
  let up = false;
  setup(pageApi(addAnswering(() => (up ? json(duneAdded, 201) : json({ error: "TMDB did not answer" }, 502)))));
  await openTile(/^Dune \(2021\)/);

  fireEvent.click(await page().findByRole("button", { name: "Add & monitor" }));

  expect((await page().findByRole("alert")).textContent).toContain("TMDB did not answer");
  expect(screen.queryByRole("status")).toBeNull();
  up = true;
  fireEvent.click(page().getByRole("button", { name: "Add & monitor" }));
  await page().findByRole("button", { name: "Search now" });
  expect(page().queryByRole("alert")).toBeNull();
});

test("an added title reads as in the library on its tile and when its page is reopened", async () => {
  let added = false;
  setup(
    pageApi((url, init) => {
      if (isAdd(url, init)) {
        added = true;
        return json(duneAdded, 201);
      }
      if (url.pathname === "/api/titles/movie-1" && added) return json(duneAdded);
      return notFound();
    }),
  );
  await openTile(/^Dune \(2021\)/);
  fireEvent.click(await page().findByRole("button", { name: "Add & monitor" }));
  await page().findByRole("button", { name: "Search now" });

  fireEvent.click(screen.getByRole("button", { name: "Back" }));

  await waitFor(() => expect(screen.queryByRole("main")).toBeNull());
  const tile = screen.getAllByRole("button", { name: /^Dune \(2021\), Searching/ })[0];
  fireEvent.click(tile);
  expect(await page().findByRole("button", { name: "Search now" })).toBeTruthy();
});

test("Esc closes the page first and keeps the search", async () => {
  setup(pageApi());
  await grid().findAllByRole("button", { name: /house/i });
  type("dune");
  const results = await screen.findByRole("list", { name: /results/i });
  fireEvent.click(within(results).getByRole("button", { name: /dune/i }));
  await screen.findByRole("heading", { name: "Dune", level: 1 });

  fireEvent.keyDown(window, { key: "Escape" });

  await waitFor(() => expect(screen.queryByRole("main")).toBeNull());
  expect((bar() as HTMLInputElement).value).toBe("dune");
});

test("the toast goes away by itself", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    setup(pageApi(addAnswering(() => json(duneAdded, 201))));
    await openTile(/^Dune \(2021\)/);
    fireEvent.click(await page().findByRole("button", { name: "Add & monitor" }));
    await screen.findByRole("status");

    await vi.advanceTimersByTimeAsync(6000);

    await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
  } finally {
    vi.useRealTimers();
  }
});

test("Esc on a focused toast dismisses the toast and leaves the page open", async () => {
  setup(pageApi(addAnswering(() => json(duneAdded, 201))));
  await openTile(/^Dune \(2021\)/);
  fireEvent.click(await page().findByRole("button", { name: "Add & monitor" }));
  await screen.findByRole("status");

  fireEvent.keyDown(screen.getByRole("button", { name: "Dismiss" }), { key: "Escape" });

  await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
  expect(window.location.hash).toBe("#/title/movie-1");
  expect(screen.getByRole("main")).toBeTruthy();
});

test("the toast's X dismisses it", async () => {
  setup(pageApi(addAnswering(() => json(duneAdded, 201))));
  await openTile(/^Dune \(2021\)/);
  fireEvent.click(await page().findByRole("button", { name: "Add & monitor" }));
  await screen.findByRole("status");

  fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));

  await waitFor(() => expect(screen.queryByRole("status")).toBeNull());
});

test("the clear button empties the search and brings the library back", async () => {
  setup((url) => (url.pathname === "/api/wall" ? json(wall) : json({ results: [dune] })));
  await grid().findAllByRole("button", { name: /house/i });
  type("dune");
  await screen.findByRole("list", { name: /results/i });

  fireEvent.click(screen.getByRole("button", { name: "Clear search" }));

  expect((bar() as HTMLInputElement).value).toBe("");
  await waitFor(() => expect(root().dataset.mode).toBe("library"));
  expect(screen.queryByRole("button", { name: "Clear search" })).toBeNull();
});

test("clicking a section moves the pressed state to it", async () => {
  setup(() => json(wall));
  await grid().findAllByRole("button", { name: /house/i });

  expect(pressed()).toEqual(["Library2"]);
  fireEvent.click(chip(/^missing/i));
  expect(pressed()).toEqual(["Missing1"]);
});

test("the library shows a skeleton while the wall loads, then the titles", async () => {
  let resolveWall: (r: Response) => void = () => {};
  const pending = new Promise<Response>((r) => (resolveWall = r));
  setup((url) => (url.pathname === "/api/wall" ? pending : json({ error: "nope" }, 404)));

  const stage = document.querySelector<HTMLElement>(".library-stage")!;
  await waitFor(() => expect(stage.querySelectorAll(".sk").length).toBeGreaterThan(0));
  expect(within(stage).getByRole("status").textContent).toBe("Loading the library");
  expect(stage.textContent).not.toMatch(/loading…|searching…/i);

  resolveWall(json(wall));
  await grid().findAllByRole("button", { name: /house/i });
  expect(stage.querySelectorAll(".sk")).toHaveLength(0);
});

test("a wall that failed says why with one way to try again", async () => {
  let fail = true;
  setup((url) => (url.pathname === "/api/wall" ? (fail ? json({ error: "TMDB did not answer" }, 502) : json(wall)) : json({ error: "nope" }, 404)));
  const stage = document.querySelector<HTMLElement>(".library-stage")!;
  await waitFor(() => expect(within(stage).getByRole("alert").textContent).toContain("TMDB did not answer"));

  fail = false;
  fireEvent.click(within(stage).getByRole("button", { name: "Try again" }));

  await grid().findAllByRole("button", { name: /house/i });
  expect(within(stage).queryByRole("alert")).toBeNull();
});

test("colsOf caps grid column calculation at 1760px to match the stage max-width", () => {
  expect(colsOf(3200)).toBe(colsOf(1760));
  expect(colsOf(2400)).toBe(colsOf(1760));
});

test("search shows skeleton placeholders while results are loading", async () => {
  let resolveSearch: (r: Response) => void = () => {};
  const pending = new Promise<Response>((r) => (resolveSearch = r));
  setup((url) => {
    if (url.pathname === "/api/wall") return json(wall);
    if (url.pathname === "/api/search") return pending;
    return json({ checks: [] });
  });
  await grid().findAllByRole("button", { name: /house/i });

  type("dune");

  await waitFor(() => expect(root().dataset.mode).toBe("search"));
  const resultsStage = document.querySelector<HTMLElement>(".results-stage")!;
  expect(resultsStage.querySelectorAll(".sk").length).toBeGreaterThan(0);

  resolveSearch(json({ results: [dune] }));
  await screen.findByRole("list", { name: /results/i });
  expect(resultsStage.querySelectorAll(".sk")).toHaveLength(0);
});

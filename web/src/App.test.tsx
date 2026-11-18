// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { App } from "./App";
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

function setup(handler: Handler) {
  const calls: URL[] = [];
  const fetchStub = vi.fn(async (input: Request) => {
    const url = new URL(input.url);
    calls.push(url);
    return handler(url, input);
  });
  const api = createApi({ baseUrl: BASE, fetch: fetchStub as unknown as typeof fetch });
  render(<App api={api} />);
  return { calls };
}

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

test("the wall shows the API's tiles in order with their badges and same-origin images", async () => {
  setup((url) => (url.pathname === "/api/wall" ? json(wall) : json({ error: "nope" }, 404)));

  const tiles = await screen.findAllByRole("button", { name: /house|hail mary|dune/i });
  const names = tiles.map((t) => t.getAttribute("aria-label"));
  expect(names.slice(0, 3)).toEqual(["House (2004), Season 4 · 3 missing", "Project Hail Mary (2026), Downloaded", "Dune (2021)"]);
  expect(within(tiles[0]).getByText("Season 4 · 3 missing")).toBeTruthy();
  expect(within(tiles[2]).queryByText(/downloaded|missing/i)).toBeNull();
  const img = tiles[0].querySelector("img")!;
  expect(img.getAttribute("src")).toMatch(new RegExp(`^${BASE}/api/images/(poster|backdrop)/series-2$`));
  expect(root().dataset.mode).toBe("wall");
});

test("portrait tiles ask for the poster and wide tiles for the backdrop", async () => {
  setup(() => json(wall));
  await screen.findAllByRole("button", { name: /house/i });
  const kinds = new Set(Array.from(document.querySelectorAll(".tile img")).map((i) => /\/(poster|backdrop)\//.exec(i.getAttribute("src")!)![1]));
  expect(kinds).toEqual(new Set(["poster", "backdrop"]));
  for (const tile of Array.from(document.querySelectorAll<HTMLElement>(".tile"))) {
    const want = tile.dataset.shape === "portrait" ? "poster" : "backdrop";
    expect(tile.querySelector("img")!.getAttribute("src")).toContain(`/${want}/`);
  }
});

test("typing switches to search mode, shows results with year, kind and badge", async () => {
  const { calls } = setup((url) => {
    if (url.pathname === "/api/wall") return json(wall);
    return json({ results: [dune, house] });
  });
  await screen.findAllByRole("button", { name: /house/i });

  type("du");

  await waitFor(() => expect(root().dataset.mode).toBe("search"));
  const results = await screen.findByRole("list", { name: /results/i });
  const items = within(results).getAllByRole("listitem");
  expect(items).toHaveLength(2);
  expect(within(items[0]).getByText("Dune")).toBeTruthy();
  expect(within(items[0]).getByText("2021 · movie")).toBeTruthy();
  expect(within(items[0]).queryByText(/downloaded|missing/i)).toBeNull();
  expect(within(items[1]).getByText("2004 · series")).toBeTruthy();
  expect(within(items[1]).getByText("Season 4 · 3 missing")).toBeTruthy();
  expect(items[0].querySelector("img")!.getAttribute("src")).toBe(`${BASE}/api/images/poster/movie-1`);
  expect(calls.filter((u) => u.pathname === "/api/search").map((u) => u.searchParams.get("q"))).toEqual(["du"]);
});

test("Esc clears the search and brings the wall back", async () => {
  setup((url) => (url.pathname === "/api/wall" ? json(wall) : json({ results: [dune] })));
  await screen.findAllByRole("button", { name: /house/i });
  type("dune");
  await screen.findByRole("list", { name: /results/i });

  fireEvent.keyDown(window, { key: "Escape" });

  expect(root().dataset.mode).toBe("wall");
  expect((bar() as HTMLInputElement).value).toBe("");
  expect(screen.queryByRole("list", { name: /results/i })).toBeNull();
  expect(document.activeElement).toBe(bar());
});

test("a blank query never reaches the API and stays on the wall", async () => {
  const { calls } = setup(() => json(wall));
  await screen.findAllByRole("button", { name: /house/i });

  type("   ");
  await new Promise((r) => setTimeout(r, 350));

  expect(root().dataset.mode).toBe("wall");
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
  await screen.findAllByRole("button", { name: /house/i });

  type("du");
  await new Promise((r) => setTimeout(r, 300));
  type("dune");
  await screen.findByText("Dune");
  release();
  await new Promise((r) => setTimeout(r, 50));

  const results = screen.getByRole("list", { name: /results/i });
  expect(within(results).getAllByRole("listitem")).toHaveLength(1);
  expect(within(results).queryByText("House")).toBeNull();
});

test("no matches say so", async () => {
  setup((url) => (url.pathname === "/api/wall" ? json(wall) : json({ results: [] })));
  await screen.findAllByRole("button", { name: /house/i });
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
  await screen.findAllByRole("button", { name: /house/i });

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

test("an empty wall still shows the search bar", async () => {
  setup(() => json({ items: [] }));
  expect(await screen.findByText(/nothing to show yet/i)).toBeTruthy();
  expect(bar()).toBeTruthy();
});

test("a poster that fails to load falls back to the title instead of a broken image", async () => {
  setup((url) => (url.pathname === "/api/wall" ? json(wall) : json({ results: [dune] })));
  const [tile] = await screen.findAllByRole("button", { name: /house/i });

  fireEvent.error(tile.querySelector("img")!);

  expect(tile.querySelector("img")).toBeNull();
  expect(within(tile).getByText("House")).toBeTruthy();
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

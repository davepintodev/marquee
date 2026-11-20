// @vitest-environment jsdom
import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { json, setupApp, BASE, type Handler } from "./testkit";
import type { TitleSummary } from "./api/types";
import { mostColourful, type Tint } from "./tint";

const tintOfFake = vi.hoisted(() => vi.fn());
vi.mock("./tint", async (original) => ({ ...(await original<typeof import("./tint")>()), tintOf: tintOfFake }));

const swatch = (src: string): Tint => {
  const hue = [...src].reduce((n, c) => (n * 31 + c.charCodeAt(0)) % 360, 7);
  return { h: hue, s: 40, mass: src.includes("series-") ? 50 : 1 + (hue % 10) };
};

function title(tmdbId: number, kind: "movie" | "series", name: string, over: Partial<TitleSummary> = {}): TitleSummary {
  return {
    id: `${kind}-${tmdbId}`,
    tmdbId,
    kind,
    title: name,
    year: 2020,
    overview: "",
    posterPath: `/api/images/poster/${kind}-${tmdbId}`,
    backdropPath: `/api/images/backdrop/${kind}-${tmdbId}`,
    inLibrary: false,
    ...over,
  };
}

const house = title(2, "series", "House", { year: 2004, inLibrary: true, monitored: true, badge: { state: "partial_season", label: "Season 4 · 3 missing", season: 4, missingCount: 3 } });
const hail = title(3, "movie", "Project Hail Mary", { year: 2026, inLibrary: true, badge: { state: "downloading", label: "Downloading 40%", percent: 40 } });
const dune = title(1, "movie", "Dune", { year: 2021 });
const wall = { items: [house, hail, dune].map((t) => ({ source: t.inLibrary ? "library" : "trending", title: t })) };

const serve =
  (items: unknown = wall, extra: Handler = () => json({ results: [dune] })): Handler =>
  (url, init) => (url.pathname === "/api/wall" ? (items instanceof Response ? items : json(items)) : extra(url, init));

const onlyPages = serve(wall, (url) => (url.pathname.startsWith("/api/titles/") ? json({ error: "no such title in the library" }, 404) : json({ results: [dune] })));

const front = () => document.querySelector<HTMLElement>(".front")!;
const app = () => document.querySelector<HTMLElement>(".app")!;
const words = () => within(front()).getByRole("button", { name: /what are we watching tonight\?/i });
const arrow = () => front().querySelector<HTMLButtonElement>(".slot-arrow")!;
const searchbox = () => screen.getByRole("searchbox", { name: /search movies and series/i });
const posters = () => Array.from(front().querySelectorAll<HTMLButtonElement>(".ptile"));
const onWall = () => expect(app().dataset.screen).toBe("front");
const inApp = () => expect(app().dataset.screen).toBe("grid");

beforeEach(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  tintOfFake.mockImplementation(async (src: string) => swatch(src));
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.location.hash = "";
});

async function wallUp(handler: Handler = serve(), hash = "") {
  const world = setupApp(handler, hash);
  await waitFor(() => expect(posters().length).toBeGreaterThan(5));
  return world;
}

test("the front is posters and the words only: no badges, bars, links, filters or search", async () => {
  await wallUp();

  onWall();
  expect(words().textContent).toBe("What are wewatching tonight?");
  expect(front().querySelector(".slot-words em")!.textContent).toBe("tonight?");
  expect(front().querySelector(".badge, .meter, .chips, .nav, .state-line, input")).toBeNull();
  expect(within(front()).queryAllByRole("link")).toEqual([]);
  expect(front().hasAttribute("inert")).toBe(false);
  expect(document.querySelector(".grid-screen")!.hasAttribute("inert")).toBe(true);
});

test("poster tiles are named by title and year, portrait ones show the poster and wide ones the backdrop", async () => {
  await wallUp();

  const names = new Set(posters().map((t) => t.getAttribute("aria-label")));
  expect(names).toEqual(new Set(["House (2004), Season 4 · 3 missing", "Project Hail Mary (2026), Downloading 40%", "Dune (2021)"]));
  const kinds = new Set<string>();
  for (const tile of posters()) {
    const want = tile.dataset.shape === "portrait" ? "poster" : "backdrop";
    const src = tile.querySelector("img")!.getAttribute("src")!;
    expect(src).toMatch(new RegExp(`^${BASE}/api/images/${want}/(movie|series)-\\d$`));
    kinds.add(want);
  }
  expect(kinds).toEqual(new Set(["poster", "backdrop"]));
});

test("no poster sits next to its own twin when the titles repeat", async () => {
  const many = Array.from({ length: 14 }, (_, i) => ({ source: "trending", title: title(100 + i, "movie", `Film ${i}`) }));
  await wallUp(serve({ items: many }));
  expect(posters().length).toBeGreaterThan(14);

  const boxes = posters().map((t) => ({ name: t.getAttribute("aria-label"), x: parseFloat(t.style.left), y: parseFloat(t.style.top), w: parseFloat(t.style.width), h: parseFloat(t.style.height) }));
  boxes.forEach((a, i) =>
    boxes.slice(i + 1).forEach((b) => {
      if (a.name !== b.name) return;
      const apart = a.x + a.w + 2 < b.x || b.x + b.w + 2 < a.x || a.y + a.h + 2 < b.y || b.y + b.h + 2 < a.y;
      expect(apart).toBe(true);
    }),
  );
});

test("the wall reads as loading, then shows still placeholders when there is nothing to show", async () => {
  let release!: () => void;
  const held = new Promise<void>((r) => (release = r));
  setupApp(async (url) => {
    await held;
    return url.pathname === "/api/wall" ? json({ items: [] }) : json({ error: "nope" }, 404);
  }, "");
  expect(screen.getByLabelText("Posters").getAttribute("aria-busy")).toBe("true");
  expect(front().querySelectorAll(".pplaceholder").length).toBeGreaterThan(5);

  release();
  await within(front()).findByText("Nothing to show yet.");
  expect(screen.getByLabelText("Posters").getAttribute("aria-busy")).toBe("false");
  expect(within(front()).queryByRole("button", { name: /what are we watching/i })).toBeNull();
  fireEvent.click(within(front()).getByRole("button", { name: "Add a title" }));
  inApp();
});

test("a wall that failed to load says why on the front and Try again reloads it", async () => {
  let up = false;
  const { calls } = setupApp((url) => (url.pathname !== "/api/wall" ? json({ error: "nope" }, 404) : up ? json(wall) : json({ error: "this part of marquee is not built yet" }, 501)), "");
  expect((await within(front()).findByRole("alert")).textContent).toContain("not built yet");
  up = true;

  fireEvent.click(within(front()).getByRole("button", { name: /try again/i }));

  await waitFor(() => expect(posters().length).toBeGreaterThan(5));
  expect(within(front()).queryByRole("alert")).toBeNull();
  expect(calls.filter((u) => u.pathname === "/api/wall")).toHaveLength(2);
});

test("clicking the words opens the app screen, with the search field focused", async () => {
  await wallUp();

  fireEvent.click(words());

  inApp();
  expect(window.location.hash).toBe("#/grid");
  expect(front().hasAttribute("inert")).toBe(true);
  await waitFor(() => expect(document.activeElement).toBe(searchbox()));
  expect(searchbox().getAttribute("aria-label")).toMatch(/search/i);
  expect((searchbox() as HTMLInputElement).value).toBe("");
});

test("clicking the arrow opens the app screen too, but the arrow is not a tab stop", async () => {
  await wallUp();
  expect(arrow().tabIndex).toBe(-1);
  expect(arrow().getAttribute("aria-hidden")).toBe("true");

  fireEvent.click(arrow());

  inApp();
});

test("Enter opens the app screen", async () => {
  await wallUp();

  fireEvent.keyDown(window, { key: "Enter" });

  inApp();
  expect((searchbox() as HTMLInputElement).value).toBe("");
});

test.each([" ", "/"])("%j opens the app screen with an empty search", async (key) => {
  await wallUp();

  fireEvent.keyDown(window, { key });

  inApp();
  expect((searchbox() as HTMLInputElement).value).toBe("");
});

test("typing a letter opens the app screen and seeds the search with it", async () => {
  const { calls } = await wallUp();

  fireEvent.keyDown(window, { key: "d" });

  inApp();
  expect((searchbox() as HTMLInputElement).value).toBe("d");
  expect(document.activeElement).toBe(searchbox());
  await waitFor(() => expect(calls.filter((u) => u.pathname === "/api/search").map((u) => u.searchParams.get("q"))).toEqual(["d"]));
});

test("keys with Ctrl, Meta or Alt, and keys that are not characters, leave the wall alone", async () => {
  await wallUp();

  for (const init of [{ key: "d", ctrlKey: true }, { key: "d", metaKey: true }, { key: "d", altKey: true }, { key: "Shift" }, { key: "ArrowDown" }, { key: "Tab" }]) {
    fireEvent.keyDown(window, init);
  }

  onWall();
  expect(window.location.hash).toBe("");
});

test("Enter or Space on a focused poster belongs to the poster: it opens the title's page, not the app", async () => {
  await wallUp(onlyPages);
  const tile = posters()[0];
  tile.focus();

  fireEvent.keyDown(tile, { key: "Enter" });
  fireEvent.keyDown(tile, { key: " " });
  onWall();
  fireEvent.click(tile);

  await screen.findByRole("main");
  expect(window.location.hash).toBe(`#/title/${tile.dataset.tid}`);
});

test("Esc on the wall does nothing", async () => {
  await wallUp();

  fireEvent.keyDown(window, { key: "Escape" });

  onWall();
  expect(window.location.hash).toBe("");
});

test("Esc in the app returns to the wall, and so does the browser's back", async () => {
  await wallUp();
  fireEvent.click(words());
  inApp();

  fireEvent.keyDown(window, { key: "Escape" });
  await waitFor(onWall);

  fireEvent.click(words());
  inApp();
  act(() => window.history.back());
  await waitFor(onWall);
});

test("back on the wall no control of the app keeps the keyboard focus, so Enter and letters walk in again", async () => {
  await wallUp();
  fireEvent.click(words());
  await waitFor(() => expect(document.activeElement).toBe(searchbox()));

  fireEvent.keyDown(window, { key: "Escape" });
  await waitFor(onWall);

  expect(document.activeElement).toBe(document.body);
  fireEvent.keyDown(document.activeElement!, { key: "Enter" });
  inApp();
});

test("opening the app by its address, Esc still goes to the wall", async () => {
  await wallUp(serve(), "#/grid");
  inApp();

  fireEvent.keyDown(window, { key: "Escape" });

  await waitFor(onWall);
});

test("Esc closes the top layer first: the title's page, then the search, then the app", async () => {
  await wallUp(onlyPages);
  fireEvent.click(words());
  fireEvent.change(searchbox(), { target: { value: "dune" } });
  const results = await screen.findByRole("list", { name: /results/i });
  fireEvent.click(within(results).getByRole("button", { name: /dune/i }));
  await screen.findByRole("heading", { name: "Dune", level: 1 });

  fireEvent.keyDown(window, { key: "Escape" });
  await waitFor(() => expect(screen.queryByRole("heading", { name: "Dune", level: 1 })).toBeNull());
  expect((searchbox() as HTMLInputElement).value).toBe("dune");
  inApp();

  fireEvent.keyDown(window, { key: "Escape" });
  expect((searchbox() as HTMLInputElement).value).toBe("");
  inApp();

  fireEvent.keyDown(window, { key: "Escape" });
  await waitFor(onWall);
});

test("Esc closes a page first and lands back in the app; a second Esc goes to the wall", async () => {
  await wallUp(serve(wall, (url) => (url.pathname === "/api/health" ? json({ status: "ok", dryRun: false, checks: [], libraryTotals: { movies: 0, episodes: 0 }, libraryProblems: [] }) : json({ error: "nope" }, 404))));
  fireEvent.click(words());
  fireEvent.click(await screen.findByRole("link", { name: "Health" }));
  await screen.findByRole("heading", { name: "Health" });
  expect(window.location.hash).toBe("#/health");

  fireEvent.keyDown(window, { key: "Escape" });
  await waitFor(() => expect(screen.queryByRole("heading", { name: "Health" })).toBeNull());
  expect(window.location.hash).toBe("#/grid");
  inApp();

  fireEvent.keyDown(window, { key: "Escape" });
  await waitFor(onWall);
});

test("clicking a poster on the wall opens the title's own page over the app, and the wall is inert behind it", async () => {
  await wallUp(onlyPages);

  fireEvent.click(posters().find((t) => /^Dune/.test(t.getAttribute("aria-label")!))!);

  expect(await screen.findByRole("heading", { name: "Dune", level: 1 })).toBeTruthy();
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(window.location.hash).toBe("#/title/movie-1");
  expect(front().hasAttribute("inert")).toBe(true);
});

const houseDetail = { ...house, seasons: [], historySummary: "Nothing tried yet." };
const withDetail = serve(wall, (url) => (url.pathname === "/api/titles/series-2" ? json(houseDetail) : json({ error: "nope" }, 404)));

test("a title opened from the wall goes back to the library grid, and the browser's back then reaches the wall, not the title", async () => {
  await wallUp(withDetail);
  fireEvent.click(posters().find((t) => /^House/.test(t.getAttribute("aria-label")!))!);
  await screen.findByRole("heading", { name: "House", level: 1 });
  expect(window.location.hash).toBe("#/title/series-2");

  fireEvent.keyDown(window, { key: "Escape" });
  await waitFor(() => expect(window.location.hash).toBe("#/grid"));
  inApp();
  expect(screen.queryByRole("heading", { name: "House", level: 1 })).toBeNull();

  act(() => window.history.back());
  await waitFor(onWall);
  expect(window.location.hash).not.toMatch(/^#\/title/);
});

test("a title opened from the library grid goes back to the grid", async () => {
  await wallUp(withDetail);
  fireEvent.click(words());
  inApp();
  fireEvent.click(within(document.querySelector<HTMLElement>(".library-stage")!).getByRole("button", { name: /^House/ }));
  await screen.findByRole("heading", { name: "House", level: 1 });

  fireEvent.click(screen.getByRole("button", { name: "Back" }));

  await waitFor(() => expect(window.location.hash).toBe("#/grid"));
  inApp();
  expect(screen.queryByRole("heading", { name: "House", level: 1 })).toBeNull();
});

test("typing while a title's page is open does not start a search or leave the page", async () => {
  await wallUp(onlyPages);
  fireEvent.click(posters()[0]);
  await screen.findByRole("main");
  const hash = window.location.hash;

  fireEvent.keyDown(window, { key: "d" });

  expect(window.location.hash).toBe(hash);
  expect(screen.getByRole("main")).toBeTruthy();
});

const hueOf = (slot: HTMLElement) => parseFloat(slot.style.getPropertyValue("--th"));
const rect = (el: HTMLElement) => ({ x: parseFloat(el.style.left), y: parseFloat(el.style.top), w: parseFloat(el.style.width), h: parseFloat(el.style.height) });
const srcOf = (tile: HTMLElement) => tile.querySelector("img")!.getAttribute("src")!;

test("at rest the words take the colour of the most colourful poster touching them", async () => {
  await wallUp();
  const slot = front().querySelector<HTMLElement>(".slot-text")!;
  const s = rect(slot);
  const touching = posters().filter((t) => {
    const r = rect(t);
    const m = 12;
    return r.x < s.x + s.w + m && r.x + r.w > s.x - m && r.y < s.y + s.h + m && r.y + r.h > s.y - m;
  });
  expect(touching.length).toBeGreaterThan(0);

  await waitFor(() => expect(slot.style.getPropertyValue("--th")).not.toBe(""));

  expect(hueOf(slot)).toBe(mostColourful(touching.map((t) => swatch(srcOf(t))))!.h);
  expect(hueOf(front().querySelector<HTMLElement>(".slot-arrow")!)).toBe(hueOf(slot));
});

test("with a mouse the words follow the poster under the pointer, then go back to rest", async () => {
  await wallUp();
  const slot = front().querySelector<HTMLElement>(".slot-text")!;
  await waitFor(() => expect(slot.style.getPropertyValue("--th")).not.toBe(""));
  const rest = hueOf(slot);
  const slotBox = rect(slot);
  const target = posters().find((t) => {
    const r = rect(t);
    const apart = r.x + r.w <= slotBox.x || slotBox.x + slotBox.w <= r.x || r.y + r.h <= slotBox.y || slotBox.y + slotBox.h <= r.y;
    return apart && swatch(srcOf(t)).h !== rest;
  })!;
  const r = rect(target);
  const wallEl = screen.getByLabelText("Posters");

  fireEvent.pointerMove(wallEl, { pointerType: "mouse", clientX: r.x + r.w / 2, clientY: r.y + r.h / 2 });
  await waitFor(() => expect(hueOf(slot)).toBe(swatch(srcOf(target)).h));

  fireEvent.pointerLeave(wallEl, { pointerType: "mouse" });
  await waitFor(() => expect(hueOf(slot)).toBe(rest));
});

test("a touch pointer and the pointer over the words themselves leave the colour as it was", async () => {
  await wallUp();
  const slot = front().querySelector<HTMLElement>(".slot-text")!;
  await waitFor(() => expect(slot.style.getPropertyValue("--th")).not.toBe(""));
  const rest = hueOf(slot);
  const wallEl = screen.getByLabelText("Posters");
  const other = posters().find((t) => swatch(srcOf(t)).h !== rest)!;
  const r = rect(other);

  fireEvent.pointerMove(wallEl, { pointerType: "touch", clientX: r.x + 5, clientY: r.y + 5 });
  fireEvent.pointerMove(words(), { pointerType: "mouse", clientX: r.x + 5, clientY: r.y + 5 });
  await new Promise((done) => setTimeout(done, 20));

  expect(hueOf(slot)).toBe(rest);
});

test("posters whose colour cannot be read leave the words in their default colour", async () => {
  tintOfFake.mockResolvedValue(null);
  await wallUp();
  await new Promise((done) => setTimeout(done, 20));

  expect(front().querySelector<HTMLElement>(".slot-text")!.style.getPropertyValue("--th")).toBe("");
});

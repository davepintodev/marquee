// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { Title, TitleSummary } from "./api/types";
import { profiles } from "./fixtures";
import { json, setupApp } from "./testkit";

const severance: TitleSummary = {
  id: "series-2",
  tmdbId: 2,
  kind: "series",
  title: "Severance",
  year: 2022,
  overview: "Work and life are split.",
  posterPath: "/api/images/poster/series-2",
  backdropPath: "/api/images/backdrop/series-2",
  inLibrary: false,
};
const dune: TitleSummary = { ...severance, id: "movie-1", tmdbId: 1, kind: "movie", title: "Dune", year: 2021 };

const added = (t: TitleSummary): Title => ({ ...t, inLibrary: true, monitored: true, badge: { state: "searching", label: "Searching" }, seasons: [], historySummary: "Nothing tried yet.", history: [] });

beforeEach(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.location.hash = "";
});

function serve(post: (init: Request) => Response | Promise<Response> = () => json(added(severance), 201), withProfiles = false) {
  return (url: URL, init: Request) => {
    if (withProfiles && url.pathname === "/api/profiles") return json(profiles);
    if (url.pathname === "/api/wall") return json({ items: [{ source: "trending", title: severance }, { source: "trending", title: dune }] });
    if (init.method === "POST" && url.pathname === "/api/titles") return post(init);
    return json({ error: "no such title in the library" }, 404);
  };
}

async function open(name: RegExp, post?: Parameters<typeof serve>[0], withProfiles = false) {
  const app = setupApp(serve(post, withProfiles), "#/");
  fireEvent.click((await screen.findAllByRole("button", { name }))[0]);
  await screen.findByRole("main");
  return app;
}

const page = () => within(screen.getByRole("main"));
const toggle = () => page().getByRole("button", { name: "Choose what to monitor" });
const menuNames = () => within(screen.getByRole("menu")).getAllByRole("menuitem").map((i) => i.textContent);

const options = [
  ["All episodes", "all", /all episodes/i],
  ["Future episodes", "future", /future episodes/i],
  ["First season", "first_season", /first season/i],
  ["Latest season", "latest_season", /latest season/i],
  ["None", "none", /nothing monitored/i],
] as const;

test("a series offers an add button with a menu of six ways to monitor, closed until opened", async () => {
  await open(/^Severance \(2022\)/);

  expect(screen.queryByRole("menu")).toBeNull();
  expect(toggle().getAttribute("aria-haspopup")).toBe("menu");
  expect(toggle().getAttribute("aria-expanded")).toBe("false");

  fireEvent.click(toggle());

  expect(toggle().getAttribute("aria-expanded")).toBe("true");
  expect(menuNames()).toEqual(["All episodes", "Future episodes", "First season", "Latest season", "None", "Select seasons and episodes…"]);
});

test("the main button of a series adds it with every episode monitored, and the page becomes the library title's", async () => {
  const { writes } = await open(/^Severance \(2022\)/);

  fireEvent.click(page().getByRole("button", { name: "Add & monitor" }));

  expect((await screen.findByRole("status")).textContent).toMatch(/added.*severance.*all episodes/i);
  expect(writes).toEqual([{ method: "POST", path: "/api/titles", body: { kind: "series", tmdbId: 2, monitor: "all" } }]);
  expect(await page().findByRole("button", { name: "Search automatically" })).toBeTruthy();
  expect(page().queryByRole("button", { name: "Choose what to monitor" })).toBeNull();
});

test.each(options)("choosing %s adds the series with monitor %s and the toast says what is monitored", async (label, monitor, words) => {
  const { writes } = await open(/^Severance \(2022\)/);
  fireEvent.click(toggle());

  fireEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: label }));

  const toast = await screen.findByRole("status");
  expect(toast.textContent).toMatch(/added.*severance/i);
  expect(toast.textContent).toMatch(words);
  expect(writes).toEqual([{ method: "POST", path: "/api/titles", body: { kind: "series", tmdbId: 2, monitor } }]);
  expect(screen.queryByRole("menu")).toBeNull();
  expect(await page().findByRole("button", { name: "Search automatically" })).toBeTruthy();
});

test("Select seasons and episodes adds the series with nothing monitored and stays on its page, ready to tick episodes", async () => {
  const { writes } = await open(/^Severance \(2022\)/);
  fireEvent.click(toggle());

  fireEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: "Select seasons and episodes…" }));

  expect((await screen.findByRole("status")).textContent).toMatch(/added.*severance.*nothing monitored/i);
  expect(writes).toEqual([{ method: "POST", path: "/api/titles", body: { kind: "series", tmdbId: 2, monitor: "none" } }]);
  expect(window.location.hash).toBe("#/title/series-2");
  expect(screen.getByRole("heading", { name: "Severance", level: 1 })).toBeTruthy();
});

test("a movie keeps its single add button, which sends no monitor choice", async () => {
  const { writes } = await open(/^Dune \(2021\)/, () => json(added(dune), 201));

  expect(page().queryByRole("button", { name: "Choose what to monitor" })).toBeNull();
  fireEvent.click(page().getByRole("button", { name: "Add & monitor" }));

  expect((await screen.findByRole("status")).textContent).toMatch(/added.*dune/i);
  expect(writes).toEqual([{ method: "POST", path: "/api/titles", body: { kind: "movie", tmdbId: 1 } }]);
});

test("opening the menu puts focus on the first option; arrows, Home and End move and wrap", async () => {
  await open(/^Severance \(2022\)/);

  fireEvent.click(toggle());
  const items = within(screen.getByRole("menu")).getAllByRole("menuitem");
  expect(document.activeElement).toBe(items[0]);

  fireEvent.keyDown(items[0], { key: "ArrowDown" });
  expect(document.activeElement).toBe(items[1]);
  fireEvent.keyDown(items[1], { key: "ArrowUp" });
  fireEvent.keyDown(items[0], { key: "ArrowUp" });
  expect(document.activeElement).toBe(items[5]);
  fireEvent.keyDown(items[5], { key: "ArrowDown" });
  expect(document.activeElement).toBe(items[0]);
  fireEvent.keyDown(items[0], { key: "End" });
  expect(document.activeElement).toBe(items[5]);
  fireEvent.keyDown(items[5], { key: "Home" });
  expect(document.activeElement).toBe(items[0]);
});

test("ArrowDown on the closed toggle opens the menu", async () => {
  await open(/^Severance \(2022\)/);

  fireEvent.keyDown(toggle(), { key: "ArrowDown" });

  expect(screen.getByRole("menu")).toBeTruthy();
  expect(document.activeElement).toBe(within(screen.getByRole("menu")).getAllByRole("menuitem")[0]);
});

test("Esc closes only the menu, returns focus to its button and leaves the page open", async () => {
  await open(/^Severance \(2022\)/);
  fireEvent.click(toggle());

  fireEvent.keyDown(document.activeElement!, { key: "Escape" });

  expect(screen.queryByRole("menu")).toBeNull();
  expect(window.location.hash).toBe("#/title/series-2");
  expect(document.activeElement).toBe(toggle());
  fireEvent.keyDown(window, { key: "Escape" });
  await waitFor(() => expect(screen.queryByRole("main")).toBeNull());
});

test("Esc with focus back on the toggle while the menu is open still closes only the menu", async () => {
  await open(/^Severance \(2022\)/);
  fireEvent.click(toggle());
  toggle().focus();

  fireEvent.keyDown(toggle(), { key: "Escape" });

  expect(screen.queryByRole("menu")).toBeNull();
  expect(window.location.hash).toBe("#/title/series-2");
});

test("pressing the toggle again or clicking elsewhere closes the menu", async () => {
  await open(/^Severance \(2022\)/);

  fireEvent.click(toggle());
  fireEvent.click(toggle());
  expect(screen.queryByRole("menu")).toBeNull();

  fireEvent.click(toggle());
  fireEvent.pointerDown(page().getByRole("heading", { name: "Severance", level: 1 }));
  expect(screen.queryByRole("menu")).toBeNull();
});

test("a failed add shows the message, closes the menu and can be tried again", async () => {
  let up = false;
  const { writes } = await open(/^Severance \(2022\)/, () => (up ? json(added(severance), 201) : json({ error: "TMDB did not answer" }, 502)));
  fireEvent.click(toggle());

  fireEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: "Select seasons and episodes…" }));

  expect((await page().findByRole("alert")).textContent).toContain("TMDB did not answer");
  expect(screen.queryByRole("status")).toBeNull();
  expect(screen.queryByRole("menu")).toBeNull();
  expect(toggle().getAttribute("aria-disabled")).not.toBe("true");

  up = true;
  fireEvent.click(toggle());
  fireEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: "First season" }));
  await page().findByRole("button", { name: "Search automatically" });
  expect(page().queryByRole("alert")).toBeNull();
  expect(writes.map((w) => (w.body as { monitor: string }).monitor)).toEqual(["none", "first_season"]);
});

test("while an add is in flight the menu cannot start a second one", async () => {
  let release!: () => void;
  const hold = new Promise<void>((r) => (release = r));
  const { writes } = await open(/^Severance \(2022\)/, async () => {
    await hold;
    return json(added(severance), 201);
  });
  fireEvent.click(toggle());
  const first = within(screen.getByRole("menu")).getByRole("menuitem", { name: "Future episodes" });

  fireEvent.click(first);
  fireEvent.click(first);
  fireEvent.click(page().getByRole("button", { name: "Add & monitor" }));
  fireEvent.click(toggle());
  expect(screen.queryByRole("menu")).toBeNull();
  release();

  await page().findByRole("button", { name: "Search automatically" });
  expect(writes).toHaveLength(1);
});

test.each([
  ["Future episodes", "future"],
  ["Select seasons and episodes…", "none"],
])("Try again after a failed %s add retries with the same monitor choice", async (label, monitor) => {
  let up = false;
  const { writes } = await open(/^Severance \(2022\)/, () => (up ? json(added(severance), 201) : json({ error: "TMDB did not answer" }, 502)));
  fireEvent.click(toggle());
  fireEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: label }));
  await page().findByRole("alert");

  up = true;
  fireEvent.click(page().getByRole("button", { name: "Try again" }));

  await page().findByRole("button", { name: "Search automatically" });
  expect(writes.map((w) => (w.body as { monitor: string }).monitor)).toEqual([monitor, monitor]);
});

const picker = () => page().findByRole("combobox", { name: "Quality profile" });

test("a title not in the library offers a profile picker that starts on the default of its kind", async () => {
  await open(/^Severance \(2022\)/, undefined, true);
  const series = (await picker()) as HTMLSelectElement;
  expect(series.value).toBe("everything");
  expect([...series.options].map((o) => o.value)).toEqual(["everything", "tv-safe"]);

  cleanup();
  window.location.hash = "";
  await open(/^Dune \(2021\)/, () => json(added(dune), 201), true);
  expect(((await picker()) as HTMLSelectElement).value).toBe("tv-safe");
});

test("adding with the picker left alone sends no profile, so the title keeps following the default", async () => {
  const { writes } = await open(/^Severance \(2022\)/, undefined, true);
  await picker();

  fireEvent.click(page().getByRole("button", { name: "Add & monitor" }));

  await screen.findByRole("status");
  expect(writes).toEqual([{ method: "POST", path: "/api/titles", body: { kind: "series", tmdbId: 2, monitor: "all" } }]);
});

test("a profile chosen before adding is sent with the add, for the main button and the monitor menu", async () => {
  const { writes } = await open(/^Dune \(2021\)/, () => json(added(dune), 201), true);
  fireEvent.change(await picker(), { target: { value: "everything" } });

  fireEvent.click(page().getByRole("button", { name: "Add & monitor" }));

  await screen.findByRole("status");
  expect(writes).toEqual([{ method: "POST", path: "/api/titles", body: { kind: "movie", tmdbId: 1, profile: "everything" } }]);

  cleanup();
  window.location.hash = "";
  const series = await open(/^Severance \(2022\)/, undefined, true);
  fireEvent.change(await picker(), { target: { value: "tv-safe" } });
  fireEvent.click(toggle());
  fireEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: "First season" }));

  await screen.findByRole("status");
  expect(series.writes).toEqual([{ method: "POST", path: "/api/titles", body: { kind: "series", tmdbId: 2, monitor: "first_season", profile: "tv-safe" } }]);
});

test("when the profiles cannot be loaded the add still works and no picker shows", async () => {
  const { writes } = await open(/^Severance \(2022\)/);

  expect(page().queryByRole("combobox")).toBeNull();
  fireEvent.click(page().getByRole("button", { name: "Add & monitor" }));

  await screen.findByRole("status");
  expect(writes).toHaveLength(1);
});

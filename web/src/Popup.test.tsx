// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { Title, TitleSummary } from "./api/types";
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

const added = (t: TitleSummary): Title => ({ ...t, inLibrary: true, monitored: true, badge: { state: "searching", label: "Searching" }, seasons: [], historySummary: "Nothing tried yet." });

beforeEach(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.location.hash = "";
});

function serve(post: (init: Request) => Response | Promise<Response> = () => json(added(severance), 201)) {
  return (url: URL, init: Request) => {
    if (url.pathname === "/api/wall") return json({ items: [{ source: "trending", title: severance }, { source: "trending", title: dune }] });
    if (init.method === "POST" && url.pathname === "/api/titles") return post(init);
    if (url.pathname === "/api/titles/series-2" && init.method === "GET") return json(added(severance));
    return json({ error: "nope" }, 404);
  };
}

async function open(name: RegExp, post?: Parameters<typeof serve>[0]) {
  const app = setupApp(serve(post), "#/");
  fireEvent.click((await screen.findAllByRole("button", { name }))[0]);
  return app;
}

const dialog = () => within(screen.getByRole("dialog"));
const toggle = () => dialog().getByRole("button", { name: "Choose what to monitor" });
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

test("the main button of a series adds it with every episode monitored", async () => {
  const { writes } = await open(/^Severance \(2022\)/);

  fireEvent.click(dialog().getByRole("button", { name: "Add & monitor" }));

  expect((await screen.findByRole("status")).textContent).toMatch(/added.*severance.*all episodes/i);
  expect(writes).toEqual([{ method: "POST", path: "/api/titles", body: { kind: "series", tmdbId: 2, monitor: "all" } }]);
  expect(dialog().getByRole("button", { name: "Search now" })).toBeTruthy();
  expect(dialog().queryByRole("button", { name: "Choose what to monitor" })).toBeNull();
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
  expect(dialog().getByRole("button", { name: "Search now" })).toBeTruthy();
});

test("Select seasons and episodes adds the series with nothing monitored and opens its detail page", async () => {
  const { writes } = await open(/^Severance \(2022\)/);
  fireEvent.click(toggle());

  fireEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: "Select seasons and episodes…" }));

  await waitFor(() => expect(window.location.hash).toBe("#/title/series-2"));
  expect(writes).toEqual([{ method: "POST", path: "/api/titles", body: { kind: "series", tmdbId: 2, monitor: "none" } }]);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(await screen.findByRole("heading", { name: "Severance", level: 1 })).toBeTruthy();
  expect((await screen.findByRole("status")).textContent).toMatch(/added.*severance/i);
});

test("a movie keeps its single add button, which sends no monitor choice", async () => {
  const { writes } = await open(/^Dune \(2021\)/, () => json(added(dune), 201));

  expect(dialog().queryByRole("button", { name: "Choose what to monitor" })).toBeNull();
  fireEvent.click(dialog().getByRole("button", { name: "Add & monitor" }));

  expect((await screen.findByRole("status")).textContent).toMatch(/added.*dune/i);
  expect(writes).toEqual([{ method: "POST", path: "/api/titles", body: { kind: "movie", tmdbId: 1 } }]);
});

test("a series already in the library has Search now and no add menu", async () => {
  setupApp(
    (url) => (url.pathname === "/api/wall" ? json({ items: [{ source: "library", title: { ...severance, inLibrary: true, monitored: true } }] }) : json({ error: "nope" }, 404)),
    "#/",
  );
  fireEvent.click((await screen.findAllByRole("button", { name: /^Severance \(2022\)/ }))[0]);

  expect(dialog().getByRole("button", { name: "Search now" })).toBeTruthy();
  expect(dialog().queryByRole("button", { name: "Choose what to monitor" })).toBeNull();
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

test("Esc closes only the menu, returns focus to its button and leaves the popup open", async () => {
  await open(/^Severance \(2022\)/);
  fireEvent.click(toggle());

  fireEvent.keyDown(document.activeElement!, { key: "Escape" });

  expect(screen.queryByRole("menu")).toBeNull();
  expect(screen.getByRole("dialog")).toBeTruthy();
  expect(document.activeElement).toBe(toggle());
  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
});

test("pressing the toggle again, moving focus away or clicking elsewhere closes the menu", async () => {
  await open(/^Severance \(2022\)/);

  fireEvent.click(toggle());
  fireEvent.click(toggle());
  expect(screen.queryByRole("menu")).toBeNull();

  fireEvent.click(toggle());
  fireEvent.blur(document.activeElement!, { relatedTarget: dialog().getByRole("button", { name: "Interactive search" }) });
  expect(screen.queryByRole("menu")).toBeNull();

  fireEvent.click(toggle());
  fireEvent.blur(document.activeElement!, { relatedTarget: null });
  expect(screen.getByRole("menu")).toBeTruthy();
  fireEvent.click(toggle());

  fireEvent.click(toggle());
  fireEvent.pointerDown(dialog().getByRole("heading", { name: "Severance" }));
  expect(screen.queryByRole("menu")).toBeNull();
});

test("a failed add shows the message, closes the menu and can be tried again; Select stays on the popup", async () => {
  let up = false;
  const { writes } = await open(/^Severance \(2022\)/, () => (up ? json(added(severance), 201) : json({ error: "TMDB did not answer" }, 502)));
  fireEvent.click(toggle());

  fireEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: "Select seasons and episodes…" }));

  expect((await dialog().findByRole("alert")).textContent).toContain("TMDB did not answer");
  expect(screen.queryByRole("status")).toBeNull();
  expect(screen.queryByRole("menu")).toBeNull();
  expect(window.location.hash).toBe("#/");
  expect(toggle().getAttribute("aria-disabled")).not.toBe("true");

  up = true;
  fireEvent.click(toggle());
  fireEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: "First season" }));
  await dialog().findByRole("button", { name: "Search now" });
  expect(dialog().queryByRole("alert")).toBeNull();
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
  fireEvent.click(dialog().getByRole("button", { name: "Add & monitor" }));
  fireEvent.click(toggle());
  expect(screen.queryByRole("menu")).toBeNull();
  release();

  await dialog().findByRole("button", { name: "Search now" });
  expect(writes).toHaveLength(1);
});

test("after adding from the menu focus rests on the popup's main button", async () => {
  await open(/^Severance \(2022\)/);
  fireEvent.click(toggle());

  fireEvent.click(within(screen.getByRole("menu")).getByRole("menuitem", { name: "Latest season" }));

  const search = await dialog().findByRole("button", { name: "Search now" });
  expect(document.activeElement).toBe(search);
});

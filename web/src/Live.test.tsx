// @vitest-environment jsdom
import { act, cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { TitleSummary } from "./api/types";
import { FakeEventSource, json, setupApp } from "./testkit";

function movie(id: number, name: string, over: Partial<TitleSummary> = {}): TitleSummary {
  return { id: `movie-${id}`, tmdbId: id, kind: "movie", title: name, year: 2021, overview: "", posterPath: "", backdropPath: "", inLibrary: true, monitored: true, ...over };
}

const dune = movie(1, "Dune", { badge: { state: "searching", label: "Searching" } });
const arrival = movie(2, "Arrival", { badge: { state: "downloaded", label: "Downloaded" } });

beforeEach(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.location.hash = "";
});

let walls = 0;
const serve = (url: URL) => {
  if (url.pathname === "/api/wall") {
    walls++;
    return json({ items: [dune, arrival].map((title) => ({ source: "library", title })) });
  }
  return json({ error: "nope" }, 404);
};

// The wall repeats a small library to fill the screen.
const tile = async (name: RegExp) => (await screen.findAllByRole("button", { name }))[0];
const live = () => FakeEventSource.open()[0];

test("a wall tile follows the live badge of its title without a reload", async () => {
  walls = 0;
  setupApp(serve, "#/");
  await tile(/^Dune \(2021\), Searching$/);
  expect(live().url).toBe("http://marquee.test/api/events");

  act(() => live().emit("progress", { downloadId: "d1", titleId: "movie-1", state: "downloading", percent: 43, bytesDone: 43, bytesTotal: 100 }));
  await tile(/^Dune \(2021\), Downloading 43%$/);

  act(() => live().emit("progress", { downloadId: "d1", titleId: "movie-1", state: "caching", percent: 42, bytesDone: 0, bytesTotal: 0 }));
  await tile(/^Dune \(2021\), Caching 42%$/);

  act(() => live().emit("title", { titleId: "movie-1", badge: { state: "downloaded", label: "Downloaded" } }));
  await tile(/^Dune \(2021\), Downloaded$/);
  expect(await tile(/^Arrival \(2021\), Downloaded$/)).toBeTruthy();
  expect(walls).toBe(1);
});

test("a title that left the library loses its badge on the wall", async () => {
  setupApp(serve, "#/");
  await tile(/^Arrival \(2021\), Downloaded$/);
  act(() => live().emit("title", { titleId: "movie-2", badge: null }));
  await tile(/^Arrival \(2021\)$/);
});

test("after the stream reconnects the wall is fetched again, since events were lost", async () => {
  walls = 0;
  setupApp(serve, "#/");
  await tile(/^Dune/);
  act(() => live().open());
  act(() => live().open());
  await waitFor(() => expect(walls).toBe(2));
  act(() => live().emit("title", { titleId: "movie-1", badge: { state: "stalled", label: "Stalled" } }));
  await tile(/^Dune \(2021\), Stalled$/);
});

test("a title the wall does not show yet brings the wall back for it, once", async () => {
  walls = 0;
  let added = false;
  setupApp((url) => {
    if (url.pathname !== "/api/wall") return json({ error: "nope" }, 404);
    walls++;
    const items = [dune, arrival, ...(added ? [movie(3, "Heat")] : [])];
    return json({ items: items.map((title) => ({ source: "library", title })) });
  }, "#/");
  await tile(/^Dune/);

  added = true;
  act(() => live().emit("title", { titleId: "movie-3", badge: { state: "searching", label: "Searching" } }));
  act(() => live().emit("progress", { downloadId: "d9", titleId: "movie-3", state: "downloading", percent: 1, bytesDone: 1, bytesTotal: 100 }));
  await tile(/^Heat \(2021\)/);
  expect(walls).toBe(2);
});

test("a title removed from the wall and added again brings the wall back for it again", async () => {
  walls = 0;
  let onWall = false;
  setupApp((url) => {
    if (url.pathname !== "/api/wall") return json({ error: "nope" }, 404);
    walls++;
    const items = [dune, arrival, ...(onWall ? [movie(3, "Heat")] : [])];
    return json({ items: items.map((title) => ({ source: "library", title })) });
  }, "#/");
  await tile(/^Dune/);
  const added = { titleId: "movie-3", badge: { state: "searching", label: "Searching" } };

  onWall = true;
  act(() => live().emit("title", added));
  await tile(/^Heat \(2021\)/);
  expect(walls).toBe(2);

  onWall = false;
  act(() => live().open());
  act(() => live().open());
  await waitFor(() => expect(walls).toBe(3));
  await waitFor(() => expect(screen.queryAllByRole("button", { name: /^Heat/ })).toHaveLength(0));

  onWall = true;
  act(() => live().emit("title", added));
  await waitFor(() => expect(walls).toBe(4));
  await tile(/^Heat \(2021\)/);
});

test("a title another client removed never turns up in Trending, which holds only what the wall called trending", async () => {
  setupApp((url) => {
    if (url.pathname !== "/api/wall") return json({ error: "nope" }, 404);
    return json({ items: [{ source: "library", title: dune }, { source: "library", title: arrival }, { source: "trending", title: movie(3, "Heat", { inLibrary: false, monitored: undefined }) }] });
  }, "#/grid");
  const grid = within(document.querySelector<HTMLElement>(".grid-screen")!);
  await grid.findByRole("list", { name: "Library titles" });

  act(() => live().emit("title", { titleId: "movie-2", badge: null }));
  await waitFor(() => expect(grid.queryByRole("button", { name: /^Arrival \(2021\), Downloaded$/ })).toBeNull());
  fireEvent.click(grid.getByRole("button", { name: /^trending/i }));

  expect(grid.getAllByRole("button", { name: /\(20\d\d\)/ }).map((b) => b.getAttribute("aria-label"))).toEqual(["Heat (2021)"]);
});

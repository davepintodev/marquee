// @vitest-environment jsdom
import { afterEach, beforeEach, expect, test } from "vitest";
import { BACK_FROM_TITLE, hrefOf, leaveTitle, pushTo } from "./route";

const settle = () => new Promise((r) => setTimeout(r, 20));

beforeEach(() => {
  window.history.replaceState(null, "", "#/");
});
afterEach(() => {
  window.history.replaceState(null, "", "#/");
});

test("Back from a title is the library grid, as chosen", () => {
  expect(BACK_FROM_TITLE).toBe("library");
});

test("a title opened from the wall leaves for the grid, and the wall is still one step back", async () => {
  pushTo({ name: "title", id: "movie-1" }, "page");
  expect(window.location.hash).toBe(hrefOf({ name: "title", id: "movie-1" }));

  leaveTitle();

  expect(window.location.hash).toBe("#/grid");
  window.history.back();
  await settle();
  expect(window.location.hash).toBe("#/");
});

test("a title opened from the grid leaves by the browser's own back, to the grid", async () => {
  pushTo({ name: "grid" }, "grid");
  pushTo({ name: "title", id: "movie-1" }, "page");

  leaveTitle();
  await settle();

  expect(window.location.hash).toBe("#/grid");
});

test("a title reached by a plain link, with no entry of ours behind it, leaves for the grid", () => {
  window.history.replaceState(null, "", "#/title/movie-1");

  leaveTitle();

  expect(window.location.hash).toBe("#/grid");
});

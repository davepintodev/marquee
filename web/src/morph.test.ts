// @vitest-environment jsdom
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { morphRoute } from "./morph";

type Doc = { startViewTransition?: unknown };
const doc = document as unknown as Doc;

let finish: () => void;
const start = vi.fn((update: () => void) => {
  update();
  return { finished: new Promise<void>((done) => (finish = done)) };
});

function motion(reduce: boolean) {
  window.matchMedia = ((query: string) => ({ matches: reduce && query.includes("reduce"), media: query, addEventListener() {}, removeEventListener() {} })) as unknown as typeof window.matchMedia;
}

const grid = { name: "grid" } as const;
const title = { name: "title", id: "movie-1" } as const;

beforeEach(() => {
  start.mockClear();
  doc.startViewTransition = start;
  motion(false);
});
afterEach(() => {
  delete doc.startViewTransition;
  delete document.documentElement.dataset.morph;
  document.body.innerHTML = "";
});

test("opening a title runs the view transition around the switch and marks the page while it plays", async () => {
  const commit = vi.fn();

  morphRoute(grid, title, commit);

  expect(start).toHaveBeenCalledTimes(1);
  expect(commit).toHaveBeenCalledTimes(1);
  expect(document.documentElement.hasAttribute("data-morph")).toBe(true);
  finish();
  await vi.waitFor(() => expect(document.documentElement.hasAttribute("data-morph")).toBe(false));
});

test("the clicked poster is named for the transition while the old screen is captured, and the name is gone after", () => {
  document.body.innerHTML = `<button data-tid="movie-1"><span class="cell-art"></span></button>`;
  const art = document.querySelector<HTMLElement>(".cell-art")!;
  document.querySelector("button")!.click();
  let during = "";
  start.mockImplementationOnce((update: () => void) => {
    during = art.style.viewTransitionName || art.style.getPropertyValue("view-transition-name");
    update();
    return { finished: Promise.resolve() };
  });

  morphRoute(grid, title, () => {});

  expect(during).toBe("poster");
  expect(art.style.getPropertyValue("view-transition-name")).toBe("");
});

test("a reader who asked for less motion just gets the switch: no transition, no marker", () => {
  motion(true);
  const commit = vi.fn();

  morphRoute(grid, title, commit);
  morphRoute(title, grid, commit);

  expect(start).not.toHaveBeenCalled();
  expect(commit).toHaveBeenCalledTimes(2);
  expect(document.documentElement.hasAttribute("data-morph")).toBe(false);
});

test("a browser without view transitions just switches", () => {
  delete doc.startViewTransition;
  const commit = vi.fn();

  morphRoute(grid, title, commit);

  expect(commit).toHaveBeenCalledTimes(1);
  expect(document.documentElement.hasAttribute("data-morph")).toBe(false);
});

test("only entering and leaving a title morph: other switches, and one title to another, do not", () => {
  const commit = vi.fn();

  morphRoute(grid, { name: "activity" }, commit);
  morphRoute({ name: "wall" }, grid, commit);
  morphRoute(title, { name: "title", id: "movie-2" }, commit);

  expect(start).not.toHaveBeenCalled();
  expect(commit).toHaveBeenCalledTimes(3);
});

test("leaving a title carries the poster back to its tile", () => {
  document.body.innerHTML = `<button data-tid="movie-1"><span class="cell-art"></span></button>`;
  const art = document.querySelector<HTMLElement>(".cell-art")!;
  art.scrollIntoView = vi.fn();

  morphRoute(title, grid, () => {});

  expect(start).toHaveBeenCalledTimes(1);
  expect(art.scrollIntoView).toHaveBeenCalled();
  expect(art.style.getPropertyValue("view-transition-name")).toBe("poster");
  finish();
  return vi.waitFor(() => expect(art.style.getPropertyValue("view-transition-name")).toBe(""));
});

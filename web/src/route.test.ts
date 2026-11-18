import { expect, test } from "vitest";
import { hrefOf, parseRoute } from "./route";

test("hashes name the wall, Activity or a title; anything else is the wall", () => {
  expect(parseRoute("")).toEqual({ name: "wall" });
  expect(parseRoute("#/")).toEqual({ name: "wall" });
  expect(parseRoute("#/activity")).toEqual({ name: "activity" });
  expect(parseRoute("#/title/series-1408")).toEqual({ name: "title", id: "series-1408" });
  expect(parseRoute("#/title/movie-5")).toEqual({ name: "title", id: "movie-5" });
  expect(parseRoute("#/title/book-5")).toEqual({ name: "wall" });
  expect(parseRoute("#/title/movie-5/extra")).toEqual({ name: "wall" });
});

test("a route's href parses back to the same route", () => {
  for (const r of [{ name: "wall" }, { name: "activity" }, { name: "title", id: "movie-9" }] as const) {
    expect(parseRoute(hrefOf(r))).toEqual(r);
  }
});

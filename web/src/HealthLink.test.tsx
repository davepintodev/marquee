// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { Health } from "./api/types";
import { json, setupApp } from "./testkit";

const check = (status: "ok" | "warning" | "error"): Health["checks"][number] => ({ id: "tmdb", name: "Check", status, message: "m" });
const health = (...statuses: ("ok" | "warning" | "error")[]): Health => ({
  status: statuses.includes("error") ? "error" : statuses.includes("warning") ? "warning" : "ok",
  dryRun: false,
  checks: statuses.map(check),
  libraryProblems: [],
});

function serve(answer: () => Response | Promise<Response>) {
  return (url: URL) => {
    if (url.pathname === "/api/wall") return json({ items: [] });
    if (url.pathname === "/api/health") return answer();
    return json({ error: "nope" }, 404);
  };
}

beforeEach(() => vi.spyOn(console, "error").mockImplementation(() => {}));
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.location.hash = "";
});

test("the grid header has a plain-text Health link, styled like Activity, that opens the Health page", async () => {
  setupApp(serve(() => json(health("ok", "ok"))), "#/grid");

  const link = await screen.findByRole("link", { name: "Health" });
  expect(link.getAttribute("href")).toBe("#/health");
  expect(link.textContent).toBe("Health");
  expect(link.className).toBe(screen.getByRole("link", { name: "Activity" }).className);
  expect(link.querySelector("svg")).toBeNull();
  expect(screen.getByRole("link", { name: "Activity" })).toBeTruthy();

  fireEvent.click(link);
  await screen.findByRole("heading", { name: "Health" });
  await screen.findByRole("list", { name: /checks/i });
});

test("the link has no dot while every check passes, and a warning alone is not a failure", async () => {
  setupApp(serve(() => json(health("ok", "warning"))), "#/grid");

  const link = await screen.findByRole("link", { name: "Health" });
  await new Promise((r) => setTimeout(r, 20));
  expect(link.querySelector("i")).toBeNull();
  expect(link.getAttribute("aria-label")).toBe("Health");
});

test("the link shows a problem dot before the word and says something needs attention when a check fails", async () => {
  setupApp(serve(() => json(health("ok", "error"))), "#/grid");

  const link = await screen.findByRole("link", { name: /health.*needs attention/i });
  const dot = link.querySelector("i");
  expect(dot).toBeTruthy();
  expect(dot?.getAttribute("aria-hidden")).toBe("true");
  expect(link.firstElementChild).toBe(dot);
  expect(link.textContent).toBe("Health");
});

test("the link has no dot while Health is loading and when the request fails", async () => {
  let release!: (r: Response) => void;
  const pending = new Promise<Response>((r) => (release = r));
  const { calls } = setupApp(serve(() => pending), "#/grid");
  const link = await screen.findByRole("link", { name: "Health" });
  await waitFor(() => expect(calls.some((u) => u.pathname === "/api/health")).toBe(true));
  expect(link.querySelector("i")).toBeNull();
  release(json({ error: "down" }, 500));
  await new Promise((r) => setTimeout(r, 20));
  expect(screen.getByRole("link", { name: "Health" }).querySelector("i")).toBeNull();
  expect(screen.getByRole("link", { name: "Health" }).getAttribute("aria-label")).toBe("Health");
  expect(screen.getByRole("searchbox")).toBeTruthy();
});

test("coming back from the Health page checks again", async () => {
  let failing = true;
  setupApp(serve(() => json(failing ? health("error") : health("ok"))), "#/grid");
  await screen.findByRole("link", { name: /needs attention/i });

  fireEvent.click(screen.getByRole("link", { name: /needs attention/i }));
  await screen.findByRole("heading", { name: "Health" });
  failing = false;
  fireEvent.click(screen.getByRole("button", { name: "Close" }));

  await waitFor(() => expect(screen.getByRole("link", { name: "Health" }).querySelector("i")).toBeNull());
});

// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { Health } from "./Health";
import { createApi } from "./api/client";
import type { Health as HealthData } from "./api/types";

const BASE = "http://marquee.test";

const sick: HealthData = {
  status: "error",
  dryRun: true,
  checks: [
    { id: "dry_run", name: "Dry-run", status: "warning", message: "Dry-run is on: marquee searches and chooses but never downloads." },
    { id: "aiostreams", name: "AIOStreams", status: "error", message: "AIOStreams is not answering; check that it is running and that the manifest URL is right." },
    { id: "tmdb", name: "TMDB", status: "ok", message: "Key accepted." },
    { id: "temp_path", name: "Temp path", status: "error", message: "The temp folder is on a different filesystem than the movies folder." },
  ],
  libraryProblems: [
    { path: "Movies/Heat", reason: "ambiguous", message: "Matched more than one TMDB title (1995 and 1986); add the title to pick one." },
    { path: "Series/Unknown Show", reason: "unmatched", message: "No TMDB title matches this folder name." },
    { path: "Series/House/Season 4/notes.txt", reason: "unparsed_file", message: "The file name has no S01E01 token; ignored." },
  ],
};

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function setup(answers: (() => Response)[]) {
  let n = 0;
  const fetchStub = vi.fn(async () => answers[Math.min(n++, answers.length - 1)]());
  const api = createApi({ baseUrl: BASE, fetch: fetchStub as unknown as typeof fetch });
  render(<Health api={api} onClose={() => {}} />);
  return fetchStub;
}

beforeEach(() => vi.spyOn(console, "error").mockImplementation(() => {}));
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("Health names each problem in the API's words and marks what is fine", async () => {
  setup([() => json(sick)]);

  const list = await screen.findByRole("list", { name: /checks/i });
  const rows = within(list).getAllByRole("listitem");
  expect(rows).toHaveLength(4);
  expect(within(rows[1]).getByText("AIOStreams")).toBeTruthy();
  expect(within(rows[1]).getByText(/not answering/i)).toBeTruthy();
  expect(within(rows[1]).getByText(/problem/i)).toBeTruthy();
  expect(within(rows[0]).getByText(/warning/i)).toBeTruthy();
  expect(within(rows[2]).getByText(/^ok$/i)).toBeTruthy();
  expect(screen.getByText(/needs attention/i)).toBeTruthy();
});

test("Health lists the folders marquee could not match and the files it ignored, with the reason", async () => {
  setup([() => json(sick)]);

  const list = await screen.findByRole("list", { name: /library/i });
  const rows = within(list).getAllByRole("listitem");
  expect(rows).toHaveLength(3);
  expect(within(rows[0]).getByText("Movies/Heat")).toBeTruthy();
  expect(within(rows[0]).getByText(/more than one/i)).toBeTruthy();
  expect(within(rows[0]).getByText(/several matches/i)).toBeTruthy();
  expect(within(rows[1]).getByText(/no match/i)).toBeTruthy();
  expect(within(rows[2]).getByText(/ignored file/i)).toBeTruthy();
});

test("Health says so when nothing needs attention and has no library list", async () => {
  setup([() => json({ status: "ok", dryRun: false, checks: [sick.checks[2]], libraryProblems: [] })]);

  await screen.findByText(/everything is working/i);
  expect(screen.queryByRole("list", { name: /library/i })).toBeNull();
});

test("a failed read says why and Check again reads once more", async () => {
  const fetchStub = setup([() => json({ error: "something went wrong inside marquee" }, 500), () => json(sick)]);

  expect(await screen.findByRole("alert")).toHaveProperty("textContent", expect.stringContaining("something went wrong"));
  fireEvent.click(screen.getByRole("button", { name: /try again/i }));
  await screen.findByRole("list", { name: /checks/i });
  expect(fetchStub).toHaveBeenCalledTimes(2);
});

test("Check again probes the dependencies once more", async () => {
  const fetchStub = setup([() => json(sick), () => json({ ...sick, status: "ok", checks: [sick.checks[2]], libraryProblems: [] })]);

  await screen.findByRole("list", { name: /checks/i });
  fireEvent.click(screen.getByRole("button", { name: /check again/i }));
  await screen.findByText(/everything is working/i);
  expect(fetchStub).toHaveBeenCalledTimes(2);
});

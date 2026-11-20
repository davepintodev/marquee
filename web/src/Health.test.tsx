// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
  libraryTotals: { movies: 7, episodes: 15 },
  libraryProblems: [
    { path: "Movies/Heat", reason: "ambiguous", message: "Matched more than one TMDB title (1995 and 1986); add the title to pick one." },
    { path: "Series/Unknown Show", reason: "unmatched", message: "No TMDB title matches this folder name." },
    { path: "Series/House/Season 4/notes.txt", reason: "unparsed_file", message: "The file name has no S01E01 token; ignored." },
  ],
};

const fine = (over: Partial<HealthData> = {}): HealthData => ({
  status: "ok",
  dryRun: false,
  checks: [
    { id: "aiostreams", name: "AIOStreams", status: "ok", message: "Reachable." },
    { id: "tmdb", name: "TMDB", status: "ok", message: "Key accepted." },
  ],
  libraryTotals: { movies: 1, episodes: 1 },
  libraryProblems: [],
  ...over,
});

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function setup(answers: (() => Response)[], props: { onClose?: () => void; onForget?: () => void } = {}) {
  let n = 0;
  const fetchStub = vi.fn(async () => answers[Math.min(n++, answers.length - 1)]());
  const api = createApi({ baseUrl: BASE, fetch: fetchStub as unknown as typeof fetch });
  render(<Health api={api} onClose={props.onClose ?? (() => {})} onForget={props.onForget} />);
  return fetchStub;
}

beforeEach(() => vi.spyOn(console, "error").mockImplementation(() => {}));
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const line = async () => within(await screen.findByRole("list", { name: /in the order a title travels/i })).getAllByRole("listitem");

test("Health says it is checking, then answers the one question first, with the counts", async () => {
  setup([() => json(sick)]);

  expect(screen.getByText(/checking/i)).toBeTruthy();
  const head = await screen.findByRole("heading", { level: 2, name: /can.t download right now/i });
  expect(head.parentElement?.textContent).toContain("AIOStreams is not answering");
  expect(screen.getByText("2 problems · 1 warning · 1 passing")).toBeTruthy();
  expect(screen.queryByText(/checking/i)).toBeNull();
});

test("the headline follows the checks: a dry-run alone is not downloading yet, a clean bill is ready", async () => {
  setup([() => json(fine({ status: "warning", dryRun: true, checks: [sick.checks[0], sick.checks[2]] }))]);
  const head = await screen.findByRole("heading", { level: 2, name: /not downloading yet/i });
  expect(head.parentElement?.textContent).toContain("never downloads");
  expect(screen.getByText("1 warning · 1 passing")).toBeTruthy();
  cleanup();

  setup([() => json(fine())]);
  const ready = await screen.findByRole("heading", { level: 2, name: /ready to download/i });
  expect(ready.parentElement?.textContent).toContain("Every check passed.");
  expect(screen.getByText("2 passing")).toBeTruthy();
});

test("the checks are a path in the order a title travels, folded to a name and one state word each", async () => {
  setup([() => json(sick)]);

  const stops = await line();
  expect(stops.map((s) => s.querySelector(".bd-name")?.firstChild?.textContent)).toEqual(["TMDB", "AIOStreams", "Dry-run", "Temp path"]);
  expect(stops.map((s) => within(s).getByText(/^(OK|Warning|Problem)$/).textContent)).toEqual(["OK", "Problem", "Warning", "Problem"]);
  expect(stops.map((s) => s.getAttribute("data-status"))).toEqual(["ok", "error", "warning", "error"]);
  // Folded: no message is on screen, and the control that unfolds them says so.
  expect(screen.queryByText(/different filesystem/i)).toBeNull();
  expect(screen.getByRole("button", { name: /show details/i }).getAttribute("aria-expanded")).toBe("false");
});

test("a dry-run that is on is a gate across the path; a failed check breaks the link that leads to it", async () => {
  setup([() => json(sick)]);

  const stops = await line();
  expect(stops.map((s) => s.getAttribute("data-gate"))).toEqual(["false", "false", "true", "false"]);
  // TMDB leads into the failed AIOStreams; AIOStreams leads into the gate; the gate leads into the failed temp path.
  expect(stops.map((s) => s.getAttribute("data-out"))).toEqual(["broken", "flow", "broken", "end"]);
});

test("past the gate the links are paused, not flowing", async () => {
  const checks = [
    { id: "dry_run" as const, name: "Dry-run", status: "warning" as const, message: "on" },
    { id: "tmdb" as const, name: "TMDB", status: "ok" as const, message: "ok" },
    { id: "temp_path" as const, name: "Temp path", status: "ok" as const, message: "ok" },
  ];
  setup([() => json(fine({ checks }))]);

  expect((await line()).map((s) => s.getAttribute("data-out"))).toEqual(["flow", "paused", "end"]);
});

test("a check this build does not know goes after the known ones", async () => {
  setup([() => json(fine({ checks: [{ id: "later" as "tmdb", name: "Later", status: "ok", message: "m" }, sick.checks[2]] }))]);
  expect((await line()).map((s) => s.querySelector(".bd-name")?.firstChild?.textContent)).toEqual(["TMDB", "Later"]);
});

test("Show details unfolds the full message of every check and Hide details folds it back", async () => {
  setup([() => json(sick)]);
  await line();

  const toggle = screen.getByRole("button", { name: /show details/i });
  fireEvent.click(toggle);
  expect(toggle.getAttribute("aria-expanded")).toBe("true");
  expect(toggle.textContent).toMatch(/hide details/i);
  const rows = within(screen.getByRole("list", { name: /check details/i })).getAllByRole("listitem");
  expect(rows).toHaveLength(4);
  expect(within(rows[1]).getByText("AIOStreams")).toBeTruthy();
  expect(within(rows[1]).getByText(/not answering/i)).toBeTruthy();
  // The mark already shows the state, so the detail rows do not repeat it as a word.
  expect(within(rows[1]).queryByText(/^problem$/i)).toBeNull();
  expect(within(rows[0]).getByText("Key accepted.")).toBeTruthy();

  fireEvent.click(toggle);
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  expect(screen.queryByRole("list", { name: /check details/i })).toBeNull();
});

test("the details toggle is a button, so Enter and Space reach it by keyboard", async () => {
  setup([() => json(sick)]);
  await line();
  const toggle = screen.getByRole("button", { name: /show details/i });
  expect(toggle.tagName).toBe("BUTTON");
  toggle.focus();
  expect(document.activeElement).toBe(toggle);
});

test("the library is folded with its totals and problem count, and Show problems lists each with its reason", async () => {
  setup([() => json(sick)]);
  await line();

  const lib = screen.getByRole("region", { name: /library/i });
  expect(within(lib).getByText("7 movies · 15 episodes")).toBeTruthy();
  expect(within(lib).getByText("3 problems")).toBeTruthy();
  expect(screen.queryByRole("list", { name: /library problems/i })).toBeNull();
  const toggle = within(lib).getByRole("button", { name: /show problems/i });
  expect(toggle.getAttribute("aria-expanded")).toBe("false");

  fireEvent.click(toggle);
  expect(toggle.getAttribute("aria-expanded")).toBe("true");
  expect(toggle.textContent).toMatch(/hide problems/i);
  const rows = within(screen.getByRole("list", { name: /library problems/i })).getAllByRole("listitem");
  expect(rows).toHaveLength(3);
  expect(rows[0].textContent).toContain("Movies/Heat");
  expect(within(rows[0]).getByText("More than one match")).toBeTruthy();
  expect(within(rows[0]).getByText(/Matched more than one TMDB title/)).toBeTruthy();
  expect(within(rows[1]).getByText("No match")).toBeTruthy();
  expect(within(rows[2]).getByText("Ignored file")).toBeTruthy();

  fireEvent.click(toggle);
  expect(screen.queryByRole("list", { name: /library problems/i })).toBeNull();
});

test("a library with one problem and one title of each kind reads in the singular", async () => {
  setup([() => json(fine({ libraryTotals: { movies: 1, episodes: 1 }, libraryProblems: [sick.libraryProblems[0]] }))]);
  const lib = await screen.findByRole("region", { name: /library/i });
  expect(within(lib).getByText("1 movie · 1 episode")).toBeTruthy();
  expect(within(lib).getByText("1 problem")).toBeTruthy();
});

test("a library with no problems says so and has nothing to unfold; an empty one counts zero", async () => {
  setup([() => json(fine({ libraryTotals: { movies: 0, episodes: 0 } }))]);

  const lib = await screen.findByRole("region", { name: /library/i });
  expect(within(lib).getByText("No problems")).toBeTruthy();
  expect(within(lib).getByText("0 movies · 0 episodes")).toBeTruthy();
  expect(within(lib).queryByRole("button")).toBeNull();
});

test("the controls come in the order Close, Check again, Show details, Show problems", async () => {
  setup([() => json(sick)], { onForget: () => {} });
  await line();

  const names = screen.getAllByRole("button").map((b) => b.getAttribute("aria-label") ?? b.textContent);
  expect(names).toEqual(["Close", "Check again", "Show details", "Show problems", "Forget on this device"]);
});

test("Close closes the page", async () => {
  const onClose = vi.fn();
  setup([() => json(sick)], { onClose });
  await line();
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  expect(onClose).toHaveBeenCalledTimes(1);
});

test("a failed read says why and Try again reads once more", async () => {
  const fetchStub = setup([() => json({ error: "something went wrong inside marquee" }, 500), () => json(sick)]);

  expect(await screen.findByRole("alert")).toHaveProperty("textContent", expect.stringContaining("something went wrong"));
  expect(screen.queryByRole("button", { name: /check again/i })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: /try again/i }));
  await line();
  expect(screen.queryByRole("alert")).toBeNull();
  expect(fetchStub).toHaveBeenCalledTimes(2);
});

test("Check again probes once more, keeps the answer on screen meanwhile and shows the new one", async () => {
  let release: () => void = () => {};
  const held = new Promise<void>((r) => (release = r));
  let n = 0;
  const slow = vi.fn(async () => {
    if (n++ === 0) return json(sick);
    await held;
    return json(fine());
  });
  render(<Health api={createApi({ baseUrl: BASE, fetch: slow as unknown as typeof fetch })} onClose={() => {}} />);
  await line();

  fireEvent.click(screen.getByRole("button", { name: /check again/i }));
  const busy = screen.getByRole("button", { name: /checking/i });
  expect(busy.getAttribute("aria-disabled")).toBe("true");
  expect(screen.getByText("Temp path")).toBeTruthy();
  fireEvent.click(busy);
  await waitFor(() => expect(slow).toHaveBeenCalledTimes(2));

  release();
  await screen.findByRole("heading", { level: 2, name: /ready to download/i });
  expect(screen.getByRole("button", { name: /check again/i })).toBeTruthy();
  expect(slow).toHaveBeenCalledTimes(2);
});

test("a failed Check again says why instead of leaving a stale answer", async () => {
  setup([() => json(sick), () => json({ error: "something went wrong inside marquee" }, 500)]);
  await line();
  fireEvent.click(screen.getByRole("button", { name: /check again/i }));
  expect(await screen.findByRole("alert")).toBeTruthy();
});

test("without a token to forget the API token section says marquee does not ask for one", async () => {
  setup([() => json(sick)]);
  const sec = await screen.findByRole("region", { name: /api token/i });
  expect(within(sec).getByText(/does not ask for a token/i)).toBeTruthy();
  expect(within(sec).queryByRole("button")).toBeNull();
});

test("Forget on this device asks first, opens on no, and only yes forgets", async () => {
  const onForget = vi.fn();
  setup([() => json(sick)], { onForget });
  const sec = await screen.findByRole("region", { name: /api token/i });

  fireEvent.click(within(sec).getByRole("button", { name: /forget on this device/i }));
  const ask = within(sec).getByRole("group", { name: /forget the saved token/i });
  expect(onForget).not.toHaveBeenCalled();
  const no = within(ask).getByRole("button", { name: "no" });
  expect(document.activeElement).toBe(no);

  fireEvent.click(no);
  expect(onForget).not.toHaveBeenCalled();
  const again = within(sec).getByRole("button", { name: /forget on this device/i });
  await waitFor(() => expect(document.activeElement).toBe(again));

  fireEvent.click(again);
  fireEvent.click(within(sec).getByRole("button", { name: "yes" }));
  expect(onForget).toHaveBeenCalledTimes(1);
});

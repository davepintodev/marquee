// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { Shell } from "./Shell";

const BASE = "http://marquee.test";
const TOKEN = "s3cret-token";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

// A marquee that wants TOKEN on every call and has an empty wall.
function marquee(token: string | null = TOKEN) {
  const seen: (string | null)[] = [];
  const fetchStub = vi.fn(async (input: Request) => {
    const auth = input.headers.get("Authorization");
    seen.push(auth);
    if (token !== null && auth !== `Bearer ${token}`) return json({ error: "this marquee needs its API token" }, 401);
    return json({ items: [] });
  });
  return { fetch: fetchStub as unknown as typeof fetch, seen };
}

beforeEach(() => {
  localStorage.clear();
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const field = () => screen.getByLabelText(/api token/i) as HTMLInputElement;
const unlock = (v: string) => {
  fireEvent.change(field(), { target: { value: v } });
  fireEvent.click(screen.getByRole("button", { name: /unlock/i }));
};

test("a marquee that wants a token asks for it once, then remembers it in the browser", async () => {
  const m = marquee();
  const first = render(<Shell baseUrl={BASE} fetch={m.fetch} path="/" />);

  await screen.findByLabelText(/api token/i);
  expect(screen.queryByRole("searchbox")).toBeNull();
  expect(field().type).toBe("password");

  unlock(TOKEN);
  await screen.findByRole("searchbox");
  expect(m.seen.at(-1)).toBe(`Bearer ${TOKEN}`);

  // Another visit on this device: no prompt, the stored token goes out at once.
  first.unmount();
  const second = marquee();
  render(<Shell baseUrl={BASE} fetch={second.fetch} path="/" />);
  await screen.findByRole("searchbox");
  expect(screen.queryByLabelText(/api token/i)).toBeNull();
  expect(second.seen[0]).toBe(`Bearer ${TOKEN}`);
});

test("a token marquee refuses is forgotten and asked for again, with the reason", async () => {
  const m = marquee();
  render(<Shell baseUrl={BASE} fetch={m.fetch} path="/" />);
  await screen.findByLabelText(/api token/i);
  expect(screen.queryByRole("alert")).toBeNull();

  unlock("wrong");

  expect(await screen.findByRole("alert")).toHaveProperty("textContent", expect.stringMatching(/not accepted/i));
  await waitFor(() => expect(field().value).toBe(""));
  expect(localStorage.getItem("marquee.token")).toBeNull();

  unlock(TOKEN);
  await screen.findByRole("searchbox");
});

test("a marquee with no token never shows the prompt", async () => {
  const m = marquee(null);
  render(<Shell baseUrl={BASE} fetch={m.fetch} path="/" />);
  await screen.findByRole("searchbox");
  expect(screen.queryByLabelText(/api token/i)).toBeNull();
  expect(m.seen.every((h) => h === null)).toBe(true);
});

test("a browser that will not store the token still unlocks for this visit", async () => {
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new DOMException("full", "QuotaExceededError");
  });
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
    throw new DOMException("blocked", "SecurityError");
  });
  const m = marquee();
  render(<Shell baseUrl={BASE} fetch={m.fetch} path="/" />);
  await screen.findByLabelText(/api token/i);
  unlock(TOKEN);
  await screen.findByRole("searchbox");
});

test("the Health page asks for the token too", async () => {
  const m = marquee();
  render(<Shell baseUrl={BASE} fetch={m.fetch} path="/health" />);
  await screen.findByLabelText(/api token/i);
});

test("every page, by path or by hash, asks for the token on a 401 and comes back to itself after it", async () => {
  for (const [path, hash, heading] of [
    ["/health", "", "Health"],
    ["/", "#/health", "Health"],
    ["/", "#/activity", "Activity"],
    ["/", "#/title/movie-1", null],
  ] as const) {
    localStorage.clear();
    window.location.hash = hash;
    const answers = (input: Request) => {
      if (input.headers.get("Authorization") !== `Bearer ${TOKEN}`) return json({ error: "this marquee needs its API token" }, 401);
      const p = new URL(input.url).pathname;
      if (p === "/api/health") return json({ status: "ok", dryRun: false, checks: [], libraryProblems: [] });
      if (p === "/api/wall") return json({ items: [] });
      return json({ error: "nope" }, 404);
    };
    const f = vi.fn(async (input: Request) => answers(input)) as unknown as typeof fetch;
    const view = render(<Shell baseUrl={BASE} fetch={f} path={path} />);

    await screen.findByLabelText(/api token/i);
    expect(screen.queryByRole("main")).toBeNull();
    unlock(TOKEN);
    if (heading) await screen.findByRole("heading", { name: heading });
    else await waitFor(() => expect(screen.queryByLabelText(/api token/i)).toBeNull());
    expect(window.location.hash).toBe(hash || "#/health");
    view.unmount();
    window.location.hash = "";
  }
});

test("/health keeps working as a path: it opens the Health page with the wall behind it", async () => {
  const m = marquee(null);
  render(<Shell baseUrl={BASE} fetch={m.fetch} path="/health" />);
  await screen.findByRole("heading", { name: "Health" });
  expect(window.location.hash).toBe("#/health");
  window.location.hash = "";
});

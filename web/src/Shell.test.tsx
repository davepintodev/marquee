// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
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
  expect(document.activeElement).toBe(field());
  expect(screen.queryByRole("searchbox")).toBeNull();

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
      if (p === "/api/health") return json({ status: "ok", dryRun: false, checks: [], libraryTotals: { movies: 0, episodes: 0 }, libraryProblems: [] });
      if (p === "/api/wall") return json({ items: [] });
      return json({ error: "nope" }, 404);
    };
    const f = vi.fn(async (input: Request) => answers(input)) as unknown as typeof fetch;
    const view = render(<Shell baseUrl={BASE} fetch={f} path={path} />);

    await screen.findByLabelText(/api token/i);
    expect(screen.queryByRole("main")).toBeNull();
    unlock(TOKEN);
    if (heading) await screen.findByRole("heading", { name: heading });
    else await waitFor(() => expect(screen.queryByLabelText(/api token/i)).toBeNull(), { timeout: 3000 });
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

// A marquee that holds back its answer to the token until the test lets it go.
function slowMarquee(token = TOKEN) {
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => (release = resolve));
  const f = vi.fn(async (input: Request) => {
    const auth = input.headers.get("Authorization");
    if (auth === null) return json({ error: "this marquee needs its API token" }, 401);
    await gate;
    return auth === `Bearer ${token}` ? json({ items: [] }) : json({ error: "no" }, 401);
  });
  return { fetch: f as unknown as typeof fetch, release };
}

const pass = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms));
const screenEl = () => document.querySelector<HTMLElement>(".screen");
const appWrap = () => document.querySelector<HTMLElement>(".shell-app");

function reduceMotion() {
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: q.includes("prefers-reduced-motion"), media: q, addEventListener() {}, removeEventListener() {} }));
}

test("a right token holds the lock until marquee accepts it, then hands over, then leaves nothing behind", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    const m = slowMarquee();
    render(<Shell baseUrl={BASE} fetch={m.fetch} path="/" />);
    await screen.findByLabelText(/api token/i);
    unlock(TOKEN);

    // The app is mounting underneath, but nobody can see or reach it yet.
    await screen.findByRole("searchbox");
    expect(screenEl()!.getAttribute("data-phase")).toBe("checking");
    expect(screen.getByRole("button", { name: "Unlocking…" })).toBeTruthy();
    expect(appWrap()!.hasAttribute("inert")).toBe(true);

    m.release();
    await waitFor(() => expect(screenEl()!.getAttribute("data-phase")).toBe("open"));
    expect(screen.getByRole("button", { name: "Unlocked" })).toBeTruthy();
    expect(appWrap()!.hasAttribute("data-handover")).toBe(true);
    expect(appWrap()!.hasAttribute("inert")).toBe(true);

    await pass(1000);
    expect(screenEl()).not.toBeNull();
    await pass(700);

    expect(screenEl()).toBeNull();
    expect(document.querySelector(".utile")).toBeNull();
    expect(appWrap()!.hasAttribute("inert")).toBe(false);
    expect(appWrap()!.hasAttribute("data-handover")).toBe(false);
    expect(screen.getByRole("searchbox")).toBeTruthy();
  } finally {
    vi.useRealTimers();
  }
});

test("a wrong token stays locked: no hand-over starts, the error reads, the field keeps focus", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    const m = slowMarquee();
    render(<Shell baseUrl={BASE} fetch={m.fetch} path="/" />);
    await screen.findByLabelText(/api token/i);
    unlock("wrong");
    await screen.findByRole("searchbox");
    expect(screenEl()!.getAttribute("data-phase")).toBe("checking");

    m.release();
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screenEl()!.getAttribute("data-phase")).toBe("ask");
    expect(document.activeElement).toBe(field());
    expect(field().readOnly).toBe(false);
    expect(screen.queryByRole("searchbox")).toBeNull();

    await pass(3000);
    expect(screenEl()).not.toBeNull();
    expect(screen.getByRole("alert")).toBeTruthy();
  } finally {
    vi.useRealTimers();
  }
});

test("with reduced motion the hand-over is a short fade", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  reduceMotion();
  try {
    const m = slowMarquee();
    render(<Shell baseUrl={BASE} fetch={m.fetch} path="/" />);
    await screen.findByLabelText(/api token/i);
    unlock(TOKEN);
    await screen.findByRole("searchbox");
    m.release();
    await waitFor(() => expect(screenEl()!.getAttribute("data-phase")).toBe("open"));

    await pass(400);

    expect(screenEl()).toBeNull();
    expect(appWrap()!.hasAttribute("inert")).toBe(false);
  } finally {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  }
});

test("a marquee that never answers does not hold the screen shut for ever", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    const f = vi.fn(async (input: Request) => (input.headers.get("Authorization") === null ? json({ error: "token" }, 401) : new Promise<Response>(() => {}))) as unknown as typeof fetch;
    render(<Shell baseUrl={BASE} fetch={f} path="/" />);
    await screen.findByLabelText(/api token/i);
    unlock(TOKEN);
    await screen.findByRole("searchbox");
    await pass(5000);
    expect(screenEl()!.getAttribute("data-phase")).toBe("checking");

    await pass(1500);
    expect(screenEl()!.getAttribute("data-phase")).toBe("open");
    await pass(1600);

    expect(screenEl()).toBeNull();
  } finally {
    vi.useRealTimers();
  }
});

test("Esc on the lock screen leaves it locked", async () => {
  const m = marquee();
  render(<Shell baseUrl={BASE} fetch={m.fetch} path="/" />);
  await screen.findByLabelText(/api token/i);
  fireEvent.keyDown(window, { key: "Escape" });
  fireEvent.keyDown(field(), { key: "Escape" });
  expect(screen.getByLabelText(/api token/i)).toBeTruthy();
  expect(screen.queryByRole("searchbox")).toBeNull();
});

test("while the screen is over it, the app underneath ignores the keyboard", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    const m = slowMarquee();
    render(<Shell baseUrl={BASE} fetch={m.fetch} path="/" />);
    await screen.findByLabelText(/api token/i);
    unlock(TOKEN);
    await screen.findByRole("searchbox");

    fireEvent.keyDown(window, { key: "x" });
    expect(window.location.hash).toBe("");

    m.release();
    await waitFor(() => expect(screenEl()!.getAttribute("data-phase")).toBe("open"));
    fireEvent.keyDown(window, { key: "y" });
    expect(window.location.hash).toBe("");

    await pass(1700);
    expect(screenEl()).toBeNull();
    fireEvent.keyDown(window, { key: "z" });
    expect(window.location.hash).toBe("#/grid");
  } finally {
    vi.useRealTimers();
    window.location.hash = "";
  }
});

test("Esc while unlocking onto a page does not close the page", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  window.location.hash = "#/health";
  try {
    const m = slowMarquee();
    render(<Shell baseUrl={BASE} fetch={m.fetch} path="/" />);
    await screen.findByLabelText(/api token/i);
    unlock(TOKEN);
    await screen.findByRole("searchbox");

    fireEvent.keyDown(window, { key: "Escape" });
    expect(window.location.hash).toBe("#/health");
  } finally {
    vi.useRealTimers();
    window.location.hash = "";
  }
});

function healthMarquee(token: string | null) {
  const f = vi.fn(async (input: Request) => {
    if (token !== null && input.headers.get("Authorization") !== `Bearer ${token}`) return json({ error: "this marquee needs its API token" }, 401);
    const p = new URL(input.url).pathname;
    if (p === "/api/health") return json({ status: "ok", dryRun: false, checks: [], libraryTotals: { movies: 0, episodes: 0 }, libraryProblems: [] });
    return json({ items: [] });
  });
  return f as unknown as typeof fetch;
}

test("Forget on this device on the Health page forgets the token and asks for it again", async () => {
  window.location.hash = "#/health";
  localStorage.setItem("marquee.token", TOKEN);
  render(<Shell baseUrl={BASE} fetch={healthMarquee(TOKEN)} path="/" />);

  fireEvent.click(await screen.findByRole("button", { name: /forget on this device/i }));
  fireEvent.click(screen.getByRole("button", { name: "Forget" }));

  await screen.findByLabelText(/api token/i);
  expect(localStorage.getItem("marquee.token")).toBeNull();
  expect(screen.queryByRole("heading", { name: "Health" })).toBeNull();

  unlock(TOKEN);
  await screen.findByRole("heading", { name: "Health" });
  window.location.hash = "";
});

test("Esc answers the forget question without closing the Health page, and closes the page once it is answered", async () => {
  window.location.hash = "#/health";
  localStorage.setItem("marquee.token", TOKEN);
  render(<Shell baseUrl={BASE} fetch={healthMarquee(TOKEN)} path="/" />);

  fireEvent.click(await screen.findByRole("button", { name: /forget on this device/i }));
  fireEvent.keyDown(screen.getByRole("button", { name: "Keep it" }), { key: "Escape" });
  expect(screen.getByRole("heading", { name: "Health" })).toBeTruthy();
  expect(screen.queryByRole("group", { name: /forget the saved token/i })).toBeNull();
  expect(localStorage.getItem("marquee.token")).toBe(TOKEN);

  fireEvent.keyDown(screen.getByRole("button", { name: /forget on this device/i }), { key: "Escape" });
  await waitFor(() => expect(screen.queryByRole("heading", { name: "Health" })).toBeNull());
  window.location.hash = "";
});

test("a marquee with no token has nothing to forget", async () => {
  window.location.hash = "#/health";
  render(<Shell baseUrl={BASE} fetch={healthMarquee(null)} path="/" />);
  await screen.findByRole("heading", { name: "Health" });
  expect(screen.queryByRole("button", { name: /forget on this device/i })).toBeNull();
  window.location.hash = "";
});

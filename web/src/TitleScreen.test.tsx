// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { Title, TitleAbout } from "./api/types";
import { aboutOf, dune, house, profiles } from "./fixtures";
import { FakeEventSource, json, setupApp, titleApi } from "./testkit";

beforeEach(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  vi.useFakeTimers({ toFake: ["Date"], now: new Date("2026-11-18T12:00:00Z") });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
  window.location.hash = "";
});

const seasonOf = (t: Title, number: number, monitored: boolean): Title => ({ ...t, seasons: t.seasons.map((s) => (s.number === number ? { ...s, episodes: s.episodes.map((e) => ({ ...e, monitored })) } : s)) });
const episodeList = () => within(screen.getByRole("list", { name: /episodes$/ })).getAllByRole("listitem");
const open = async (title: Title, extra?: Parameters<typeof titleApi>[1], about: TitleAbout | null = aboutOf()) => {
  const world = setupApp(titleApi(title, extra, about), `#/title/${title.id}`);
  await screen.findByRole("heading", { name: title.title, level: 1 });
  return world;
};

test("a series opens on its season with each episode's state, and 'N missing' on the season row", async () => {
  await open(house());

  expect(screen.getByText("2004 · Series · 2 seasons · 6 episodes")).toBeTruthy();
  expect(screen.getByText("A diagnostician.")).toBeTruthy();
  const row = screen.getByRole("heading", { name: /Season 4/ });
  expect(row.textContent).toBe("Season 41 of 4 · 2 missing · 1 not aired");
  const rows = episodeList();
  expect(rows).toHaveLength(5);
  expect(within(rows[0]).getByText("Downloaded")).toBeTruthy();
  expect(within(rows[1]).getByText("Missing")).toBeTruthy();
  expect(within(rows[1]).getByText(/next search in 6h/i)).toBeTruthy();
  expect(within(rows[2]).getByText("Downloading 40%")).toBeTruthy();
  expect(within(rows[3]).getByText("Stalled")).toBeTruthy();
  expect(within(rows[3]).getByText("Not monitored")).toBeTruthy();
  expect(within(rows[4]).getByText("Not aired yet")).toBeTruthy();
  expect(within(rows[4]).getByRole("button", { name: /S04E05 Next Week/ }).getAttribute("aria-disabled")).toBe("true");
});

test("the details block has the description, two actions and the four facts, and the scores wear their service's name", async () => {
  await open(house());

  expect(screen.getByRole("button", { name: "Search automatically" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Monitored. Stop monitoring this title" }).getAttribute("aria-pressed")).toBe("true");
  const facts = Object.fromEntries([...document.querySelectorAll(".ts-factlist div")].map((d) => [d.querySelector("dt")!.textContent, d.querySelector("dd")!.textContent]));
  expect(facts).toEqual({ Genres: "Drama, Mystery, Comedy", Runtime: "44 min", Status: "Ended", Language: "English" });
  const scores = within(screen.getByRole("list", { name: "Scores" })).getAllByRole("listitem");
  expect(scores.map((s) => s.getAttribute("aria-label"))).toEqual([
    "TMDB rating: 8.2 out of 10, 2,900 votes",
    "IMDb rating: 8.4 out of 10, 310,000 votes",
    "Rotten Tomatoes rating: 95 percent",
  ]);
  expect(scores.map((s) => s.textContent)).toEqual(["8.2", "8.4", "95%"]);
});

test("with TMDB's score alone the screen shows that one mark and no empty slot", async () => {
  await open(house(), undefined, aboutOf({ ratings: [{ source: "tmdb", score: 8.2, scale: 10, votes: 10 }] }));
  expect(within(screen.getByRole("list", { name: "Scores" })).getAllByRole("listitem")).toHaveLength(1);

  cleanup();
  await open(house(), undefined, aboutOf({ ratings: [] }));
  expect(screen.queryByRole("list", { name: "Scores" })).toBeNull();
});

test("cast and 'More like this' come from the about, a similar title opens its own page, and a failed about just leaves them out", async () => {
  await open(house());
  expect(screen.getByRole("heading", { name: "Cast" })).toBeTruthy();
  expect(screen.getByText("Gregory House")).toBeTruthy();
  expect(screen.getByText("LE")).toBeTruthy();
  expect(screen.getAllByRole("img", { hidden: true }).length).toBeGreaterThan(0);
  expect(document.querySelector<HTMLImageElement>(".ts-face img")!.src).toBe("http://marquee.test/api/people/11/image");
  fireEvent.click(screen.getByRole("button", { name: "Scrubs, 2001" }));
  await waitFor(() => expect(window.location.hash).toBe("#/title/series-9"));

  cleanup();
  window.location.hash = "";
  await open(house(), undefined, null);
  expect(screen.queryByRole("heading", { name: "Cast" })).toBeNull();
  expect(screen.queryByRole("heading", { name: "More like this" })).toBeNull();
  expect(screen.queryByRole("list", { name: "Scores" })).toBeNull();
  expect(screen.queryByRole("alert")).toBeNull();
  expect(screen.getByRole("button", { name: "History" })).toBeTruthy();
});

test("a broken cast photo falls back to initials", async () => {
  await open(house());
  fireEvent.error(document.querySelector(".ts-face img")!);
  expect(screen.getByText("HL")).toBeTruthy();
});

test("a long cast shows twelve and the rest on request", async () => {
  const cast = Array.from({ length: 15 }, (_, i) => ({ personId: 100 + i, name: `Actor ${i}`, character: "" }));
  await open(house(), undefined, aboutOf({ cast }));
  expect(document.querySelectorAll(".ts-cast li")).toHaveLength(12);
  fireEvent.click(screen.getByRole("button", { name: "Show all 15" }));
  expect(document.querySelectorAll(".ts-cast li")).toHaveLength(15);
  fireEvent.click(screen.getByRole("button", { name: "Show fewer" }));
  expect(document.querySelectorAll(".ts-cast li")).toHaveLength(12);
});

test("the season strip switches seasons, with arrows too, and keeps Specials last", async () => {
  const withSpecials = house();
  withSpecials.seasons = [{ number: 0, episodes: [{ id: "s00e01", number: 1, name: "Pilot cut", airDate: "2004-01-01", monitored: true, state: "downloaded" }] }, ...withSpecials.seasons];
  await open(withSpecials);

  const tabs = screen.getAllByRole("tab");
  expect(tabs.map((t) => t.textContent)).toEqual(["3", "4", "SP"]);
  expect(screen.getByRole("tab", { name: /^Season 4/ }).getAttribute("aria-selected")).toBe("true");
  fireEvent.click(screen.getByRole("tab", { name: /^Season 3/ }));
  expect(screen.getByRole("heading", { name: /Season 3/ })).toBeTruthy();
  expect(episodeList()).toHaveLength(1);

  fireEvent.keyDown(screen.getByRole("tab", { name: /^Season 3/ }), { key: "End" });
  expect(screen.getByRole("heading", { name: /Specials/ })).toBeTruthy();
  expect(document.activeElement).toBe(screen.getByRole("tab", { name: /^Specials/ }));
});

test("a season folds from its row, and 'Missing' lists only what is still wanted", async () => {
  await open(house());
  const toggle = screen.getByRole("button", { name: /Season 4/ });

  fireEvent.click(screen.getByRole("button", { name: "Missing" }));
  expect(episodeList().map((r) => r.querySelector(".ts-ep-n")!.textContent)).toEqual(["02", "03", "04"]);
  fireEvent.click(screen.getByRole("button", { name: "All" }));
  expect(episodeList()).toHaveLength(5);

  fireEvent.click(toggle);
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  expect(screen.queryByRole("list", { name: /episodes$/ })).toBeNull();
  fireEvent.click(toggle);
  expect(episodeList()).toHaveLength(5);
});

test("a season with nothing missing says so under 'Missing'", async () => {
  await open(house());
  fireEvent.click(screen.getByRole("tab", { name: /^Season 3/ }));
  fireEvent.click(screen.getByRole("button", { name: "Missing" }));
  expect(screen.getByText("Nothing is missing from this season.")).toBeTruthy();
});

test("the season button monitors the whole season, says so, and sends one call when pressed twice", async () => {
  let release: (r: Response) => void = () => {};
  const { writes } = await open(house(), (url, init) =>
    init.method === "PUT" && url.pathname === "/api/titles/series-2/seasons/4" ? (new Promise<Response>((resolve) => (release = resolve)) as unknown as Response) : undefined,
  );
  const button = screen.getByRole("button", { name: "Monitoring 4 of 5" });

  fireEvent.click(button);
  await waitFor(() => expect(button.getAttribute("aria-disabled")).toBe("true"));
  fireEvent.click(button);
  release(json(seasonOf(house(), 4, true)));

  expect((await screen.findByRole("status")).textContent).toBe("Season 4 monitored.");
  expect(screen.getByRole("button", { name: "Monitoring season" })).toBeTruthy();
  expect(writes).toEqual([{ method: "PUT", path: "/api/titles/series-2/seasons/4", body: { monitored: true } }]);
});

test("a monitored season is un-monitored from the same button, and a failure is said in words", async () => {
  const all = seasonOf(house(), 4, true);
  const { writes } = await open(all, (url, init) => {
    if (init.method !== "PUT") return undefined;
    return url.pathname.endsWith("/4") ? json({ error: "no such season" }, 404) : undefined;
  });

  fireEvent.click(screen.getByRole("button", { name: "Monitoring season" }));

  expect((await screen.findByRole("alert")).textContent).toContain("no such season");
  expect(screen.getByRole("button", { name: "Monitoring season" })).toBeTruthy();
  expect(screen.queryByRole("status")).toBeNull();
  expect(writes).toEqual([{ method: "PUT", path: "/api/titles/series-2/seasons/4", body: { monitored: false } }]);
});

test("an episode and the title can be un-monitored, and the page shows the answer", async () => {
  const off = house();
  off.seasons[1].episodes[1] = { ...off.seasons[1].episodes[1], monitored: false };
  const { writes } = await open(house(), (url, init) => {
    if (init.method === "PATCH" && url.pathname === "/api/titles/series-2/episodes/s04e02") return json(off);
    if (init.method === "PATCH" && url.pathname === "/api/titles/series-2") return json(house({ monitored: false }));
  });

  fireEvent.click(screen.getByRole("button", { name: "Monitored. Stop monitoring Big Baby" }));
  await screen.findByRole("button", { name: "Not monitored. Monitor Big Baby" });
  fireEvent.click(screen.getByRole("button", { name: "Monitored. Stop monitoring this title" }));
  await screen.findByRole("button", { name: "Not monitored. Start monitoring this title" });

  expect(writes).toEqual([
    { method: "PATCH", path: "/api/titles/series-2/episodes/s04e02", body: { monitored: false } },
    { method: "PATCH", path: "/api/titles/series-2", body: { monitored: false } },
  ]);
  expect(screen.getByText("Not monitored", { selector: ".tag" })).toBeTruthy();
});

test("Search automatically on an episode and on the title skip the backoff", async () => {
  const { writes } = await open(house(), (url, init) => (init.method === "POST" && url.pathname.endsWith("/search") ? json(house({ historySummary: "Searching now." })) : undefined));

  fireEvent.click(screen.getByRole("button", { name: "Search automatically for Big Baby" }));
  await screen.findByText("Searching for Big Baby now.");
  fireEvent.click(screen.getByRole("button", { name: "Search automatically" }));
  await screen.findByText("Searching for “House” now.");

  expect(writes.map((w) => `${w.method} ${w.path}`)).toEqual(["POST /api/titles/series-2/episodes/s04e02/search", "POST /api/titles/series-2/search"]);
});

test("the episode that is downloading cannot be searched, and an unaired one has no search at all", async () => {
  await open(house());
  expect(screen.getByRole("button", { name: "Downloading now You Don't Want to Know" }).getAttribute("aria-disabled")).toBe("true");
  expect(screen.queryByRole("button", { name: /Search automatically for Next Week/ })).toBeNull();
});

test("download progress moves the episode without a reload", async () => {
  const { calls } = await open(house());
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));
  const before = calls.length;

  FakeEventSource.open()[0].emit("progress", { downloadId: "d", titleId: "series-2", episode: "s04e03", state: "downloading", percent: 61, bytesDone: 61, bytesTotal: 100 });

  expect(await screen.findByText("Downloading 61%")).toBeTruthy();
  expect(calls.length).toBe(before);
});

test("TorBox caching progress shows on the episode as Caching, not Downloading", async () => {
  await open(house());
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));

  FakeEventSource.open()[0].emit("progress", { downloadId: "d", titleId: "series-2", episode: "s04e03", state: "caching", percent: 42, bytesDone: 0, bytesTotal: 0 });

  expect(await screen.findByText("Caching 42%")).toBeTruthy();
  expect(screen.queryByText("Downloading 42%")).toBeNull();
  expect(screen.getByRole("button", { name: /^Caching now / }).getAttribute("aria-disabled")).toBe("true");
});

test("an open episode survives the title being refreshed from under it", async () => {
  const { calls } = await open(house(), (url) => (url.pathname === "/api/titles/series-2/releases" ? json({ candidates: [], dropped: [] }) : undefined));
  fireEvent.click(screen.getByRole("button", { name: /S04E02 Big Baby/ }));
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));
  const loads = () => calls.filter((u) => u.pathname === "/api/titles/series-2").length;
  const before = loads();

  FakeEventSource.open()[0].emit("title", { titleId: "series-2" });
  await waitFor(() => expect(loads()).toBe(before + 1));

  expect(screen.getByRole("button", { name: /S04E02 Big Baby/ }).getAttribute("aria-expanded")).toBe("true");
});

test("a movie has releases and no seasons", async () => {
  await open(dune(), undefined, aboutOf());
  expect(screen.getByRole("heading", { name: "Releases" })).toBeTruthy();
  expect(screen.queryByRole("heading", { name: "Episodes" })).toBeNull();
  expect(screen.getByText("2021 · Movie")).toBeTruthy();
});

test("a series not in the library says its episodes come once it is added", async () => {
  setupApp((url) => {
    if (url.pathname === "/api/wall") return json({ items: [{ source: "trending", title: { ...house(), inLibrary: false, monitored: undefined, badge: undefined } }] });
    return json({ error: "nope" }, 404);
  }, "#/title/series-2");

  expect(await screen.findByText("Seasons and episodes show up once the series is added.")).toBeTruthy();
  expect(screen.getByRole("heading", { name: "House", level: 1 })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Add & monitor" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Remove…" })).toBeNull();
});

test("an unknown title says so and offers to try again", async () => {
  setupApp(() => json({ error: "no such title" }, 404), "#/title/series-9");
  expect((await screen.findByRole("alert")).textContent).toContain("no such title");
  expect(screen.getByRole("button", { name: "Try again" })).toBeTruthy();
});

test("Back and Esc leave the page for the library grid", async () => {
  await open(house());
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  await waitFor(() => expect(window.location.hash).toBe("#/grid"));
  expect(screen.queryByRole("heading", { name: "House", level: 1 })).toBeNull();

  window.location.hash = "#/title/series-2";
  await screen.findByRole("heading", { name: "House", level: 1 });
  fireEvent.keyDown(window, { key: "Escape" });
  await waitFor(() => expect(window.location.hash).toBe("#/grid"));
});

test("Esc folds an open episode first and leaves the page where it is", async () => {
  await open(house(), (url) => (url.pathname === "/api/titles/series-2/releases" ? json({ candidates: [], dropped: [] }) : undefined));
  const row = screen.getByRole("button", { name: /S04E02 Big Baby/ });
  fireEvent.click(row);
  await screen.findByText(/has nothing for/);

  fireEvent.keyDown(row, { key: "Escape" });

  await waitFor(() => expect(row.getAttribute("aria-expanded")).toBe("false"));
  expect(window.location.hash).toBe("#/title/series-2");
  fireEvent.keyDown(window, { key: "Escape" });
  await waitFor(() => expect(window.location.hash).toBe("#/grid"));
});

test("Esc pressed while an episode row is folding does not leave the page, and neither does Esc with focus on the page body", async () => {
  await open(house(), (url) => (url.pathname === "/api/titles/series-2/releases" ? json({ candidates: [], dropped: [] }) : undefined));
  const row = screen.getByRole("button", { name: /S04E02 Big Baby/ });
  fireEvent.click(row);
  await screen.findByText(/has nothing for/);

  (document.activeElement as HTMLElement | null)?.blur();
  fireEvent.keyDown(document.body, { key: "Escape" });
  fireEvent.keyDown(document.body, { key: "Escape" });
  expect(window.location.hash).toBe("#/title/series-2");
  await waitFor(() => expect(row.getAttribute("aria-expanded")).toBe("false"));

  fireEvent.keyDown(document.body, { key: "Escape" });
  await waitFor(() => expect(window.location.hash).toBe("#/grid"));
});

test("Esc closes the attempts explanation first, however it was opened, then the open row, then leaves the page", async () => {
  await open(house(), (url) => (url.pathname === "/api/titles/series-2/releases" ? json({ candidates: [], dropped: [] }) : undefined));
  const info = screen.getByRole("button", { name: "About these attempts" });
  const tip = screen.getByRole("tooltip", { hidden: true });
  const row = screen.getByRole("button", { name: /S04E02 Big Baby/ });
  fireEvent.click(row);
  await screen.findByText(/has nothing for/);

  fireEvent.mouseEnter(info.parentElement!);
  (document.activeElement as HTMLElement | null)?.blur();
  fireEvent.keyDown(document.body, { key: "Escape" });
  expect(info.parentElement!.hasAttribute("data-dismissed")).toBe(true);
  expect(row.getAttribute("aria-expanded")).toBe("true");
  expect(window.location.hash).toBe("#/title/series-2");

  fireEvent.mouseLeave(info.parentElement!);
  expect(info.parentElement!.hasAttribute("data-dismissed")).toBe(false);
  vi.spyOn(HTMLElement.prototype, "matches").mockImplementation(function (this: HTMLElement, sel: string) {
    return sel === ":focus-visible" ? this === info : Element.prototype.matches.call(this, sel);
  });
  fireEvent.focus(info);
  fireEvent.keyDown(info, { key: "Escape" });
  expect(info.parentElement!.hasAttribute("data-dismissed")).toBe(true);
  expect(row.getAttribute("aria-expanded")).toBe("true");
  expect(tip.hasAttribute("data-open")).toBe(false);

  fireEvent.keyDown(document.body, { key: "Escape" });
  await waitFor(() => expect(row.getAttribute("aria-expanded")).toBe("false"));
  expect(window.location.hash).toBe("#/title/series-2");
});

test("History is one muted line with the progress and the next try, closed until opened", async () => {
  await open(house());
  const toggle = screen.getByRole("button", { name: "History" });

  expect(screen.getByRole("heading", { name: "History", level: 2 }).contains(toggle)).toBe(true);
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  expect(screen.queryByRole("list", { name: "History" })).toBeNull();
  expect(screen.getByRole("img", { name: "2 of 5 aired episodes downloaded" }).textContent).toBe("2 of 5downloaded");
  expect(screen.getByText("next try in 6h")).toBeTruthy();

  fireEvent.click(toggle);
  const list = screen.getByRole("list", { name: "History" });
  expect(toggle.getAttribute("aria-expanded")).toBe("true");
  expect(within(list).getAllByRole("listitem")).toHaveLength(3);
  fireEvent.click(toggle);
  expect(screen.queryByRole("list", { name: "History" })).toBeNull();
});

test("a failure in History is a reason in bold, a dot and no pill, and a retryable one has a quiet Retry", async () => {
  const { writes } = await open(house(), (url, init) => (init.method === "POST" && url.pathname === "/api/history/h2/retry" ? json(house({ historySummary: "Searching again." })) : undefined));
  fireEvent.click(screen.getByRole("button", { name: "History" }));
  const rows = within(screen.getByRole("list", { name: "History" })).getAllByRole("listitem");

  expect(rows[0].getAttribute("data-failed")).toBe("true");
  expect(rows[0].getAttribute("data-reason")).toBe("stalled");
  expect(within(rows[0]).getByText("Stalled.", { selector: "b" })).toBeTruthy();
  expect(rows[1].getAttribute("data-reason")).toBe("not_video");
  expect(within(rows[1]).queryByRole("button")).toBeNull();
  expect(rows[2].getAttribute("data-failed")).toBeNull();
  expect(rows[0].querySelector(".badge, .reason, .pill")).toBeNull();

  const retryBtn = within(rows[0]).getByRole("button", { name: "Retry S04E02" });
  expect(retryBtn.classList.contains("link-btn")).toBe(true);
  expect(retryBtn.querySelector("svg")).toBeNull();
  fireEvent.click(retryBtn);
  await screen.findByText("Searching for “House” again.");
  expect(writes).toEqual([{ method: "POST", path: "/api/history/h2/retry", body: undefined }]);
});

test("opening a movie keeps focus on Back instead of jumping to Search releases", async () => {
  await open(dune());
  expect(screen.getByRole("button", { name: "Search releases" })).toBeTruthy();
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Back" }));
});

test("Season, History, and Cast folds all use the shared trailing rotating chevron", async () => {
  const manyCast = Array.from({ length: 14 }, (_, i) => ({ personId: i + 1, name: `Actor ${i + 1}`, character: `Role ${i + 1}` }));
  await open(house(), (url) => (url.pathname.endsWith("/about") ? json({ ratings: [], genres: [], cast: manyCast, similar: [] }) : undefined));

  const seasonBtn = screen.getByRole("button", { name: /Season 4/ });
  expect(seasonBtn.querySelector(".fold-chev")?.getAttribute("data-open")).toBe("true");

  const histBtn = screen.getByRole("button", { name: "History" });
  expect(histBtn.querySelector(".fold-chev")?.getAttribute("data-open")).toBe("false");
  fireEvent.click(histBtn);
  expect(histBtn.querySelector(".fold-chev")?.getAttribute("data-open")).toBe("true");

  const castBtn = await screen.findByRole("button", { name: "Show all 14" });
  expect(castBtn.querySelector(".fold-chev")?.getAttribute("data-open")).toBe("false");
  fireEvent.click(castBtn);
  expect(castBtn.querySelector(".fold-chev")?.getAttribute("data-open")).toBe("true");
});

test("the '?' beside 'next try' explains the attempts on hover, focus or tap, and Esc closes only it", async () => {
  await open(house());
  const info = screen.getByRole("button", { name: "About these attempts" });
  const tip = screen.getByRole("tooltip", { hidden: true });

  expect(info.getAttribute("aria-expanded")).toBe("false");
  expect(tip.hasAttribute("data-open")).toBe(false);
  expect(tip.textContent).toContain("Tried 3 releases, 1 stalled, 2 fake, retrying in 6h.");
  expect(tip.textContent).toContain("Stalled, ");
  expect(tip.textContent).toContain("Fake file, ");

  fireEvent.click(info);
  expect(info.getAttribute("aria-expanded")).toBe("true");
  expect(tip.hasAttribute("data-open")).toBe(true);

  fireEvent.keyDown(info, { key: "Escape" });
  expect(info.getAttribute("aria-expanded")).toBe("false");
  expect(window.location.hash).toBe("#/title/series-2");

  fireEvent.click(info);
  fireEvent.pointerDown(document.body);
  expect(info.getAttribute("aria-expanded")).toBe("false");
});

test("a title that was never tried has no 'next try' and no '?'", async () => {
  await open(house({ nextSearchAt: null, history: [], historySummary: "" }));
  expect(screen.queryByRole("button", { name: "About these attempts" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "History" }));
  expect(screen.getByText("Nothing has happened yet.")).toBeTruthy();
});

test("Remove from marquee is a row at the foot that says what happens, and asks before it does anything", async () => {
  const { writes } = await open(house(), (url, init) => (init.method === "DELETE" && url.pathname === "/api/titles/series-2" ? new Response(null, { status: 204 }) : undefined));
  const group = screen.getByRole("group", { name: "Remove from marquee" });

  expect(within(group).getByText("marquee stops searching for it. The files on disk stay.")).toBeTruthy();
  fireEvent.click(within(group).getByRole("button", { name: "Remove…" }));

  const keep = within(group).getByRole("button", { name: "Keep it" });
  expect(document.activeElement).toBe(keep);
  expect(writes).toEqual([]);
  expect(within(group).getByText("Remove “House”?")).toBeTruthy();

  fireEvent.click(keep);
  expect(within(group).getByRole("button", { name: "Remove…" })).toBeTruthy();
  expect(document.activeElement).toBe(within(group).getByRole("button", { name: "Remove…" }));
  expect(writes).toEqual([]);
});

test("Esc cancels the remove question and does not leave the page; Enter on the focused 'Keep it' removes nothing", async () => {
  const { writes } = await open(house());
  const group = screen.getByRole("group", { name: "Remove from marquee" });
  fireEvent.click(within(group).getByRole("button", { name: "Remove…" }));

  fireEvent.keyDown(within(group).getByRole("button", { name: "Keep it" }), { key: "Escape" });

  expect(within(group).getByRole("button", { name: "Remove…" })).toBeTruthy();
  expect(window.location.hash).toBe("#/title/series-2");
  fireEvent.click(within(group).getByRole("button", { name: "Remove…" }));
  fireEvent.click(document.activeElement as HTMLElement);
  expect(writes).toEqual([]);
  expect(within(group).getByRole("button", { name: "Remove…" })).toBeTruthy();
});

test("confirming removes the title, goes back to the grid and says the files stay", async () => {
  const { writes } = await open(house(), (url, init) => (init.method === "DELETE" && url.pathname === "/api/titles/series-2" ? new Response(null, { status: 204 }) : undefined));
  const group = screen.getByRole("group", { name: "Remove from marquee" });
  fireEvent.click(within(group).getByRole("button", { name: "Remove…" }));

  fireEvent.click(within(group).getByRole("button", { name: "Yes, remove “House”" }));

  await waitFor(() => expect(window.location.hash).toBe("#/grid"));
  expect(writes).toEqual([{ method: "DELETE", path: "/api/titles/series-2", body: undefined }]);
  expect((await screen.findByRole("status")).textContent).toBe("Removed “House” from marquee. Its files are still on disk.");
});

test("a failed removal is said in words and the title stays", async () => {
  await open(house(), (_url, init) => (init.method === "DELETE" ? json({ error: "that title is not in the library" }, 404) : undefined));
  const group = screen.getByRole("group", { name: "Remove from marquee" });
  fireEvent.click(within(group).getByRole("button", { name: "Remove…" }));
  fireEvent.click(within(group).getByRole("button", { name: "Yes, remove “House”" }));

  expect((await screen.findByRole("alert")).textContent).toContain("that title is not in the library");
  expect(window.location.hash).toBe("#/title/series-2");
});

test("the screen's own fade is decided once, when it mounts: a morph in progress suppresses it for good", async () => {
  document.documentElement.setAttribute("data-morph", "");
  try {
    await open(house());
    expect(screen.getByRole("main").hasAttribute("data-enter")).toBe(false);
    document.documentElement.removeAttribute("data-morph");
    fireEvent.click(screen.getByRole("button", { name: "History" }));
    expect(screen.getByRole("main").hasAttribute("data-enter")).toBe(false);
  } finally {
    document.documentElement.removeAttribute("data-morph");
  }
  cleanup();
  window.location.hash = "";
  await open(house());
  expect(screen.getByRole("main").hasAttribute("data-enter")).toBe(true);
});

const withProfiles = (extra?: Parameters<typeof titleApi>[1]): Parameters<typeof titleApi>[1] => (url, init) => (url.pathname === "/api/profiles" ? json(profiles) : extra?.(url, init));
const profilePicker = () => screen.findByRole("combobox", { name: "Quality profile" }) as Promise<HTMLSelectElement>;

test("a title in the library shows its profile, the default of its kind when it has none of its own", async () => {
  await open(dune(), withProfiles());
  expect((await profilePicker()).value).toBe("tv-safe");

  cleanup();
  window.location.hash = "";
  await open(dune({ profile: "everything" }), withProfiles());
  expect((await profilePicker()).value).toBe("everything");
});

test("changing the profile saves it and says so", async () => {
  const { writes } = await open(
    dune(),
    withProfiles((url, init) => (init.method === "PATCH" && url.pathname === "/api/titles/movie-1" ? json(dune({ profile: "everything" })) : undefined)),
  );

  fireEvent.change(await profilePicker(), { target: { value: "everything" } });

  expect((await screen.findByRole("status")).textContent).toBe("“Dune” now uses the everything profile.");
  expect(writes).toEqual([{ method: "PATCH", path: "/api/titles/movie-1", body: { profile: "everything" } }]);
  expect((await profilePicker()).value).toBe("everything");
});

test("a refused profile change is said in words and the picker goes back to the saved profile", async () => {
  await open(dune({ profile: "everything" }), withProfiles((_url, init) => (init.method === "PATCH" ? json({ error: "unknown profile" }, 400) : undefined)));

  fireEvent.change(await profilePicker(), { target: { value: "tv-safe" } });

  expect((await screen.findByRole("alert")).textContent).toContain("unknown profile");
  expect((await profilePicker()).value).toBe("everything");
  expect(screen.queryByRole("status")).toBeNull();
});

test("without profiles from the API the screen has no picker", async () => {
  await open(dune());
  await screen.findByRole("button", { name: "Search automatically" });
  expect(screen.queryByRole("combobox", { name: "Quality profile" })).toBeNull();
});

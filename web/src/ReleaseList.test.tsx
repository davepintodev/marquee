// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { Releases, Title } from "./api/types";
import { aboutOf, dune, house, queued, release, rels } from "./fixtures";
import { json, setupApp, titleApi } from "./testkit";

beforeEach(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.location.hash = "";
});

const serve = (releases: Releases | Response | (() => Promise<Response>), extra?: Parameters<typeof titleApi>[1]) => (url: URL, init: Request) => {
  if (url.pathname.endsWith("/releases")) return typeof releases === "function" ? releases() : releases instanceof Response ? releases : json(releases);
  return extra?.(url, init);
};

async function openMovie(releases: Parameters<typeof serve>[0] = rels, extra?: Parameters<typeof titleApi>[1], movie: Title = dune()) {
  const world = setupApp(titleApi(movie, serve(releases, extra), aboutOf()), `#/title/${movie.id}`);
  await screen.findByRole("heading", { name: movie.title, level: 1 });
  fireEvent.click(await screen.findByRole("button", { name: "Search releases" }));
  return world;
}

const table = () => screen.findByRole("list", { name: "Releases for Dune" });
const namesOf = (list: HTMLElement) => within(list).getAllByRole("listitem").map((r) => r.querySelector(".rt-file")!.firstChild!.textContent);

test("a movie waits for a click before asking AIOStreams, and the click asks once and adds nothing", async () => {
  const { calls, writes } = setupApp(titleApi(dune(), serve(rels), aboutOf()), "#/title/movie-1");
  await screen.findByRole("heading", { name: "Dune", level: 1 });
  expect(calls.some((u) => u.pathname.endsWith("/releases"))).toBe(false);

  fireEvent.click(screen.getByRole("button", { name: "Search releases" }));
  await table();

  expect(calls.filter((u) => u.pathname.endsWith("/releases")).map((u) => u.search)).toEqual([""]);
  expect(writes).toEqual([]);
});

test("the list is in marquee's order with a hint saying so, quality bold over its source, size, source and cache", async () => {
  await openMovie();
  const list = await table();

  expect(screen.getByText("in marquee’s order")).toBeTruthy();
  const rows = within(list).getAllByRole("listitem");
  expect(namesOf(list)).toEqual(["Dune.2021.2160p.WEB-DL", "Dune.2021.1080p.BluRay.x264", "Dune.2021.1080p.WEB-DL"]);
  const first = rows[0];
  expect(first.querySelector(".rt-q b")!.textContent).toBe("2160p");
  expect(first.querySelector(".rt-q small")!.textContent).toBe("WEBDL");
  expect(within(first).getByText("32 GB")).toBeTruthy();
  expect(within(first).getByText("Comet")).toBeTruthy();
  expect(within(first).getByText("Cached")).toBeTruthy();
});

test("only marquee's pick is the solid one; every other Pick is the plain word", async () => {
  await openMovie();
  const rows = within(await table()).getAllByRole("listitem");

  expect(rows.map((r) => r.getAttribute("data-top"))).toEqual(["true", null, null]);
  expect(within(rows[0]).getByRole("button", { name: "Pick Dune.2021.2160p.WEB-DL" }).textContent).toBe("Pick");
  expect(within(rows[1]).getByRole("button", { name: "Pick Dune.2021.1080p.BluRay.x264" }).textContent).toBe("Pick");
  expect(screen.queryByText("Pick anyway")).toBeNull();
});

test("the resolutions are filters without counts, and a filter lists everything it matches", async () => {
  await openMovie();
  await table();
  const group = screen.getByRole("group", { name: "Filter by resolution" });

  expect(within(group).getAllByRole("button").map((b) => b.textContent)).toEqual(["All", "2160p", "1080p", "720p"]);
  fireEvent.click(within(group).getByRole("button", { name: "720p" }));

  expect(within(group).getByRole("button", { name: "720p" }).getAttribute("aria-pressed")).toBe("true");
  expect(namesOf(screen.getByRole("list", { name: "Releases for Dune" }))).toEqual(["Dune.2021.720p.BluRay", "Dune.2021.720p.WEB"]);
  expect(screen.queryByText("in marquee’s order")).toBeNull();
  expect(screen.queryByRole("button", { name: /more releases/ })).toBeNull();
  fireEvent.click(within(group).getByRole("button", { name: "All" }));
  expect(within(screen.getByRole("list", { name: "Releases for Dune" })).getAllByRole("listitem")).toHaveLength(3);
});

test("a single resolution offers no filter", async () => {
  await openMovie({ candidates: [rels.candidates[0], release("rel-x", "Dune.2021.2160p.REMUX", "Remux-2160p", 60_000_000_000, "Comet")], dropped: [] });
  await table();
  expect(screen.queryByRole("group", { name: "Filter by resolution" })).toBeNull();
});

test("the rest of the list sits under a chevron 'Show more' fold, which opens and closes it", async () => {
  await openMovie();
  const more = await screen.findByRole("button", { name: "Show 2 more releases" });

  expect(more.textContent).toBe("Show more");
  expect(more.getAttribute("aria-expanded")).toBe("false");
  expect(more.querySelector(".fold-chev")?.getAttribute("data-open")).toBe("false");
  fireEvent.click(more);
  expect(more.getAttribute("aria-expanded")).toBe("true");
  expect(more.querySelector(".fold-chev")?.getAttribute("data-open")).toBe("true");
  expect(namesOf(screen.getByRole("list", { name: "Releases for Dune" }))).toHaveLength(5);
  fireEvent.click(more);
  expect(namesOf(screen.getByRole("list", { name: "Releases for Dune" }))).toHaveLength(3);
});

test("a short list has no fold", async () => {
  await openMovie({ candidates: rels.candidates.slice(0, 4), dropped: [] });
  await table();
  expect(screen.queryByRole("button", { name: /more releases/ })).toBeNull();
});

test("Size sorts the list both ways and then shows everything, with no 'marquee's order' hint", async () => {
  await openMovie();
  await table();

  fireEvent.click(screen.getByRole("button", { name: /^Size/ }));
  expect(namesOf(screen.getByRole("list", { name: "Releases for Dune" }))[0]).toBe("Dune.2021.2160p.WEB-DL");
  expect(within(screen.getByRole("list", { name: "Releases for Dune" })).getAllByRole("listitem")).toHaveLength(5);
  expect(screen.getByRole("button", { name: /^Size/ }).getAttribute("aria-sort")).toBe("descending");
  fireEvent.click(screen.getByRole("button", { name: /^Size/ }));
  expect(namesOf(screen.getByRole("list", { name: "Releases for Dune" }))[0]).toBe("Dune.2021.720p.WEB");
  expect(screen.queryByText("in marquee’s order")).toBeNull();
  expect(screen.queryAllByRole("listitem").filter((r) => r.getAttribute("data-top") === "true")).toHaveLength(0);
});

test("dropped releases sit behind their own quieter fold, with the reason, and only a blocklisted one can be picked anyway", async () => {
  await openMovie();
  await table();
  const fold = screen.getByRole("button", { name: "Show 3 dropped releases" });

  expect(fold.textContent).toBe("Dropped releases");
  expect(fold.querySelector(".fold-chev")?.getAttribute("data-open")).toBe("false");
  expect(screen.queryByText("marquee will not try these.")).toBeNull();
  fireEvent.click(fold);
  expect(fold.querySelector(".fold-chev")?.getAttribute("data-open")).toBe("true");

  expect(screen.getByText("marquee will not try these.")).toBeTruthy();
  const rows = within(screen.getByRole("list", { name: "Dropped releases" })).getAllByRole("listitem");
  expect(rows).toHaveLength(3);
  expect(rows.every((r) => r.getAttribute("data-dropped") === "true")).toBe(true);
  expect(within(rows[0]).getByText("not cached on the debrid service")).toBeTruthy();
  expect(within(rows[0]).queryByRole("button")).toBeNull();
  expect(within(rows[1]).getByText("a .exe file, not a video")).toBeTruthy();
  expect(within(rows[1]).queryByRole("button")).toBeNull();
  expect(within(rows[2]).getByText("failed before (stalled); blocked for 30 days")).toBeTruthy();
  expect(within(rows[2]).getByRole("button", { name: "Pick anyway Dune.2021.1080p.WEB" }).textContent).toBe("Pick anyway");
  fireEvent.click(fold);
  expect(screen.queryByRole("list", { name: "Dropped releases" })).toBeNull();
});

const tagged: Releases = {
  candidates: [{ ...release("rel-1", "Dune.2021.2160p.WEB-DL", "WEBDL-2160p", 34_359_738_368, "Comet"), tags: ["dd+-atmos", "hdr10+", "hevc"] }, release("rel-2", "Dune.2021.1080p.WEB-DL", "WEBDL-1080p", 6_442_450_944, "Comet")],
  dropped: [
    { release: { ...release("rel-3", "Dune.2021.2160p.Remux", "Remux-2160p", 60_000_000_000, "Comet"), tags: ["truehd", "dv-p7"] }, reason: "profile_rejected", message: "rejected by profile: TrueHD audio, DV Profile 7" },
    { release: release("rel-4", "Dune.2021.1080p.exe", "WEBDL-1080p", 4_194_304_000, "Torrentio"), reason: "not_video", message: "a .exe file, not a video" },
  ],
};

test("format tags sit on the row in words, on candidates and dropped releases alike, and a row without tags has none", async () => {
  await openMovie(tagged);
  const rows = within(await table()).getAllByRole("listitem");

  expect([...rows[0].querySelectorAll(".rl-tag")].map((t) => t.textContent)).toEqual(["DD+ Atmos", "HDR10+", "HEVC"]);
  expect(rows[1].querySelector(".rl-tag")).toBeNull();

  fireEvent.click(screen.getByRole("button", { name: "Show 2 dropped releases" }));
  const dropped = within(screen.getByRole("list", { name: "Dropped releases" })).getAllByRole("listitem");
  expect([...dropped[0].querySelectorAll(".rl-tag")].map((t) => t.textContent)).toEqual(["TrueHD", "DV P7"]);
});

test("a release the profile rejected says why in words and can be picked anyway, which grabs it", async () => {
  const { writes } = await openMovie(tagged, (_url, init) => (init.method === "POST" ? json(queued({ releaseName: "Dune.2021.2160p.Remux" }), 202) : undefined));
  await table();
  fireEvent.click(screen.getByRole("button", { name: "Show 2 dropped releases" }));
  const rows = within(screen.getByRole("list", { name: "Dropped releases" })).getAllByRole("listitem");

  expect(within(rows[0]).getByText("rejected by profile: TrueHD audio, DV Profile 7")).toBeTruthy();
  expect(within(rows[1]).queryByRole("button")).toBeNull();
  fireEvent.click(within(rows[0]).getByRole("button", { name: "Pick anyway Dune.2021.2160p.Remux" }));

  expect((await screen.findByRole("status")).textContent).toBe("Queued Dune.2021.2160p.Remux.");
  expect(writes).toEqual([{ method: "POST", path: "/api/titles/movie-1/releases/rel-3/grab", body: undefined }]);
  expect(within(rows[0]).getByText("Queued")).toBeTruthy();
});

test("picking a release grabs it, confirms with a toast and refreshes the title", async () => {
  const { writes, calls } = await openMovie(rels, (url, init) => (init.method === "POST" && url.pathname === "/api/titles/movie-1/releases/rel-1/grab" ? json(queued(), 202) : undefined));
  await table();
  const before = calls.filter((u) => u.pathname === "/api/titles/movie-1").length;

  fireEvent.click(screen.getByRole("button", { name: "Pick Dune.2021.2160p.WEB-DL" }));

  expect((await screen.findByRole("status")).textContent).toBe("Queued Dune.2021.2160p.WEB-DL.");
  expect(writes).toEqual([{ method: "POST", path: "/api/titles/movie-1/releases/rel-1/grab", body: undefined }]);
  expect(screen.getByText("Queued", { selector: ".rl-queued" })).toBeTruthy();
  await waitFor(() => expect(calls.filter((u) => u.pathname === "/api/titles/movie-1").length).toBe(before + 1));
});

test("a blocklisted release picked by hand is grabbed", async () => {
  const { writes } = await openMovie(rels, (_url, init) => (init.method === "POST" ? json(queued({ releaseName: "Dune.2021.1080p.WEB" }), 202) : undefined));
  await table();
  fireEvent.click(screen.getByRole("button", { name: "Show 3 dropped releases" }));

  fireEvent.click(screen.getByRole("button", { name: "Pick anyway Dune.2021.1080p.WEB" }));

  await waitFor(() => expect(writes.map((w) => w.path)).toEqual(["/api/titles/movie-1/releases/rel-8/grab"]));
  expect((await screen.findByRole("status")).textContent).toMatch(/Dune/);
});

test("a double click on a release grabs it once", async () => {
  let release!: (r: Response) => void;
  const { writes } = await openMovie(rels, (_url, init) => (init.method === "POST" ? new Promise<Response>((res) => (release = res)) : undefined));
  const pick = await screen.findByRole("button", { name: "Pick Dune.2021.2160p.WEB-DL" });

  fireEvent.click(pick);
  fireEvent.click(pick);
  await waitFor(() => expect(release).toBeDefined());
  release(json(queued(), 202));

  await screen.findByRole("status");
  expect(writes).toHaveLength(1);
});

test("a refused grab shows the API's reason, keeps the list and can be tried again", async () => {
  let refuse = true;
  const { writes } = await openMovie(rels, (_url, init) => (init.method === "POST" ? (refuse ? json({ error: "this release cannot be grabbed: a .exe file, not a video" }, 409) : json(queued(), 202)) : undefined));
  fireEvent.click(await screen.findByRole("button", { name: "Pick Dune.2021.2160p.WEB-DL" }));

  expect((await screen.findByRole("alert")).textContent).toContain("cannot be grabbed");
  expect(screen.getByRole("list", { name: "Releases for Dune" })).toBeTruthy();
  expect(screen.queryByRole("status")).toBeNull();
  refuse = false;
  fireEvent.click(screen.getByRole("button", { name: "Pick Dune.2021.2160p.WEB-DL" }));
  await screen.findByRole("status");
  expect(writes).toHaveLength(2);
});

test("when the source is down it says so and Try again asks again", async () => {
  let up = false;
  await openMovie(() => Promise.resolve(up ? json(rels) : json({ error: "AIOStreams did not answer" }, 502)));

  expect((await screen.findByRole("alert")).textContent).toContain("AIOStreams did not answer");
  up = true;
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));

  expect(await table()).toBeTruthy();
});

test("no candidates says so and offers to search again", async () => {
  let again = false;
  await openMovie(() => Promise.resolve(again ? json(rels) : json({ candidates: [], dropped: [] })));

  expect(await screen.findByText(/AIOStreams has nothing for/)).toBeTruthy();
  expect(screen.queryByText(/was dropped/)).toBeNull();
  expect(screen.queryByRole("list", { name: "Releases for Dune" })).toBeNull();
  again = true;
  fireEvent.click(screen.getByRole("button", { name: "Search again" }));
  expect(await table()).toBeTruthy();
});

test("when every release was dropped it says that, and the dropped fold is still there", async () => {
  await openMovie({ candidates: [], dropped: [rels.dropped[2]] });

  expect(await screen.findByText(/was dropped/)).toBeTruthy();
  expect(screen.queryByRole("list", { name: "Releases for Dune" })).toBeNull();
  expect(screen.getByRole("button", { name: "Show 1 dropped releases" })).toBeTruthy();
});

test("a series lists the releases of the episode that was opened, asks for that episode, and grabs it", async () => {
  const { calls, writes } = setupApp(
    titleApi(
      house(),
      serve(rels, (_url, init) => (init.method === "POST" ? json(queued({ titleId: "series-2", titleName: "House", episode: "s04e02" }), 202) : undefined)),
      aboutOf(),
    ),
    "#/title/series-2",
  );
  await screen.findByRole("heading", { name: "House", level: 1 });
  const row = screen.getByRole("button", { name: /S04E02 Big Baby/ });

  fireEvent.click(row);

  expect(row.getAttribute("aria-expanded")).toBe("true");
  expect(await screen.findByText("in marquee’s order")).toBeTruthy();
  expect(calls.filter((u) => u.pathname.endsWith("/releases")).map((u) => u.search)).toEqual(["?episode=s04e02"]);
  fireEvent.click(screen.getByRole("button", { name: "Pick Dune.2021.2160p.WEB-DL" }));
  await screen.findByRole("status");
  expect(writes).toEqual([{ method: "POST", path: "/api/titles/series-2/releases/rel-1/grab", body: { episode: "s04e02" } }]);
});

test("leaving an episode closes its list, and a late answer for it never lands in the next episode's list", async () => {
  let lateE02!: (r: Response) => void;
  const other: Releases = { candidates: [release("rel-9", "House.S04E03.1080p", "WEBDL-1080p", 1_073_741_824, "Comet")], dropped: [] };
  setupApp(
    titleApi(
      house(),
      (url) => {
        if (!url.pathname.endsWith("/releases")) return undefined;
        if (url.searchParams.get("episode") === "s04e02") return new Promise<Response>((res) => (lateE02 = res)) as unknown as Response;
        return json(other);
      },
      aboutOf(),
    ),
    "#/title/series-2",
  );
  await screen.findByRole("heading", { name: "House", level: 1 });
  fireEvent.click(screen.getByRole("button", { name: /S04E02 Big Baby/ }));
  await waitFor(() => expect(lateE02).toBeDefined());

  fireEvent.click(screen.getByRole("button", { name: /S04E03 You Don't Want to Know/ }));
  await screen.findByText("House.S04E03.1080p");
  lateE02(json(rels));
  await new Promise((r) => setTimeout(r, 50));

  expect(screen.getByText("House.S04E03.1080p")).toBeTruthy();
  expect(screen.queryByText("Dune.2021.2160p.WEB-DL")).toBeNull();
  expect(screen.getByRole("button", { name: /S04E02 Big Baby/ }).getAttribute("aria-expanded")).toBe("false");
});

test("closing an episode collapses it in place and a second click opens it again", async () => {
  setupApp(titleApi(house(), serve(rels), aboutOf()), "#/title/series-2");
  await screen.findByRole("heading", { name: "House", level: 1 });
  const row = screen.getByRole("button", { name: /S04E02 Big Baby/ });

  fireEvent.click(row);
  await screen.findByRole("list", { name: /^Releases for/ });
  fireEvent.click(row);
  await waitFor(() => expect(row.getAttribute("aria-expanded")).toBe("false"));
  expect(screen.queryByRole("list", { name: /^Releases for/ })).toBeNull();
  fireEvent.click(row);
  expect(await screen.findByRole("list", { name: /^Releases for/ })).toBeTruthy();
});

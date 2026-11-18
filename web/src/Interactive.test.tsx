// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { Download, Releases, Title, TitleSummary } from "./api/types";
import { json, setupApp, type Handler } from "./testkit";

const summary = (over: Partial<TitleSummary> & Pick<TitleSummary, "id" | "kind" | "title" | "tmdbId">): TitleSummary => ({
  year: 2020,
  overview: "",
  posterPath: "",
  backdropPath: "",
  inLibrary: false,
  ...over,
});

const dune = summary({ id: "movie-1", kind: "movie", tmdbId: 1, title: "Dune", year: 2021 });
const severance = summary({ id: "series-2", kind: "series", tmdbId: 2, title: "Severance", year: 2022 });
const house = summary({ id: "series-3", kind: "series", tmdbId: 3, title: "House", year: 2004, inLibrary: true, monitored: true, badge: { state: "missing", label: "Missing" } });

const release = (id: string, name: string, quality: string, sizeBytes: number, source: string, cached = true) => ({ id, name, quality, sizeBytes, source, cached });

const rels: Releases = {
  candidates: [
    release("rel-1", "Dune.2021.1080p.BluRay.x264", "Bluray-1080p", 12_884_901_888, "Torrentio"),
    release("rel-2", "Dune.2021.2160p.WEB-DL", "WEBDL-2160p", 34_359_738_368, "Comet"),
  ],
  dropped: [
    { release: release("rel-3", "Dune.2021.720p.HDTV", "HDTV-720p", 3_221_225_472, "Comet", false), reason: "not_cached", message: "not cached on the debrid service" },
    { release: release("rel-4", "Dune.2021.1080p.exe", "WEBDL-1080p", 4_194_304_000, "Torrentio"), reason: "not_video", message: "a .exe file, not a video" },
    { release: release("rel-5", "Dune.2021.1080p.WEB-DL", "WEBDL-1080p", 1_610_612_736, "Comet"), reason: "blocklisted", message: "failed before (stalled); blocked for 30 days" },
  ],
};

const queued = (over: Partial<Download> = {}): Download => ({ id: "dl-1", titleId: "movie-1", titleName: "Dune", releaseName: "Dune.2021.1080p.BluRay.x264", state: "queued", percent: 0, bytesDone: 0, bytesTotal: 12_884_901_888, stalled: false, ...over });

const duneAdded: Title = { ...dune, inLibrary: true, monitored: true, badge: { state: "searching", label: "Searching" }, seasons: [], historySummary: "Nothing tried yet." };

const episode = (n: number, state: "downloaded" | "missing" | "unaired", name = `Episode ${n}`) => ({ id: `s01e0${n}`, number: n, name, airDate: "2022-02-18", monitored: true, state });
const severanceOut = (inLibrary = false): Title => ({
  ...severance,
  inLibrary,
  seasons: [{ number: 1, episodes: [episode(1, "downloaded", "Good News About Hell"), episode(2, "missing", "Half Loop"), episode(3, "missing", "In Perpetuity"), episode(4, "unaired", "Next Week")] }],
  historySummary: "Nothing tried yet.",
});

beforeEach(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.location.hash = "";
});

function serve(extra: Handler): Handler {
  return (url, init) => {
    if (url.pathname === "/api/wall") return json({ items: [{ source: "trending", title: dune }, { source: "trending", title: severance }, { source: "library", title: house }] });
    return extra(url, init);
  };
}

const openTile = async (name: RegExp) => fireEvent.click((await screen.findAllByRole("button", { name }))[0]);
const dialog = () => within(screen.getByRole("dialog"));

async function openSearch(handler: Handler, tile = /^Dune \(2021\)/) {
  const world = setupApp(serve(handler), "");
  await openTile(tile);
  fireEvent.click(dialog().getByRole("button", { name: "Interactive search" }));
  return world;
}

test("Interactive search lists the ranked releases with quality, size, source and cached, and adds nothing", async () => {
  const { calls, writes } = await openSearch((url) => (url.pathname === "/api/titles/movie-1/releases" ? json(rels) : json({ error: "nope" }, 404)));

  const list = await screen.findByRole("list", { name: "Releases" });
  const rows = within(list).getAllByRole("listitem");
  expect(rows).toHaveLength(2);
  expect(within(rows[0]).getByText("Dune.2021.1080p.BluRay.x264")).toBeTruthy();
  expect(within(rows[0]).getByText("Bluray-1080p")).toBeTruthy();
  expect(within(rows[0]).getByText("12 GB")).toBeTruthy();
  expect(within(rows[0]).getByText("Torrentio")).toBeTruthy();
  expect(within(rows[0]).getByText("Cached")).toBeTruthy();
  expect(within(rows[1]).getByText("WEBDL-2160p")).toBeTruthy();
  expect(within(rows[1]).getByText("32 GB")).toBeTruthy();
  expect(calls.find((u) => u.pathname.endsWith("/releases"))!.search).toBe("");
  expect(writes).toEqual([]);
});

test("releases that were dropped are listed with the reason in words, and only a blocklisted one can be picked", async () => {
  await openSearch((url) => (url.pathname === "/api/titles/movie-1/releases" ? json(rels) : json({ error: "nope" }, 404)));

  const dropped = await screen.findByRole("list", { name: "Dropped" });
  const rows = within(dropped).getAllByRole("listitem");
  expect(rows).toHaveLength(3);
  expect(within(rows[0]).getByText("not cached on the debrid service")).toBeTruthy();
  expect(within(rows[0]).queryByRole("button")).toBeNull();
  expect(within(rows[1]).getByText("a .exe file, not a video")).toBeTruthy();
  expect(within(rows[1]).queryByRole("button")).toBeNull();
  expect(within(rows[2]).getByText("failed before (stalled); blocked for 30 days")).toBeTruthy();
  expect(within(rows[2]).getByRole("button", { name: "Pick Dune.2021.1080p.WEB-DL anyway" })).toBeTruthy();
});

test("picking a release grabs it, confirms with a toast and the added title now reads Search now", async () => {
  const { writes } = await openSearch((url, init) => {
    if (url.pathname === "/api/titles/movie-1/releases") return json(rels);
    if (init.method === "POST" && url.pathname === "/api/titles/movie-1/releases/rel-1/grab") return json(queued(), 202);
    if (url.pathname === "/api/titles/movie-1") return json(duneAdded);
    return json({ error: "nope" }, 404);
  });

  fireEvent.click(await screen.findByRole("button", { name: "Pick Dune.2021.1080p.BluRay.x264" }));

  expect((await screen.findByRole("status")).textContent).toMatch(/Dune/);
  expect(writes).toEqual([{ method: "POST", path: "/api/titles/movie-1/releases/rel-1/grab", body: undefined }]);
  fireEvent.click(dialog().getByRole("button", { name: "Back" }));
  expect(dialog().getByRole("button", { name: "Search now" })).toBeTruthy();
});

test("a blocklisted release picked by hand is grabbed", async () => {
  const { writes } = await openSearch((url, init) => {
    if (url.pathname === "/api/titles/movie-1/releases") return json(rels);
    if (init.method === "POST") return json(queued({ releaseName: "Dune.2021.1080p.WEB-DL" }), 202);
    return json({ error: "nope" }, 404);
  });

  fireEvent.click(await screen.findByRole("button", { name: "Pick Dune.2021.1080p.WEB-DL anyway" }));

  await waitFor(() => expect(writes.map((w) => w.path)).toEqual(["/api/titles/movie-1/releases/rel-5/grab"]));
  expect((await screen.findByRole("status")).textContent).toMatch(/Dune/);
});

test("a double click on a release grabs it once", async () => {
  let release!: (r: Response) => void;
  const { writes } = await openSearch((url, init) => {
    if (url.pathname === "/api/titles/movie-1/releases") return json(rels);
    if (init.method === "POST") return new Promise<Response>((res) => (release = res));
    return json({ error: "nope" }, 404);
  });
  const pick = await screen.findByRole("button", { name: "Pick Dune.2021.1080p.BluRay.x264" });

  fireEvent.click(pick);
  fireEvent.click(pick);
  fireEvent.click(await screen.findByRole("button", { name: "Pick Dune.2021.2160p.WEB-DL" }));
  await waitFor(() => expect(release).toBeDefined());
  release(json(queued(), 202));

  await screen.findByRole("status");
  expect(writes).toHaveLength(1);
});

test("a refused grab shows the API's reason, keeps the list and can be tried again", async () => {
  await openSearch((url, init) => {
    if (url.pathname === "/api/titles/movie-1/releases") return json(rels);
    if (init.method === "POST") return json({ error: "this release cannot be grabbed: a .exe file, not a video" }, 409);
    return json({ error: "nope" }, 404);
  });

  fireEvent.click(await screen.findByRole("button", { name: "Pick Dune.2021.1080p.BluRay.x264" }));

  expect((await dialog().findByRole("alert")).textContent).toContain("cannot be grabbed");
  expect(dialog().getByRole("list", { name: "Releases" })).toBeTruthy();
  expect(screen.queryByRole("status")).toBeNull();
});

test("when the source is down the popup says so and Try again asks again", async () => {
  let up = false;
  await openSearch((url) => {
    if (url.pathname !== "/api/titles/movie-1/releases") return json({ error: "nope" }, 404);
    return up ? json(rels) : json({ error: "AIOStreams did not answer" }, 502);
  });

  expect((await dialog().findByRole("alert")).textContent).toContain("AIOStreams did not answer");
  up = true;
  fireEvent.click(dialog().getByRole("button", { name: "Try again" }));

  expect(await screen.findByRole("list", { name: "Releases" })).toBeTruthy();
});

test("no candidates says so instead of showing an empty list", async () => {
  await openSearch((url) => (url.pathname === "/api/titles/movie-1/releases" ? json({ candidates: [], dropped: [rels.dropped[1]] }) : json({ error: "nope" }, 404)));

  expect(await dialog().findByText("Nothing to pick: every release was dropped.")).toBeTruthy();
  expect(screen.queryByRole("list", { name: "Releases" })).toBeNull();
  expect(dialog().getByRole("list", { name: "Dropped" })).toBeTruthy();
});

test("Back returns to the popup's own actions", async () => {
  await openSearch((url) => (url.pathname === "/api/titles/movie-1/releases" ? json(rels) : json({ error: "nope" }, 404)));
  await screen.findByRole("list", { name: "Releases" });

  fireEvent.click(dialog().getByRole("button", { name: "Back" }));

  expect(dialog().getByRole("button", { name: "Add & monitor" })).toBeTruthy();
  expect(screen.queryByRole("list", { name: "Releases" })).toBeNull();
});

test("a series asks which episode, starting at the first one it is missing, and lists that episode's releases", async () => {
  const { calls } = await openSearch((url) => {
    if (url.pathname === "/api/titles/series-2") return json(severanceOut());
    if (url.pathname === "/api/titles/series-2/releases") return json(rels);
    return json({ error: "nope" }, 404);
  }, /^Severance \(2022\)/);

  const pick = (await dialog().findByRole("combobox", { name: "Episode" })) as HTMLSelectElement;
  await screen.findByRole("list", { name: "Releases" });
  expect(pick.value).toBe("s01e02");
  expect([...pick.options].map((o) => o.textContent)).toEqual(["S01E01 · Good News About Hell", "S01E02 · Half Loop", "S01E03 · In Perpetuity"]);
  expect(calls.filter((u) => u.pathname.endsWith("/releases")).map((u) => u.search)).toEqual(["?episode=s01e02"]);

  fireEvent.change(pick, { target: { value: "s01e03" } });

  await waitFor(() => expect(calls.filter((u) => u.pathname.endsWith("/releases")).map((u) => u.search)).toEqual(["?episode=s01e02", "?episode=s01e03"]));
});

test("picking a release for an episode sends that episode and the series joins the library", async () => {
  let added = false;
  const { writes } = await openSearch((url, init) => {
    if (url.pathname === "/api/titles/series-2") return json(severanceOut(added));
    if (url.pathname === "/api/titles/series-2/releases") return json(rels);
    if (init.method === "POST") {
      added = true;
      return json(queued({ titleId: "series-2", titleName: "Severance", episode: "s01e02" }), 202);
    }
    return json({ error: "nope" }, 404);
  }, /^Severance \(2022\)/);

  fireEvent.click(await screen.findByRole("button", { name: "Pick Dune.2021.1080p.BluRay.x264" }));

  await screen.findByRole("status");
  expect(writes).toEqual([{ method: "POST", path: "/api/titles/series-2/releases/rel-1/grab", body: { episode: "s01e02" } }]);
  fireEvent.click(dialog().getByRole("button", { name: "Back" }));
  expect(dialog().getByRole("button", { name: "Search now" })).toBeTruthy();
});

test("a series the library does not have yet says to add it first", async () => {
  await openSearch((url) => (url.pathname === "/api/titles/series-2" ? json({ error: "no such title in the library" }, 404) : json({ error: "nope" }, 404)), /^Severance \(2022\)/);

  expect((await dialog().findByText(/Add & monitor this series first/)).textContent).toBeTruthy();
  expect(dialog().queryByRole("combobox")).toBeNull();
});

test("a series with nothing aired yet has no episode to search", async () => {
  await openSearch(
    (url) => (url.pathname === "/api/titles/series-2" ? json({ ...severanceOut(), seasons: [{ number: 1, episodes: [episode(1, "unaired")] }] }) : json({ error: "nope" }, 404)),
    /^Severance \(2022\)/,
  );

  expect(await dialog().findByText("No episode has aired yet, so there is nothing to search.")).toBeTruthy();
});

test("a library series opens on its first missing episode too", async () => {
  const { calls } = await openSearch((url) => {
    if (url.pathname === "/api/titles/series-3") return json({ ...severanceOut(true), ...house });
    if (url.pathname === "/api/titles/series-3/releases") return json(rels);
    return json({ error: "nope" }, 404);
  }, /^House \(2004\)/);

  await screen.findByRole("list", { name: "Releases" });
  expect(calls.find((u) => u.pathname.endsWith("/releases"))!.search).toBe("?episode=s01e02");
});

test("Esc closes the popup, even from the release list", async () => {
  await openSearch((url) => (url.pathname === "/api/titles/movie-1/releases" ? json(rels) : json({ error: "nope" }, 404)));
  await screen.findByRole("list", { name: "Releases" });

  fireEvent.keyDown(window, { key: "Escape" });

  expect(screen.queryByRole("dialog")).toBeNull();
});

test("Try again after a failed episode keeps the chosen episode and asks for its releases again", async () => {
  let e03Up = false;
  const { calls } = await openSearch((url) => {
    if (url.pathname === "/api/titles/series-2") return json(severanceOut());
    if (url.pathname === "/api/titles/series-2/releases") {
      if (url.searchParams.get("episode") === "s01e03") return e03Up ? json(rels) : json({ error: "AIOStreams did not answer" }, 502);
      return json(rels);
    }
    return json({ error: "nope" }, 404);
  }, /^Severance \(2022\)/);
  const pick = (await dialog().findByRole("combobox", { name: "Episode" })) as HTMLSelectElement;
  await screen.findByRole("list", { name: "Releases" });
  fireEvent.change(pick, { target: { value: "s01e03" } });
  expect((await dialog().findByRole("alert")).textContent).toContain("AIOStreams did not answer");
  const before = calls.length;
  e03Up = true;

  fireEvent.click(dialog().getByRole("button", { name: "Try again" }));

  expect(await screen.findByRole("list", { name: "Releases" })).toBeTruthy();
  expect((dialog().getByRole("combobox", { name: "Episode" }) as HTMLSelectElement).value).toBe("s01e03");
  const after = calls.slice(before).filter((u) => u.pathname.endsWith("/releases")).map((u) => u.search);
  expect(after).toContain("?episode=s01e03");
  expect(after).not.toContain("?episode=s01e02");
});

test("a late answer for an episode the user left does not replace the current episode's releases", async () => {
  let lateE02!: (r: Response) => void;
  const other: Releases = { candidates: [release("rel-9", "Severance.S01E03.1080p", "WEBDL-1080p", 1_073_741_824, "Comet")], dropped: [] };
  await openSearch((url) => {
    if (url.pathname === "/api/titles/series-2") return json(severanceOut());
    if (url.pathname === "/api/titles/series-2/releases") {
      if (url.searchParams.get("episode") === "s01e02") return new Promise<Response>((res) => (lateE02 = res));
      return json(other);
    }
    return json({ error: "nope" }, 404);
  }, /^Severance \(2022\)/);
  const pick = (await dialog().findByRole("combobox", { name: "Episode" })) as HTMLSelectElement;
  await waitFor(() => expect(lateE02).toBeDefined());
  fireEvent.change(pick, { target: { value: "s01e03" } });
  await screen.findByText("Severance.S01E03.1080p");

  lateE02(json(rels));
  await new Promise((r) => setTimeout(r, 50));

  expect(screen.queryByText("Asking AIOStreams…")).toBeNull();
  expect(screen.getByText("Severance.S01E03.1080p")).toBeTruthy();
});

test("an empty answer says nothing was found, not that every release was dropped", async () => {
  await openSearch((url) => (url.pathname === "/api/titles/movie-1/releases" ? json({ candidates: [], dropped: [] }) : json({ error: "nope" }, 404)));

  await waitFor(() => expect(screen.queryByText("Asking AIOStreams…")).toBeNull());
  expect(dialog().queryByText(/every release was dropped/)).toBeNull();
  expect(dialog().getByText(/Nothing found/)).toBeTruthy();
});

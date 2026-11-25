// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { Health, LibraryRescan, Title } from "./api/types";
import { aboutOf, dune, house, queued, rels } from "./fixtures";
import { json, setupApp, titleApi, type Handler } from "./testkit";

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

const ok = (over: Partial<Health> = {}): Health => ({
  status: "ok",
  dryRun: false,
  checks: [{ id: "tmdb", name: "TMDB", status: "ok", message: "Key accepted." }],
  libraryTotals: { movies: 1, episodes: 5 },
  libraryProblems: [],
  ...over,
});

const scan = (over: Partial<LibraryRescan> = {}): LibraryRescan => ({ titlesScanned: 22, adopted: 0, foundOnDisk: 0, missingOnDisk: 0, problems: 0, ...over });

const downloadedMovie = () => dune({ badge: { state: "downloaded", label: "Downloaded" } });

// S04E01 and S04E02 live in one file.
const sharing = (): Title => {
  const t = house();
  return {
    ...t,
    seasons: t.seasons.map((s) =>
      s.number !== 4
        ? s
        : {
            ...s,
            episodes: s.episodes.map((e) =>
              e.id === "s04e01" || e.id === "s04e02" ? { ...e, state: "downloaded" as const, nextSearchAt: undefined, sharedEpisodes: ["s04e01", "s04e02"] } : e,
            ),
          },
    ),
  };
};

const open = async (title: Title, extra?: Parameters<typeof titleApi>[1]) => {
  const world = setupApp(titleApi(title, extra, aboutOf()), `#/title/${title.id}`);
  await screen.findByRole("heading", { name: title.title, level: 1 });
  return world;
};

const releasesAnswer: Handler = (url) => (url.pathname.endsWith("/releases") ? json(rels) : undefined) as never;

// --- Rescan library (Health) ---

function health(rescan: () => Response | Promise<Response>) {
  let checks = 0;
  const world = setupApp(async (url, init) => {
    if (url.pathname === "/api/health") {
      checks++;
      return json(ok());
    }
    if (url.pathname === "/api/library/rescan" && init.method === "POST") return rescan();
    if (url.pathname === "/api/wall") return json({ items: [] });
    return json({ error: "nope" }, 404);
  }, "#/health");
  return { ...world, checks: () => checks };
}

test("Rescan library on Health runs the scan, says what it found in one toast and checks health again", async () => {
  const { writes, checks } = health(() => json(scan({ adopted: 1, foundOnDisk: 2, missingOnDisk: 1 })));
  fireEvent.click(await screen.findByRole("button", { name: "Rescan library" }));

  expect(await screen.findByText("Rescanned 22 titles: 1 adopted, 2 found on disk, 1 gone from disk.")).toBeTruthy();
  expect(writes).toEqual([{ method: "POST", path: "/api/library/rescan", body: undefined }]);
  await waitFor(() => expect(checks()).toBe(2));
});

test("a library rescan that adopted titles fetches the wall again", async () => {
  const { calls } = health(() => json(scan({ adopted: 1 })));
  fireEvent.click(await screen.findByRole("button", { name: "Rescan library" }));
  await screen.findByText("Rescanned 22 titles: 1 adopted.");

  await waitFor(() => expect(calls.filter((u) => u.pathname === "/api/wall").length).toBe(2));
});

test("a rescan that changed nothing says so, and a double click scans once", async () => {
  const { writes } = health(() => json(scan({ titlesScanned: 1 })));
  const button = await screen.findByRole("button", { name: "Rescan library" });
  fireEvent.click(button);
  fireEvent.click(button);

  expect(await screen.findByText("Rescanned 1 title. Nothing changed.")).toBeTruthy();
  expect(writes).toHaveLength(1);
});

test("a failed library rescan says why in an error toast and the button comes back", async () => {
  health(() => json({ error: "the movies folder cannot be read" }, 503));
  fireEvent.click(await screen.findByRole("button", { name: "Rescan library" }));

  expect((await screen.findByRole("alert")).textContent).toContain("the movies folder cannot be read");
  expect(screen.getByRole("button", { name: "Rescan library" }).getAttribute("aria-disabled")).not.toBe("true");
});

// --- Rescan (TitleScreen) ---

test("Rescan on a title runs its own rescan, refreshes the screen and names what changed", async () => {
  const after = sharing();
  const { writes } = await open(house(), (url, init) => (url.pathname === "/api/titles/series-2/rescan" && init.method === "POST" ? json(after) : undefined));

  fireEvent.click(screen.getByRole("button", { name: "Rescan" }));

  expect((await screen.findByRole("status")).textContent).toBe("Rescanned “House”: 1 file found on disk.");
  expect(writes).toEqual([{ method: "POST", path: "/api/titles/series-2/rescan", body: undefined }]);
  expect(within(screen.getAllByRole("listitem").find((li) => li.textContent?.includes("Big Baby"))!).getByText("Downloaded")).toBeTruthy();
});

test("a movie rescan that finds its file gone says so; one with no change says nothing changed", async () => {
  const gone = dune();
  await open(downloadedMovie(), (url, init) => (url.pathname === "/api/titles/movie-1/rescan" && init.method === "POST" ? json(gone) : undefined));

  fireEvent.click(screen.getByRole("button", { name: "Rescan" }));

  expect((await screen.findByRole("status")).textContent).toBe("Rescanned “Dune”: its file is gone from disk.");
});

test("a failed title rescan is said in words", async () => {
  await open(house(), (url, init) => (url.pathname.endsWith("/rescan") && init.method === "POST" ? json({ error: "the library folder cannot be read" }, 503) : undefined));
  fireEvent.click(screen.getByRole("button", { name: "Rescan" }));
  expect((await screen.findByRole("alert")).textContent).toContain("the library folder cannot be read");
});

test("a title that is not in the library has no Rescan", async () => {
  await open(house({ inLibrary: false, monitored: undefined, seasons: [] }));
  expect(screen.queryByRole("button", { name: "Rescan" })).toBeNull();
});

// --- Also delete files (RemoveBlock) ---

const del = (url: URL, init: Request) => (init.method === "DELETE" && url.pathname === "/api/titles/series-2" ? new Response(null, { status: 204 }) : undefined);
const group = () => screen.getByRole("group", { name: "Remove from marquee" });

test("the remove question offers 'Also delete files from disk', unchecked, and the warning follows it", async () => {
  const { writes } = await open(house(), del);
  expect(within(group()).queryByRole("checkbox")).toBeNull();
  fireEvent.click(within(group()).getByRole("button", { name: "Remove…" }));

  const box = within(group()).getByRole("checkbox", { name: "Also delete files from disk" }) as HTMLInputElement;
  expect(box.checked).toBe(false);
  expect(within(group()).getByText("marquee stops searching for it. The files on disk stay.")).toBeTruthy();

  fireEvent.click(box);
  expect(box.checked).toBe(true);
  expect(within(group()).getByText(/deletes every file it has on disk/)).toBeTruthy();
  expect(within(group()).queryByText("marquee stops searching for it. The files on disk stay.")).toBeNull();
  expect(within(group()).getByRole("button", { name: "Yes, remove “House” and delete its files" })).toBeTruthy();
  expect(writes).toEqual([]);
});

test("Keep it forgets a ticked box, so the next question starts unchecked", async () => {
  await open(house(), del);
  fireEvent.click(within(group()).getByRole("button", { name: "Remove…" }));
  fireEvent.click(within(group()).getByRole("checkbox", { name: "Also delete files from disk" }));
  fireEvent.click(within(group()).getByRole("button", { name: "Keep it" }));
  fireEvent.click(within(group()).getByRole("button", { name: "Remove…" }));

  expect((within(group()).getByRole("checkbox", { name: "Also delete files from disk" }) as HTMLInputElement).checked).toBe(false);
});

test("removing with the box ticked sends deleteFiles=true and the toast says the files went", async () => {
  const { writes, calls } = await open(house(), del);
  fireEvent.click(within(group()).getByRole("button", { name: "Remove…" }));
  fireEvent.click(within(group()).getByRole("checkbox", { name: "Also delete files from disk" }));
  fireEvent.click(within(group()).getByRole("button", { name: "Yes, remove “House” and delete its files" }));

  await waitFor(() => expect(window.location.hash).toBe("#/grid"));
  expect(writes).toEqual([{ method: "DELETE", path: "/api/titles/series-2", body: undefined }]);
  expect(calls.find((u) => u.pathname === "/api/titles/series-2" && u.searchParams.has("deleteFiles"))?.searchParams.get("deleteFiles")).toBe("true");
  expect((await screen.findByRole("status")).textContent).toBe("Removed “House” from marquee and deleted its files from disk.");
});

test("removing with the box unticked never mentions deleteFiles", async () => {
  const { calls } = await open(house(), del);
  fireEvent.click(within(group()).getByRole("button", { name: "Remove…" }));
  fireEvent.click(within(group()).getByRole("button", { name: "Yes, remove “House”" }));

  await waitFor(() => expect(window.location.hash).toBe("#/grid"));
  expect(calls.some((u) => u.searchParams.has("deleteFiles"))).toBe(false);
});

test("a refused file deletion (dry-run) keeps the title and says why", async () => {
  await open(house(), (_url, init) => (init.method === "DELETE" ? json({ error: "dry-run is on, so marquee will not delete files from disk" }, 409) : undefined));
  fireEvent.click(within(group()).getByRole("button", { name: "Remove…" }));
  fireEvent.click(within(group()).getByRole("checkbox", { name: "Also delete files from disk" }));
  fireEvent.click(within(group()).getByRole("button", { name: "Yes, remove “House” and delete its files" }));

  expect((await screen.findByRole("alert")).textContent).toContain("dry-run is on");
  expect(window.location.hash).toBe("#/title/series-2");
});

// --- Wrong file ---

const wrong = (title: Title, path: string) => (url: URL, init: Request) => {
  if (init.method === "POST" && url.pathname === path) {
    const t = title.kind === "movie" ? dune() : house();
    return json(t);
  }
  return releasesAnswer(url, init);
};

test("a downloaded movie has Wrong file; a missing one does not", async () => {
  await open(dune());
  expect(screen.queryByRole("button", { name: "Wrong file" })).toBeNull();
  cleanup();
  await open(downloadedMovie());
  expect(screen.getByRole("button", { name: "Wrong file" })).toBeTruthy();
});

test("Wrong file asks first, says what is deleted, and Cancel or Esc leaves everything", async () => {
  const { writes } = await open(downloadedMovie(), wrong(dune(), "/api/titles/movie-1/wrong-file"));
  fireEvent.click(screen.getByRole("button", { name: "Wrong file" }));

  const ask = screen.getByRole("group", { name: "Wrong file" });
  expect(within(ask).getByText(/deletes “Dune” from disk, with its subtitles/)).toBeTruthy();
  expect(within(ask).queryByText(/also holds/)).toBeNull();
  expect(document.activeElement).toBe(within(ask).getByRole("button", { name: "Cancel" }));
  expect(writes).toEqual([]);

  fireEvent.keyDown(within(ask).getByRole("button", { name: "Cancel" }), { key: "Escape" });
  expect(screen.queryByRole("group", { name: "Wrong file" })).toBeNull();
  expect(window.location.hash).toBe("#/title/movie-1");
  fireEvent.click(screen.getByRole("button", { name: "Wrong file" }));
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(screen.queryByRole("group", { name: "Wrong file" })).toBeNull();
  expect(writes).toEqual([]);
});

test("'Search automatically' on a wrong movie file deletes it and searches for another", async () => {
  const { writes, calls } = await open(downloadedMovie(), wrong(dune(), "/api/titles/movie-1/wrong-file"));
  fireEvent.click(screen.getByRole("button", { name: "Wrong file" }));
  const ask = screen.getByRole("group", { name: "Wrong file" });
  fireEvent.click(within(ask).getByRole("button", { name: "Search automatically" }));

  expect((await screen.findByRole("status")).textContent).toBe("Deleted the file for “Dune”. Searching for another release.");
  expect(writes).toEqual([{ method: "POST", path: "/api/titles/movie-1/wrong-file", body: { reSearch: true } }]);
  expect(screen.queryByRole("button", { name: "Wrong file" })).toBeNull();
  expect(calls.some((u) => u.pathname.endsWith("/releases"))).toBe(false);
});

test("'Pick another release' on a wrong movie file deletes it without a search and opens Interactive Search", async () => {
  const { writes } = await open(downloadedMovie(), wrong(dune(), "/api/titles/movie-1/wrong-file"));
  fireEvent.click(screen.getByRole("button", { name: "Wrong file" }));
  fireEvent.click(within(screen.getByRole("group", { name: "Wrong file" })).getByRole("button", { name: "Pick another release" }));

  expect(await screen.findByRole("list", { name: "Releases for Dune" })).toBeTruthy();
  expect(writes).toEqual([{ method: "POST", path: "/api/titles/movie-1/wrong-file", body: { reSearch: false } }]);
  expect(screen.getByRole("button", { name: "Pick Dune.2021.2160p.WEB-DL" })).toBeTruthy();
});

test("a refused Wrong file (dry-run) says why and keeps the question open to try again or cancel", async () => {
  await open(downloadedMovie(), (url, init) => (init.method === "POST" && url.pathname.endsWith("/wrong-file") ? json({ error: "dry-run is on, so marquee will not delete files from disk" }, 409) : undefined));
  fireEvent.click(screen.getByRole("button", { name: "Wrong file" }));
  fireEvent.click(within(screen.getByRole("group", { name: "Wrong file" })).getByRole("button", { name: "Search automatically" }));

  expect((await screen.findByRole("alert")).textContent).toContain("dry-run is on");
  expect(within(screen.getByRole("group", { name: "Wrong file" })).getByRole("button", { name: "Cancel" })).toBeTruthy();
});

const openEpisode = async (title: Title, extra?: Parameters<typeof titleApi>[1]) => {
  const world = await open(title, extra);
  fireEvent.click(screen.getByRole("button", { name: /^S04E01 Alone, Downloaded/ }));
  return world;
};

test("an episode that is not downloaded has no Wrong file", async () => {
  await open(house(), releasesAnswer);
  fireEvent.click(screen.getByRole("button", { name: /^S04E02 Big Baby, Missing/ }));
  await screen.findByRole("list", { name: /Releases for/ });
  expect(screen.queryByRole("button", { name: "Wrong file" })).toBeNull();
});

test("Wrong file on an episode deletes that episode's file and says so", async () => {
  const { writes } = await openEpisode(house(), wrong(house(), "/api/titles/series-2/episodes/s04e01/wrong-file"));
  fireEvent.click(await screen.findByRole("button", { name: "Wrong file" }));
  const ask = screen.getByRole("group", { name: "Wrong file" });
  expect(within(ask).getByText(/deletes S04E01 Alone from disk, with its subtitles/)).toBeTruthy();
  fireEvent.click(within(ask).getByRole("button", { name: "Search automatically" }));

  expect((await screen.findByRole("status")).textContent).toBe("Deleted the file for S04E01 Alone. Searching for another release.");
  expect(writes).toEqual([{ method: "POST", path: "/api/titles/series-2/episodes/s04e01/wrong-file", body: { reSearch: true } }]);
});

test("a file shared by several episodes warns which others go with it before anything is deleted", async () => {
  const { writes } = await openEpisode(sharing(), wrong(house(), "/api/titles/series-2/episodes/s04e01/wrong-file"));
  fireEvent.click(await screen.findByRole("button", { name: "Wrong file" }));

  const ask = screen.getByRole("group", { name: "Wrong file" });
  expect(within(ask).getByText(/This file also holds S04E02 Big Baby/)).toBeTruthy();
  expect(within(ask).getByText(/go missing together/)).toBeTruthy();
  expect(writes).toEqual([]);
});

test("'Pick another release' on an episode keeps its releases open for choosing", async () => {
  const { writes, calls } = await openEpisode(house(), wrong(house(), "/api/titles/series-2/episodes/s04e01/wrong-file"));
  await screen.findByRole("list", { name: /Releases for/ });
  fireEvent.click(screen.getByRole("button", { name: "Wrong file" }));
  fireEvent.click(within(screen.getByRole("group", { name: "Wrong file" })).getByRole("button", { name: "Pick another release" }));

  await waitFor(() => expect(writes).toEqual([{ method: "POST", path: "/api/titles/series-2/episodes/s04e01/wrong-file", body: { reSearch: false } }]));
  await waitFor(() => expect(calls.filter((u) => u.pathname.endsWith("/releases")).length).toBe(2));
  expect(await screen.findByRole("list", { name: /Releases for/ })).toBeTruthy();
});

// --- Replace file ---

test("a downloaded movie's releases say Replace file and a grab sends replace: true", async () => {
  const { writes } = await open(downloadedMovie(), (url, init) => (init.method === "POST" && url.pathname === "/api/titles/movie-1/releases/rel-1/grab" ? json(queued(), 202) : releasesAnswer(url, init)));
  fireEvent.click(await screen.findByRole("button", { name: "Search releases" }));
  await screen.findByRole("list", { name: "Releases for Dune" });

  expect(screen.queryByRole("button", { name: /^Pick / })).toBeNull();
  const button = screen.getByRole("button", { name: "Replace file Dune.2021.2160p.WEB-DL" });
  expect(button.textContent).toBe("Replace file");
  fireEvent.click(button);

  await waitFor(() => expect(writes).toEqual([{ method: "POST", path: "/api/titles/movie-1/releases/rel-1/grab", body: { replace: true } }]));
  expect((await screen.findByRole("status")).textContent).toBe("Queued Dune.2021.2160p.WEB-DL.");
});

test("a dropped release that can be overridden says Replace anyway when the movie has a file", async () => {
  await open(downloadedMovie(), releasesAnswer);
  fireEvent.click(await screen.findByRole("button", { name: "Search releases" }));
  await screen.findByRole("list", { name: "Releases for Dune" });
  fireEvent.click(screen.getByRole("button", { name: "Show 3 dropped releases" }));

  const button = screen.getByRole("button", { name: "Replace anyway Dune.2021.1080p.WEB" });
  expect(button.textContent).toBe("Replace anyway");
  expect(screen.queryByRole("button", { name: /^Pick anyway/ })).toBeNull();
});

test("a downloaded episode's grab names the episode and replaces", async () => {
  const { writes } = await openEpisode(house(), (url, init) => (init.method === "POST" && url.pathname.endsWith("/releases/rel-1/grab") ? json(queued(), 202) : releasesAnswer(url, init)));
  fireEvent.click(await screen.findByRole("button", { name: "Replace file Dune.2021.2160p.WEB-DL" }));

  await waitFor(() => expect(writes).toEqual([{ method: "POST", path: "/api/titles/series-2/releases/rel-1/grab", body: { episode: "s04e01", replace: true } }]));
});

test("a missing movie still says Pick and does not ask to replace", async () => {
  const { writes } = await open(dune(), (url, init) => (init.method === "POST" ? json(queued(), 202) : releasesAnswer(url, init)));
  fireEvent.click(await screen.findByRole("button", { name: "Search releases" }));
  fireEvent.click(await screen.findByRole("button", { name: "Pick Dune.2021.2160p.WEB-DL" }));

  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0].body).toBeUndefined();
  expect(screen.queryByRole("button", { name: /^Replace/ })).toBeNull();
});

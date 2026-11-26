// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import type { Download, TitleSummary } from "./api/types";
import { FakeEventSource, json, setupApp } from "./testkit";

const GB = 1024 ** 3;

const episode = (n: number, over: Partial<Download> = {}): Download => ({
  id: `dl-${n}`,
  titleId: "series-1",
  titleName: "Friends",
  episode: `s01e${String(n).padStart(2, "0")}`,
  releaseName: `Friends.S01E${String(n).padStart(2, "0")}.1080p`,
  state: "queued",
  percent: 0,
  bytesDone: 0,
  bytesTotal: Math.round(3.6 * GB),
  stalled: false,
  bytesPerSecond: 0,
  ...over,
});

const running = (n: number, percent: number, over: Partial<Download> = {}) =>
  episode(n, { state: "downloading", percent, bytesDone: Math.round((3.6 * GB * percent) / 100), bytesPerSecond: 48 * 1024 ** 2, ...over });

// Friends: two downloading and twenty-two queued episodes.
const season = (): Download[] => [running(1, 86), running(2, 82), ...Array.from({ length: 22 }, (_, i) => episode(i + 3))];

const dune: Download = { id: "dl-dune", titleId: "movie-1", titleName: "Dune", releaseName: "Dune.2021.2160p", state: "downloading", percent: 43, bytesDone: 4 * GB, bytesTotal: 9 * GB, stalled: false, bytesPerSecond: 0 };

interface World {
  downloads: Download[];
  paused: boolean;
  titles: TitleSummary[];
}

type Post = (url: URL, world: World) => Response | Promise<Response> | undefined;

function serve(world: World, post: Post = () => undefined) {
  return (url: URL, init: Request) => {
    if (init.method === "POST") return post(url, world) ?? json({ error: "nope" }, 404);
    if (url.pathname === "/api/wall") return json({ items: [] });
    if (url.pathname === "/api/downloads") return json({ downloads: world.downloads, paused: world.paused });
    if (url.pathname === "/api/history") return json({ history: [] });
    if (url.pathname === "/api/titles") return json({ titles: world.titles });
    return json({ error: "nope" }, 404);
  };
}

const byId = (world: World, id: string) => world.downloads.find((d) => d.id === id);
const swap = (world: World, id: string, over: Partial<Download>) => {
  world.downloads = world.downloads.map((d) => (d.id === id ? { ...d, ...over } : d));
  return byId(world, id);
};

const friends = () => screen.getByRole("heading", { level: 3, name: "Friends" }).closest("li") as HTMLElement;
const rows = () => within(within(friends()).getByRole("list", { name: "Friends downloads" })).getAllByRole("listitem");
const open = async () => {
  fireEvent.click(await screen.findByRole("button", { name: "Show 24 downloads of Friends" }));
  return rows();
};
const rowOf = (code: string) => rows().find((r) => within(r).queryByText(code)) as HTMLElement;

beforeEach(() => {
  vi.spyOn(console, "error").mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  window.location.hash = "";
});

test("Friends with two downloading and twenty-two queued episodes is one group whose summary says so", async () => {
  setupApp(serve({ downloads: [...season(), dune], paused: false, titles: [] }), "#/activity");

  const list = await screen.findByRole("list", { name: "Downloads" });
  const groups = within(list).getAllByRole("heading", { level: 3 });
  expect(groups.map((h) => h.textContent)).toEqual(["Friends", "Dune"]);
  expect(within(friends()).getByText("S01E01 86% · S01E02 82% · 22 waiting")).toBeTruthy();
  expect(screen.getByText("3 downloading · 22 waiting")).toBeTruthy();
  expect(within(friends()).queryByRole("list", { name: "Friends downloads" })).toBeNull();
  expect(within(friends()).getByRole("progressbar", { name: "Friends progress" }).getAttribute("aria-valuenow")).toBe("7");
});

test("expanding a group lists every episode with its state, percent, size, speed and time left", async () => {
  setupApp(serve({ downloads: season(), paused: false, titles: [] }), "#/activity");
  const toggle = await screen.findByRole("button", { name: "Show 24 downloads of Friends" });
  expect(toggle.getAttribute("aria-expanded")).toBe("false");

  fireEvent.click(toggle);

  expect(toggle.getAttribute("aria-expanded")).toBe("true");
  expect(toggle.getAttribute("aria-label")).toBe("Hide 24 downloads of Friends");
  expect(rows()).toHaveLength(24);
  const first = rowOf("S01E01");
  expect(within(first).getByText("Downloading")).toBeTruthy();
  expect(within(first).getByText("86%")).toBeTruthy();
  expect(within(first).getByText("3.1 of 3.6 GB · 48 MB/s · 11 s left")).toBeTruthy();
  const queued = rowOf("S01E09");
  expect(within(queued).getByText("Queued")).toBeTruthy();
  expect(within(queued).queryByText(/%/)).toBeNull();

  fireEvent.click(toggle);
  expect(screen.queryByRole("list", { name: "Friends downloads" })).toBeNull();
});

test("a row with no speed shows no time left", async () => {
  const world: World = { downloads: [running(1, 50, { bytesPerSecond: 0 }), ...Array.from({ length: 2 }, (_, i) => episode(i + 2))], paused: false, titles: [] };
  setupApp(serve(world), "#/activity");
  fireEvent.click(await screen.findByRole("button", { name: "Show 3 downloads of Friends" }));

  const first = rowOf("S01E01");
  expect(within(first).getByText("1.8 of 3.6 GB")).toBeTruthy();
  expect(first.textContent).not.toMatch(/left|\/s/);
});

test("Pause on a row shows Paused before the server has answered", async () => {
  const world: World = { downloads: season(), paused: false, titles: [] };
  let answer: (r: Response) => void = () => {};
  const { writes } = setupApp(
    serve(world, (url) => {
      if (url.pathname === "/api/downloads/dl-1/pause") return new Promise<Response>((r) => (answer = r));
    }),
    "#/activity",
  );
  await open();

  fireEvent.click(within(rowOf("S01E01")).getByRole("button", { name: "Pause S01E01 of Friends" }));

  expect(within(rowOf("S01E01")).getByText("Paused")).toBeTruthy();
  expect(within(rowOf("S01E01")).queryByRole("button", { name: "Pause S01E01 of Friends" })).toBeNull();
  expect(within(rowOf("S01E01")).getByRole("button", { name: "Resume S01E01 of Friends" })).toBeTruthy();
  await waitFor(() => expect(writes).toEqual([{ method: "POST", path: "/api/downloads/dl-1/pause", body: undefined }]));
  answer(json(swap(world, "dl-1", { state: "paused", bytesPerSecond: 0 })));
  await waitFor(() => expect(within(rowOf("S01E01")).getByText("Paused")).toBeTruthy());
});

test("a row's Pause then Resume round-trips through the server's answers", async () => {
  const world: World = { downloads: season(), paused: false, titles: [] };
  const { writes } = setupApp(
    serve(world, (url) => {
      if (url.pathname === "/api/downloads/dl-1/pause") return json(swap(world, "dl-1", { state: "paused", bytesPerSecond: 0 }));
      if (url.pathname === "/api/downloads/dl-1/resume") return json(swap(world, "dl-1", { state: "downloading" }));
    }),
    "#/activity",
  );
  await open();

  fireEvent.click(within(rowOf("S01E01")).getByRole("button", { name: "Pause S01E01 of Friends" }));
  const resume = await within(rowOf("S01E01")).findByRole("button", { name: "Resume S01E01 of Friends" });
  expect(within(rowOf("S01E01")).getByText("Paused")).toBeTruthy();
  expect(rowOf("S01E01").textContent).not.toMatch(/\/s/);

  fireEvent.click(resume);

  await waitFor(() => expect(within(rowOf("S01E01")).getByText("Downloading")).toBeTruthy());
  expect(within(rowOf("S01E01")).getByRole("button", { name: "Pause S01E01 of Friends" })).toBeTruthy();
  expect(writes.map((w) => w.path)).toEqual(["/api/downloads/dl-1/pause", "/api/downloads/dl-1/resume"]);
});

test("a movie's buttons are named for the movie", async () => {
  setupApp(serve({ downloads: [dune], paused: false, titles: [] }), "#/activity");

  expect(await screen.findByRole("button", { name: "Pause Dune" })).toBeTruthy();
  expect(screen.getByRole("button", { name: "Cancel Dune" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: /all downloads of Dune/ })).toBeNull();
});

test("Pause all on a group pauses every episode of that title and leaves other titles alone", async () => {
  const world: World = { downloads: [...season(), dune], paused: false, titles: [] };
  const { writes } = setupApp(
    serve(world, (url) => {
      if (url.pathname === "/api/titles/series-1/downloads/pause") {
        world.downloads = world.downloads.map((d) => (d.titleId === "series-1" ? { ...d, state: "paused", bytesPerSecond: 0 } : d));
        return json({ downloads: world.downloads.filter((d) => d.titleId === "series-1") });
      }
    }),
    "#/activity",
  );
  await open();

  fireEvent.click(within(friends()).getByRole("button", { name: "Pause all downloads of Friends" }));

  await waitFor(() => expect(within(friends()).getByText("S01E01 paused · S01E02 paused · S01E03 paused · +21 more")).toBeTruthy());
  expect(rows().every((r) => within(r).queryByText("Paused"))).toBe(true);
  expect(within(friends()).queryByRole("button", { name: "Pause all downloads of Friends" })).toBeNull();
  expect(within(friends()).getByRole("button", { name: "Resume downloads of Friends" })).toBeTruthy();
  expect(within(screen.getByRole("heading", { name: "Dune" }).closest("li") as HTMLElement).getByText("Downloading")).toBeTruthy();
  expect(writes).toEqual([{ method: "POST", path: "/api/titles/series-1/downloads/pause", body: undefined }]);
});

test("a group with one episode paused offers both Pause all and Resume", async () => {
  const world: World = { downloads: [running(1, 86), episode(3, { state: "paused", percent: 40 }), episode(4)], paused: false, titles: [] };
  setupApp(serve(world), "#/activity");
  await screen.findByRole("list", { name: "Downloads" });

  expect(within(friends()).getByRole("button", { name: "Pause all downloads of Friends" })).toBeTruthy();
  expect(within(friends()).getByRole("button", { name: "Resume downloads of Friends" })).toBeTruthy();
});

test("Resume on a group resumes what was paused", async () => {
  const world: World = { downloads: [episode(1, { state: "paused", percent: 40 }), episode(2, { state: "paused", percent: 10 }), episode(3)], paused: false, titles: [] };
  const { writes } = setupApp(
    serve(world, (url) => {
      if (url.pathname === "/api/titles/series-1/downloads/resume") {
        world.downloads = world.downloads.map((d) => (d.state === "paused" ? { ...d, state: "downloading", bytesPerSecond: 1_000_000 } : d));
        return json({ downloads: world.downloads.filter((d) => d.state === "downloading") });
      }
    }),
    "#/activity",
  );
  await screen.findByRole("list", { name: "Downloads" });

  fireEvent.click(within(friends()).getByRole("button", { name: "Resume downloads of Friends" }));

  await waitFor(() => expect(within(friends()).getByText("S01E01 40% · S01E02 10% · 1 waiting")).toBeTruthy());
  expect(writes).toEqual([{ method: "POST", path: "/api/titles/series-1/downloads/resume", body: undefined }]);
});

test("Cancel all asks first, Esc keeps the downloads and the page, and confirming cancels only that title", async () => {
  const world: World = { downloads: [...season(), dune], paused: false, titles: [] };
  const { writes } = setupApp(
    serve(world, (url) => {
      if (url.pathname === "/api/titles/series-1/downloads/cancel") {
        world.downloads = world.downloads.filter((d) => d.titleId !== "series-1");
        return new Response(null, { status: 204 });
      }
    }),
    "#/activity",
  );
  await screen.findByRole("list", { name: "Downloads" });

  fireEvent.click(within(friends()).getByRole("button", { name: "Cancel all downloads of Friends" }));

  const ask = within(friends()).getByRole("group", { name: "Cancel all downloads of Friends" });
  expect(ask.textContent).toMatch(/Cancel all 24 downloads of Friends\? Their releases are blocked/);
  expect(document.activeElement).toBe(within(ask).getByRole("button", { name: "Keep downloads of Friends" }));
  expect(writes).toEqual([]);

  fireEvent.keyDown(document.activeElement as Element, { key: "Escape" });
  expect(within(friends()).queryByRole("group")).toBeNull();
  expect(screen.getByRole("heading", { level: 1, name: "Activity" })).toBeTruthy();
  expect(document.activeElement).toBe(within(friends()).getByRole("button", { name: "Cancel all downloads of Friends" }));
  expect(writes).toEqual([]);

  fireEvent.click(within(friends()).getByRole("button", { name: "Cancel all downloads of Friends" }));
  fireEvent.click(screen.getByRole("button", { name: "Yes, cancel all downloads of Friends" }));

  await waitFor(() => expect(screen.queryByRole("heading", { level: 3, name: "Friends" })).toBeNull());
  expect(screen.getByRole("heading", { level: 3, name: "Dune" })).toBeTruthy();
  expect(writes).toEqual([{ method: "POST", path: "/api/titles/series-1/downloads/cancel", body: undefined }]);
  expect((await screen.findByRole("status")).textContent).toContain("Cancelled 24 downloads of Friends");
});

test("a row's Cancel confirms inline, in place, before it cancels that one episode", async () => {
  const world: World = { downloads: season(), paused: false, titles: [] };
  const { writes } = setupApp(
    serve(world, (url) => {
      if (url.pathname === "/api/downloads/dl-2/cancel") {
        world.downloads = world.downloads.filter((d) => d.id !== "dl-2");
        return new Response(null, { status: 204 });
      }
    }),
    "#/activity",
  );
  await open();

  fireEvent.click(within(rowOf("S01E02")).getByRole("button", { name: "Cancel S01E02 of Friends" }));
  expect(writes).toEqual([]);
  expect(document.activeElement).toBe(within(rowOf("S01E02")).getByRole("button", { name: "Keep S01E02 of Friends" }));

  fireEvent.click(within(rowOf("S01E02")).getByRole("button", { name: "Keep S01E02 of Friends" }));
  expect(within(rowOf("S01E02")).queryByRole("group")).toBeNull();
  expect(writes).toEqual([]);

  fireEvent.click(within(rowOf("S01E02")).getByRole("button", { name: "Cancel S01E02 of Friends" }));
  fireEvent.click(within(rowOf("S01E02")).getByRole("button", { name: "Yes, cancel S01E02 of Friends" }));

  await waitFor(() => expect(rows()).toHaveLength(23));
  expect(within(friends()).getByText("S01E01 86% · 22 waiting")).toBeTruthy();
  expect(writes).toEqual([{ method: "POST", path: "/api/downloads/dl-2/cancel", body: undefined }]);
});

test("a download being verified or imported offers no Pause or Cancel, since the server would refuse", async () => {
  const world: World = { downloads: [episode(1, { state: "verifying", percent: 100 }), episode(2, { state: "importing", percent: 100 }), running(3, 5)], paused: false, titles: [] };
  setupApp(serve(world), "#/activity");
  fireEvent.click(await screen.findByRole("button", { name: "Show 3 downloads of Friends" }));

  expect(within(rowOf("S01E01")).queryByRole("button")).toBeNull();
  expect(within(rowOf("S01E02")).queryByRole("button")).toBeNull();
  expect(within(rowOf("S01E03")).getAllByRole("button")).toHaveLength(2);
});

test("the global Pause all says downloads are paused and every running row reads Paused; Resume all undoes it", async () => {
  const world: World = { downloads: [...season(), dune], paused: false, titles: [] };
  const { writes } = setupApp(
    serve(world, (url) => {
      if (url.pathname === "/api/downloads/pause") {
        world.paused = true;
        world.downloads = world.downloads.map((d) => (d.state === "downloading" ? { ...d, state: "paused", bytesPerSecond: 0 } : d));
        return json({ downloads: world.downloads, paused: true });
      }
      if (url.pathname === "/api/downloads/resume") {
        world.paused = false;
        world.downloads = world.downloads.map((d) => (d.state === "paused" ? { ...d, state: "downloading" } : d));
        return json({ downloads: world.downloads, paused: false });
      }
    }),
    "#/activity",
  );
  await open();
  expect(screen.queryByText("Downloads paused")).toBeNull();

  fireEvent.click(screen.getByRole("button", { name: "Pause all downloads" }));

  await screen.findByRole("button", { name: "Resume all downloads" });
  expect(screen.getByRole("status").textContent).toContain("Downloads paused");
  expect(screen.getByRole("status").textContent).toContain("stays paused after Resume all");
  expect(within(rowOf("S01E01")).getByText("Paused")).toBeTruthy();
  expect(within(rowOf("S01E02")).getByText("Paused")).toBeTruthy();
  expect(within(rowOf("S01E03")).getByText("Queued")).toBeTruthy();
  expect(within(screen.getByRole("heading", { name: "Dune" }).closest("li") as HTMLElement).getByText("Paused")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /^Pause S01E01/ })).toBeNull();
  expect(screen.queryByRole("button", { name: /^Resume S01E01/ })).toBeNull();
  expect(screen.queryByRole("button", { name: /^Pause all downloads/ })).toBeNull();
  expect(within(rowOf("S01E01")).getByRole("button", { name: "Cancel S01E01 of Friends" })).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "Resume all downloads" }));

  await waitFor(() => expect(screen.queryByText("Downloads paused")).toBeNull());
  await waitFor(() => expect(within(rowOf("S01E01")).getByText("Downloading")).toBeTruthy());
  expect(screen.getByRole("button", { name: "Pause all downloads" })).toBeTruthy();
  expect(writes.map((w) => w.path)).toEqual(["/api/downloads/pause", "/api/downloads/resume"]);
});

test("a page that opens while downloads are paused says so, and the live event flips it", async () => {
  const world: World = { downloads: [dune], paused: true, titles: [] };
  setupApp(serve(world), "#/activity");

  await screen.findByRole("button", { name: "Resume all downloads" });
  expect(screen.getByRole("status").textContent).toContain("Downloads paused");
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));

  world.paused = false;
  FakeEventSource.open()[0].emit("downloads", { paused: false });

  await waitFor(() => expect(screen.queryByText("Downloads paused")).toBeNull());
  expect(screen.getByRole("button", { name: "Pause all downloads" })).toBeTruthy();
});

test("with nothing downloading the switch is still there while downloads are paused", async () => {
  setupApp(serve({ downloads: [], paused: true, titles: [] }), "#/activity");

  expect(await screen.findByRole("button", { name: "Resume all downloads" })).toBeTruthy();
  expect(screen.getByText("Nothing is downloading.")).toBeTruthy();
});

test("a pause the server refuses puts the row back and a toast says why with Try again", async () => {
  const world: World = { downloads: season(), paused: false, titles: [] };
  let asked = 0;
  setupApp(
    serve(world, (url) => {
      if (url.pathname !== "/api/downloads/dl-1/pause") return undefined;
      asked += 1;
      return asked === 1 ? json({ error: "this download has already finished" }, 409) : json(swap(world, "dl-1", { state: "paused" }));
    }),
    "#/activity",
  );
  await open();

  fireEvent.click(within(rowOf("S01E01")).getByRole("button", { name: "Pause S01E01 of Friends" }));

  expect((await screen.findByRole("alert")).textContent).toContain("this download has already finished");
  expect(within(rowOf("S01E01")).getByText("Downloading")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Try again" }));
  await waitFor(() => expect(asked).toBe(2));
  await waitFor(() => expect(within(rowOf("S01E01")).getByText("Paused")).toBeTruthy());
});

test("a refused Cancel all brings the downloads back with a toast to try again; a refused Pause all unsays it", async () => {
  const world: World = { downloads: [...season(), dune], paused: false, titles: [] };
  setupApp(
    serve(world, (url) => {
      if (url.pathname.endsWith("/cancel") || url.pathname === "/api/downloads/pause") return json({ error: "marquee did not answer" }, 502);
    }),
    "#/activity",
  );
  await screen.findByRole("list", { name: "Downloads" });

  fireEvent.click(within(friends()).getByRole("button", { name: "Cancel all downloads of Friends" }));
  fireEvent.click(screen.getByRole("button", { name: "Yes, cancel all downloads of Friends" }));
  expect((await screen.findByRole("alert")).textContent).toContain("marquee did not answer");
  await waitFor(() => expect(screen.getByRole("heading", { level: 3, name: "Friends" })).toBeTruthy());
  expect(within(friends()).getByText("S01E01 86% · S01E02 82% · 22 waiting")).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: "Pause all downloads" }));
  await waitFor(() => expect(screen.getAllByRole("alert").length).toBeGreaterThan(0));
  await waitFor(() => expect(screen.queryByText("Downloads paused")).toBeNull());
  expect(screen.getByRole("button", { name: "Pause all downloads" })).toBeTruthy();
});

test("a title waiting on its own episodes folds into its group as N waiting, and a title that only waits keeps one compact row", async () => {
  const waiting: TitleSummary[] = [
    { id: "series-1", tmdbId: 1, kind: "series", title: "Friends", year: 1994, overview: "", posterPath: "", backdropPath: "", inLibrary: true, searchingEpisodes: ["s02e01", "s02e02"], badge: { state: "wanted", label: "Wanted" } },
    { id: "series-9", tmdbId: 9, kind: "series", title: "House", year: 2004, overview: "", posterPath: "", backdropPath: "", inLibrary: true, searchingEpisodes: ["s04e02"], badge: { state: "not_found", label: "Not found" } },
  ];
  setupApp(serve({ downloads: [running(1, 50), episode(2)], paused: false, titles: waiting }), "#/activity");

  await screen.findByRole("list", { name: "Downloads" });
  expect(within(friends()).getByText("S01E01 50% · 3 waiting")).toBeTruthy();
  const queue = within(screen.getByRole("region", { name: "Waiting" })).getAllByRole("listitem");
  expect(queue).toHaveLength(1);
  expect(within(queue[0]).getByRole("link", { name: "House" })).toBeTruthy();
  expect(within(queue[0]).getByText("Not found · S04E02")).toBeTruthy();
  expect(within(screen.getByRole("region", { name: "Downloading" })).queryByText("House")).toBeNull();
});

test("a title with only queued episodes waits as one compact row that can still be paused or cancelled", async () => {
  const world: World = { downloads: [episode(1), episode(2)], paused: false, titles: [] };
  setupApp(serve(world), "#/activity");

  const queue = await screen.findByRole("region", { name: "Waiting" });
  expect(within(queue).getByText("2 queued")).toBeTruthy();
  expect(within(queue).getByRole("button", { name: "Pause all downloads of Friends" })).toBeTruthy();
  expect(within(queue).getByRole("button", { name: "Cancel all downloads of Friends" })).toBeTruthy();
  expect(screen.getByText("Nothing is downloading.")).toBeTruthy();
});

test("progress events keep a row's speed and time left current", async () => {
  setupApp(serve({ downloads: season(), paused: false, titles: [] }), "#/activity");
  await open();
  await waitFor(() => expect(FakeEventSource.open().length).toBe(1));

  FakeEventSource.open()[0].emit("progress", { downloadId: "dl-1", titleId: "series-1", episode: "s01e01", state: "downloading", percent: 90, bytesDone: Math.round(3.6 * GB * 0.9), bytesTotal: Math.round(3.6 * GB), bytesPerSecond: 10 * 1024 ** 2 });

  expect(await within(rowOf("S01E01")).findByText("3.2 of 3.6 GB · 10 MB/s · 37 s left")).toBeTruthy();
});

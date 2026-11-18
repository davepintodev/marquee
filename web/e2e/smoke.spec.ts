import { mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test, type Page } from "@playwright/test";

const shots = process.env.MARQUEE_SHOTS_DIR ?? join(tmpdir(), "mrq-229-shots");
mkdirSync(shots, { recursive: true });

const screens = [
  { name: "phone", width: 390, height: 844 },
  { name: "laptop", width: 1440, height: 900 },
  { name: "ultrawide", width: 3440, height: 1440 },
];

const GAP = 10;

async function tileBoxes(page: Page) {
  await expect(page.locator(".tile[data-shape] img").first()).toBeVisible();
  return page.locator(".tile[data-shape]").evaluateAll((els) =>
    els.map((el) => {
      const r = el.getBoundingClientRect();
      return { x: r.x, y: r.y, w: r.width, h: r.height, shape: (el as HTMLElement).dataset.shape! };
    }),
  );
}

for (const s of screens.slice(0, 2)) {
  test(`the header pills sit above the wall and cover no tile or badge on ${s.name}`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await page.goto("/");
    const tiles = await tileBoxes(page);
    const nav = (await page.locator(".nav").boundingBox())!;

    for (const t of tiles) expect(t.y, `tile at ${t.x},${t.y} starts below the pills`).toBeGreaterThanOrEqual(nav.y + nav.height);

    const corner = page.locator(".tile[data-shape]").evaluateAll((els) => {
      const best = els.reduce((a, b) => {
        const ra = a.getBoundingClientRect();
        const rb = b.getBoundingClientRect();
        return rb.top < ra.top || (rb.top === ra.top && rb.right > ra.right) ? b : a;
      });
      return best.querySelector(".badge")?.getBoundingClientRect().toJSON() ?? null;
    });
    const badge = await corner;
    expect(badge, "the top-right tile has a status badge").not.toBeNull();
    const apart = badge.right <= nav.x || badge.x >= nav.x + nav.width || badge.bottom <= nav.y || badge.y >= nav.y + nav.height;
    expect(apart, "the top-right badge does not touch the pills").toBe(true);
    await page.screenshot({ path: join(shots, `wall-header-${s.name}.png`), clip: { x: 0, y: 0, width: s.width, height: Math.min(s.height, 400) } });
  });
}

for (const s of screens) {
  test(`the wall fills a ${s.name} screen with 2:3 posters and 16:9 backdrops`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await page.goto("/");
    const tiles = await tileBoxes(page);

    const top = Math.min(...tiles.map((t) => t.y));
    expect(top).toBeGreaterThan(40);
    expect(top).toBeLessThan(90);
    expect(tiles.length).toBeGreaterThan(4);
    const area = tiles.reduce((a, t) => a + t.w * t.h, 0);
    expect(area / (s.width * (s.height - top))).toBeGreaterThan(0.99);
    expect(Math.min(...tiles.map((t) => t.x))).toBeCloseTo(0, 0);
    expect(Math.max(...tiles.map((t) => t.x + t.w))).toBeCloseTo(s.width, 0);
    expect(Math.max(...tiles.map((t) => t.y + t.h))).toBeCloseTo(s.height, 0);
    for (const t of tiles) {
      const want = t.shape === "portrait" ? 2 / 3 : 16 / 9;
      const got = (t.w - GAP) / (t.h - GAP);
      expect(got / want, `${t.shape} ${t.w}×${t.h}`).toBeGreaterThan(0.8);
      expect(got / want, `${t.shape} ${t.w}×${t.h}`).toBeLessThan(1.25);
    }
    expect(new Set(tiles.map((t) => t.shape))).toEqual(new Set(["portrait", "wide"]));

    const broken = await page.locator(".tile img").evaluateAll((imgs) =>
      imgs.filter((i) => !(i as HTMLImageElement).complete || (i as HTMLImageElement).naturalWidth === 0).length,
    );
    expect(broken).toBe(0);

    const bar = await page.getByRole("searchbox").boundingBox();
    expect(Math.abs(bar!.x + bar!.width / 2 - s.width / 2)).toBeLessThan(2);
    expect(Math.abs(bar!.y + bar!.height / 2 - s.height / 2)).toBeLessThan(2);

    await page.screenshot({ path: join(shots, `wall-${s.name}.png`) });
  });
}

test("the library comes first on the wall, then trending", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator(".tile[data-shape]").first()).toBeVisible();
  const first = await page.locator(".tile[data-shape]").first().getAttribute("aria-label");
  expect(first).toMatch(/Downloaded|missing|Searching|Downloading|Stalled/i);
});

for (const s of screens.slice(0, 2)) {
  test(`typing blurs the wall, glides the bar up and fades results in on ${s.name}; Esc brings the wall back`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await page.goto("/");
    await tileBoxes(page);
    await page.waitForTimeout(500);
    const wall = page.getByTestId("wall");
    const bar = page.getByRole("searchbox");
    await expect(bar).toBeFocused();

    await bar.pressSequentially("dune", { delay: 120 });

    await expect(page.locator(".app")).toHaveAttribute("data-mode", "search");
    await expect(wall).toHaveCSS("filter", /blur\(16px\)/);
    await expect.poll(async () => (await bar.boundingBox())!.y, { timeout: 3000 }).toBeLessThan(80);
    const results = page.getByRole("list", { name: "Results" });
    const cards = results.getByRole("listitem");
    await expect(cards.first()).toBeVisible();
    await expect(results.getByText(/\d{4} · (movie|series)/).first()).toBeVisible();
    await expect(results.getByText(/Downloading \d+%/).first()).toBeVisible();
    expect(await cards.count()).toBeGreaterThanOrEqual(2);
    await page.waitForTimeout(600);
    await page.screenshot({ path: join(shots, `search-${s.name}.png`) });

    await page.keyboard.press("Escape");

    await expect(page.locator(".app")).toHaveAttribute("data-mode", "wall");
    await expect(bar).toHaveValue("");
    await expect(wall).toHaveCSS("filter", "none");
    await expect.poll(async () => { const b = (await bar.boundingBox())!; return Math.abs(b.y + b.height / 2 - s.height / 2); }, { timeout: 3000 }).toBeLessThan(3);
    await page.waitForTimeout(600);
    await page.screenshot({ path: join(shots, `search-esc-${s.name}.png`) });
  });
}

test("a search that finds nothing says so", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("searchbox").fill("qqqzzz");
  await expect(page.getByText("Nothing found.")).toBeVisible();
});

test("dark for a dark system, light when the system asks for it", async ({ browser }) => {
  const bg = async (colorScheme: "dark" | "light") => {
    const ctx = await browser.newContext({ colorScheme });
    const page = await ctx.newPage();
    await page.goto("/");
    await expect(page.locator(".tile[data-shape] img").first()).toBeVisible();
    const color = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
    if (colorScheme === "light") await page.screenshot({ path: join(shots, "wall-light.png") });
    await ctx.close();
    return color;
  };
  expect(await bg("dark")).toBe("rgb(11, 11, 15)");
  expect(await bg("light")).toBe("rgb(244, 244, 247)");
});

// Each screen adds a different trending title: the fake keeps what was added for the whole run.
const popupTitles = [
  { ...screens[0], title: "Severance" },
  { ...screens[1], title: "Andor" },
];

for (const s of popupTitles) {
  test(`the popup opens from a result on ${s.name}, adds on Add & monitor and then reads Search now`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await page.goto("/");
    await expect(page.locator(".tile[data-shape] img").first()).toBeVisible();
    const bar = page.getByRole("searchbox");
    await bar.pressSequentially(s.title.toLowerCase(), { delay: 80 });
    await page.getByRole("list", { name: "Results" }).getByRole("button", { name: new RegExp(s.title) }).click();

    const popup = page.getByRole("dialog");
    await expect(popup.getByRole("heading", { name: s.title })).toBeVisible();
    await expect(popup.getByText(/2022 · series/)).toBeVisible();
    await expect(popup.locator("img")).toBeVisible();
    await expect(popup.getByRole("button", { name: "Add & monitor" })).toBeVisible();
    await expect(popup.getByRole("button", { name: "Interactive search" })).toBeVisible();
    await page.waitForTimeout(500);
    const box = (await popup.boundingBox())!;
    expect(box.x).toBeGreaterThanOrEqual(0);
    expect(box.x + box.width).toBeLessThanOrEqual(s.width);
    expect(box.y).toBeGreaterThanOrEqual(0);
    expect(box.y + box.height).toBeLessThanOrEqual(s.height);
    await page.screenshot({ path: join(shots, `popup-${s.name}.png`) });

    await popup.getByRole("button", { name: "Add & monitor" }).click();

    await expect(page.getByRole("status")).toContainText(s.title);
    await expect(popup.getByRole("button", { name: "Search now" })).toBeVisible();
    await page.waitForTimeout(500);
    await page.screenshot({ path: join(shots, `popup-added-${s.name}.png`) });

    await page.keyboard.press("Escape");
    await expect(popup).toBeHidden();
    await expect(bar).toHaveValue(s.title.toLowerCase());
  });
}

test("a wall tile opens the popup and a title already in the library reads Search now", async ({ page }) => {
  await page.setViewportSize({ width: screens[1].width, height: screens[1].height });
  await page.goto("/");
  await expect(page.locator(".tile[data-shape] img").first()).toBeVisible();

  await page.locator(".tile[data-shape]").first().click();

  const popup = page.getByRole("dialog");
  await expect(popup.getByRole("button", { name: "Search now" })).toBeVisible();
  await expect(popup.getByRole("button", { name: "Add & monitor" })).toHaveCount(0);
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(shots, "popup-library-laptop.png") });
});

async function noSideScroll(page: Page, width: number) {
  const overflow = await page.evaluate(() => document.querySelector(".page-layer")!.scrollWidth - document.querySelector(".page-layer")!.clientWidth);
  expect(overflow, `page is ${width}px wide and must not scroll sideways`).toBeLessThanOrEqual(0);
}

for (const s of screens.slice(0, 2)) {
  test(`a series' detail shows its seasons, episodes and history in words on ${s.name}`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await page.goto("/#/title/series-1408");

    await expect(page.getByRole("heading", { name: "House", level: 1 })).toBeVisible();
    await expect(page.getByText(/^Tried 3 releases, 1 stalled, 1 fake, retrying in 6h\.$/)).toBeVisible();
    const season4 = page.getByRole("region", { name: "Season 4" });
    await expect(season4.getByRole("listitem")).toHaveCount(4);
    await expect(season4.getByText("Missing").first()).toBeVisible();
    await expect(season4.getByText("Downloaded")).toBeVisible();
    await expect(page.locator(".page").getByText("Season 4 · 3 missing")).toHaveCount(1);
    await expect(season4.getByText("3 missing")).toBeVisible();
    const overview = page.locator(".detail-overview");
    await expect(overview).toBeVisible();
    await expect(overview).toHaveCSS("font-size", "16px");
    const ob = (await overview.boundingBox())!;
    expect(ob.width, "the overview has a readable line length").toBeGreaterThan(Math.min(240, s.width - 60));
    expect(ob.x + ob.width).toBeLessThanOrEqual(s.width);
    await expect(page.getByRole("list", { name: "History" }).getByText("Stalled")).toBeVisible();
    await noSideScroll(page, s.width);
    await page.waitForTimeout(500);
    await page.screenshot({ path: join(shots, `detail-${s.name}.png`) });
    await page.locator(".page-layer").evaluate((el) => el.scrollTo(0, el.scrollHeight));
    await page.waitForTimeout(300);
    await page.screenshot({ path: join(shots, `detail-${s.name}-scrolled.png`) });
  });
}

test("detail: Search now and Unmonitor on an episode change it in place, Remove asks first", async ({ page }) => {
  await page.setViewportSize({ width: screens[1].width, height: screens[1].height });
  await page.goto("/#/title/series-1408");
  const season4 = page.getByRole("region", { name: "Season 4" });

  await season4.getByRole("button", { name: "Search now for Big Baby" }).click();
  await expect(season4.getByRole("listitem").nth(1).getByText("Searching")).toBeVisible();
  await season4.getByRole("button", { name: "Unmonitor Big Baby" }).click();
  await expect(season4.getByRole("listitem").nth(1).getByText("Unmonitored")).toBeVisible();
  await season4.getByRole("button", { name: "Monitor Big Baby" }).click();
  await expect(season4.getByRole("listitem").nth(1).getByText("Unmonitored")).toHaveCount(0);

  await page.getByRole("button", { name: "Remove from marquee" }).click();
  await expect(page.getByText(/files stay on disk/i)).toBeVisible();
  await page.waitForTimeout(300);
  await page.screenshot({ path: join(shots, "detail-remove-laptop.png") });
  await page.getByRole("button", { name: "Cancel" }).click();
  await page.keyboard.press("Escape");
  await expect(page.locator(".app")).toHaveAttribute("data-mode", "wall");
});

const retries = [
  { ...screens[0], retry: "Retry Arrival", queued: "Arrival" },
  { ...screens[1], retry: "Retry Blade Runner 2049", queued: "Blade Runner 2049" },
];

for (const s of retries) {
  test(`Activity shows the queue and history, progress moves live and Retry puts a failure back in the queue on ${s.name}`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await page.goto("/");
    await expect(page.locator(".tile[data-shape] img").first()).toBeVisible();
    await page.getByRole("link", { name: "Activity" }).click();

    const queue = page.getByRole("list", { name: "Queue" });
    const history = page.getByRole("list", { name: "History" });
    await expect(queue.getByRole("listitem").first()).toBeVisible();
    await expect(history.getByText("Fake file").first()).toBeVisible();
    await expect(history.getByText(/\.exe file, not a video/).first()).toBeVisible();
    await noSideScroll(page, s.width);

    const dune = queue.getByRole("listitem").filter({ hasText: "Dune: Part Two" });
    const progress = dune.getByRole("progressbar");
    const first = await progress.getAttribute("aria-valuenow");
    await page.waitForTimeout(500);
    await page.screenshot({ path: join(shots, `activity-${s.name}.png`) });
    await expect.poll(() => progress.getAttribute("aria-valuenow"), { timeout: 15_000 }).not.toBe(first);

    await history.getByRole("button", { name: s.retry }).click();

    await expect(history.getByRole("button", { name: s.retry })).toHaveCount(0);
    await expect(queue.getByRole("listitem").filter({ hasText: s.queued }).getByText("Searching")).toBeVisible();
    await expect(page.getByRole("status")).toContainText(s.queued);
    await page.waitForTimeout(500);
    await page.screenshot({ path: join(shots, `activity-retried-${s.name}.png`) });

    await page.keyboard.press("Escape");
    await expect(page.locator(".app")).toHaveAttribute("data-mode", "wall");
  });
}

test("a library popup opens the title's detail", async ({ page }) => {
  await page.setViewportSize({ width: screens[1].width, height: screens[1].height });
  await page.goto("/");
  await expect(page.locator(".tile[data-shape] img").first()).toBeVisible();
  await page.locator(".tile[data-shape]").first().click();
  await page.getByRole("dialog").getByRole("link", { name: "Details" }).click();
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("retrying a failed series episode puts it in the queue", async ({ page }) => {
  await page.setViewportSize({ width: screens[1].width, height: screens[1].height });
  await page.goto("/#/activity");
  const history = page.getByRole("list", { name: "History" });

  await history.getByRole("button", { name: "Retry House" }).click();

  const queue = page.getByRole("list", { name: "Queue" });
  await expect(queue.getByRole("listitem").filter({ hasText: "House" }).getByText(/S04E02/)).toBeVisible();
});

test("detail and Activity read well in the light theme", async ({ browser }) => {
  const ctx = await browser.newContext({ colorScheme: "light", viewport: { width: screens[1].width, height: screens[1].height } });
  const page = await ctx.newPage();
  await page.goto("/#/title/series-1408");
  await expect(page.getByRole("heading", { name: "House", level: 1 })).toBeVisible();
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(shots, "detail-light.png") });
  await page.goto("/#/activity");
  await expect(page.getByRole("list", { name: "Queue" })).toBeVisible();
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(shots, "activity-light.png") });
  await ctx.close();
});

const allWell = { status: "ok", dryRun: false, checks: [{ id: "tmdb", name: "TMDB", status: "ok", message: "Key accepted." }], libraryProblems: [] };

for (const s of screens.slice(0, 2)) {
  test(`the Health pill has no dot when all is well, a red dot when a check fails, and opens Health on ${s.name}`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    const pill = page.locator(".nav-link", { hasText: "Health" });
    const dot = pill.locator("i");
    const header = { x: s.width - 320, y: 0, width: 320, height: 80 };

    await page.route("**/api/health", (r) => r.fulfill({ json: allWell }));
    await page.goto("/");
    await expect(pill).toBeVisible();
    await expect(pill).toHaveAccessibleName("Health");
    await expect(dot).toHaveCount(0);
    await page.screenshot({ path: join(shots, `health-pill-ok-${s.name}.png`), clip: header });

    await page.unroute("**/api/health");
    await page.goto("/");
    await expect(pill).toHaveAccessibleName(/needs attention/);
    await expect(dot).toBeVisible();
    // The problem colour is the Missing badge's token, not a new one.
    const bad = await page.evaluate(() => {
      const probe = document.createElement("span");
      probe.style.background = "var(--bad)";
      document.body.append(probe);
      const c = getComputedStyle(probe).backgroundColor;
      probe.remove();
      return c;
    });
    await expect(dot).toHaveCSS("background-color", bad);
    await page.screenshot({ path: join(shots, `health-pill-failing-${s.name}.png`), clip: header });

    await pill.click();
    await expect(page.getByRole("heading", { name: "Health" })).toBeVisible();
    await page.waitForTimeout(400);
    await page.screenshot({ path: join(shots, `health-page-${s.name}.png`) });
    await page.keyboard.press("Escape");
    await expect(page.getByRole("searchbox")).toBeVisible();

    await page.goto("/health");
    await expect(page.getByRole("heading", { name: "Health" })).toBeVisible();
  });
}

async function openInteractive(page: Page, query: string, name: RegExp) {
  await page.goto("/");
  await expect(page.locator(".tile[data-shape] img").first()).toBeVisible();
  await page.getByRole("searchbox").pressSequentially(query, { delay: 60 });
  await page.getByRole("list", { name: "Results" }).getByRole("button", { name }).click();
  const popup = page.getByRole("dialog");
  // Clicking while the sheet is still sliding in makes the browser scroll the page to reach it.
  await page.waitForTimeout(500);
  await popup.getByRole("button", { name: "Interactive search" }).click();
  return popup;
}

async function insidePopup(page: Page, s: { width: number; height: number }) {
  await page.waitForTimeout(500);
  const box = (await page.getByRole("dialog").boundingBox())!;
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(s.width);
  expect(box.y).toBeGreaterThanOrEqual(0);
  expect(box.y + box.height).toBeLessThanOrEqual(s.height);
}

for (const s of screens.slice(0, 2)) {
  test(`interactive search lists a movie's ranked releases with quality, size, source and cached on ${s.name}`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    const popup = await openInteractive(page, "dune", /Dune[\s\S]*2021/);

    const releases = popup.getByRole("list", { name: "Releases" });
    const rows = releases.getByRole("listitem");
    await expect(rows).toHaveCount(2);
    await expect(rows.first()).toContainText("Blade.Runner.2049.2017.1080p.BluRay.x264");
    await expect(rows.first()).toContainText("Bluray-1080p");
    await expect(rows.first()).toContainText("12 GB");
    await expect(rows.first()).toContainText("Torrentio");
    await expect(rows.first()).toContainText("Cached");
    await expect(rows.nth(1)).toContainText("WEBDL-2160p");
    await insidePopup(page, s);
    await page.waitForTimeout(500);
    await page.screenshot({ path: join(shots, `interactive-movie-${s.name}.png`) });

    await popup.getByText(/^Dropped \(\d+\)$/).click();
    const dropped = popup.getByRole("list", { name: "Dropped" });
    await expect(dropped.getByText("a .exe file, not a video")).toBeVisible();
    await expect(dropped.getByRole("button")).toHaveCount(0);
    await insidePopup(page, s);
    await page.screenshot({ path: join(shots, `interactive-movie-dropped-${s.name}.png`) });

    await popup.getByRole("button", { name: "Back" }).click();
    await expect(popup.getByRole("button", { name: "Add & monitor" })).toBeVisible();
    await expect(popup.getByRole("button", { name: "Interactive search" })).toBeFocused();
  });
}

test("picking a release queues it: the toast confirms, the popup reads Search now and Activity shows the download", async ({ page }) => {
  await page.setViewportSize({ width: screens[1].width, height: screens[1].height });
  const popup = await openInteractive(page, "dune", /Dune[\s\S]*2021/);

  await popup.getByRole("button", { name: "Pick Blade.Runner.2049.2017.2160p.WEB-DL" }).click();

  await expect(page.getByRole("status")).toContainText("Queued Blade.Runner.2049.2017.2160p.WEB-DL for “Dune”");
  await expect(popup.getByText("Queued", { exact: true })).toBeVisible();
  await page.waitForTimeout(400);
  await page.screenshot({ path: join(shots, "interactive-picked-laptop.png") });
  await popup.getByRole("button", { name: "Back" }).click();
  await expect(popup.getByRole("button", { name: "Search now" })).toBeVisible();

  await page.keyboard.press("Escape");
  await page.keyboard.press("Escape");
  await page.getByRole("link", { name: "Activity" }).click();
  const queue = page.getByRole("list", { name: "Queue" });
  const row = queue.getByRole("listitem").filter({ hasText: "Blade.Runner.2049.2017.2160p.WEB-DL" });
  await expect(row).toBeVisible();
  await expect(row).toContainText("Dune");
  await page.waitForTimeout(400);
  await page.screenshot({ path: join(shots, "interactive-activity-laptop.png") });
});

test("a series not in the library says to add it first when Interactive search is pressed", async ({ page }) => {
  await page.setViewportSize({ width: screens[0].width, height: screens[0].height });
  const popup = await openInteractive(page, "mr. robot", /Mr\. Robot[\s\S]*2015/);

  await expect(popup.getByText(/Add & monitor this series first/)).toBeVisible();
  await expect(popup.getByRole("combobox", { name: "Episode" })).toHaveCount(0);
  await expect(popup.getByRole("list", { name: "Releases" })).toHaveCount(0);
  await insidePopup(page, screens[0]);
  await page.screenshot({ path: join(shots, "interactive-series-add-first-phone.png") });

  await popup.getByRole("button", { name: "Back" }).click();
  await expect(popup.getByRole("button", { name: "Add & monitor" })).toBeVisible();
});

test("a library series asks for an episode and a blocklisted release can be picked by hand on phone", async ({ page }) => {
  await page.setViewportSize({ width: screens[0].width, height: screens[0].height });
  const popup = await openInteractive(page, "camp snoopy", /Camp Snoopy[\s\S]*2024/);

  const episode = popup.getByRole("combobox", { name: "Episode" });
  await expect(episode).toHaveValue("s01e01");
  await episode.selectOption("s01e02");
  await expect(popup.getByRole("list", { name: "Releases" }).getByRole("listitem").first()).toBeVisible();
  await insidePopup(page, screens[0]);
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(shots, "interactive-series-phone.png") });

  await popup.getByText(/^Dropped \(\d+\)$/).click();
  const blocked = popup.getByRole("list", { name: "Dropped" }).getByRole("listitem").filter({ hasText: "failed before (stalled)" });
  await expect(blocked).toBeVisible();
  await blocked.scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(shots, "interactive-blocklisted-phone.png") });
  await blocked.getByRole("button", { name: /anyway$/ }).click();

  await expect(page.getByRole("status")).toContainText("Queued House.S04E02.1080p.WEB-DL");
  await expect(blocked.getByText("Queued", { exact: true })).toBeVisible();

  await page.keyboard.press("Escape");
  await page.keyboard.press("Escape");
  await page.getByRole("link", { name: "Activity" }).click();
  const row = page.getByRole("list", { name: "Queue" }).getByRole("listitem").filter({ hasText: "House.S04E02.1080p.WEB-DL" });
  await expect(row).toBeVisible();
  await expect(row).toContainText(/Camp Snoopy.*S01E02/);
});

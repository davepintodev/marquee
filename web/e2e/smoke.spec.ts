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

for (const s of screens) {
  test(`the wall fills a ${s.name} screen with 2:3 posters and 16:9 backdrops`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await page.goto("/");
    const tiles = await tileBoxes(page);

    expect(tiles.length).toBeGreaterThan(4);
    const area = tiles.reduce((a, t) => a + t.w * t.h, 0);
    expect(area / (s.width * s.height)).toBeGreaterThan(0.99);
    expect(Math.min(...tiles.map((t) => t.x))).toBeCloseTo(0, 0);
    expect(Math.min(...tiles.map((t) => t.y))).toBeCloseTo(0, 0);
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
  test(`the Health icon is neutral when all is well, orange when a check fails, and opens Health on ${s.name}`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    const icon = page.locator(".nav-icon");
    const stroke = () => icon.locator("svg").evaluate((el) => getComputedStyle(el).stroke);
    const header = { x: s.width - 200, y: 0, width: 200, height: 80 };

    await page.route("**/api/health", (r) => r.fulfill({ json: allWell }));
    await page.goto("/");
    await expect(icon).toBeVisible();
    await expect(icon).toHaveAccessibleName("Health");
    const quiet = await stroke();
    await page.screenshot({ path: join(shots, `health-icon-ok-${s.name}.png`), clip: header });

    await page.unroute("**/api/health");
    await page.goto("/");
    await expect(icon).toHaveAccessibleName(/needs attention/);
    expect(await stroke()).not.toBe(quiet);
    await page.screenshot({ path: join(shots, `health-icon-failing-${s.name}.png`), clip: header });

    await icon.click();
    await expect(page.getByRole("heading", { name: "Health" })).toBeVisible();
    await page.waitForTimeout(400);
    await page.screenshot({ path: join(shots, `health-page-${s.name}.png`) });
    await page.keyboard.press("Escape");
    await expect(page.getByRole("searchbox")).toBeVisible();

    await page.goto("/health");
    await expect(page.getByRole("heading", { name: "Health" })).toBeVisible();
  });
}

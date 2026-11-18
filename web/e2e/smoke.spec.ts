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

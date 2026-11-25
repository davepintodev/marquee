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

async function openApp(page: Page) {
  // A navigation that only changes the hash would keep the old page's state.
  await page.goto("about:blank");
  await page.goto("/#/grid");
  await expect(page.locator(".cell img").first()).toBeVisible();
}

async function wallFrames(page: Page) {
  await expect(page.locator(".ptile[data-shape] img").first()).toBeVisible();
  return page.locator(".ptile[data-shape] .ptile-frame").evaluateAll((els) =>
    els.map((el) => {
      const r = el.getBoundingClientRect();
      return { x: r.x, y: r.y, w: r.width, h: r.height, shape: (el.parentElement as HTMLElement).dataset.shape! };
    }),
  );
}

for (const s of screens) {
  test(`the front is posters and the words on ${s.name}: a 10px margin at every edge, even 10px gaps, nothing else on it`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await page.goto("/");
    const frames = await wallFrames(page);

    expect(frames.length).toBeGreaterThan(4);
    expect(Math.min(...frames.map((f) => f.x))).toBe(GAP);
    expect(Math.min(...frames.map((f) => f.y))).toBe(GAP);
    expect(Math.max(...frames.map((f) => f.x + f.w))).toBe(s.width - GAP);
    expect(Math.max(...frames.map((f) => f.y + f.h))).toBe(s.height - GAP);

    for (const a of frames) {
      for (const b of frames) {
        const overlapY = Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y);
        const gapX = b.x - (a.x + a.w);
        if (overlapY > 0 && gapX >= 0 && gapX < GAP + 4) expect(gapX, `gap between ${a.x},${a.y} and ${b.x},${b.y}`).toBe(GAP);
        const overlapX = Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x);
        const gapY = b.y - (a.y + a.h);
        if (overlapX > 0 && gapY >= 0 && gapY < GAP + 4) expect(gapY, `gap between ${a.x},${a.y} and ${b.x},${b.y}`).toBe(GAP);
      }
    }
    // The poster above the arrow is half a tile, so it is the one backdrop that may be off 16:9.
    const off = frames.filter((f) => {
      const r = f.w / f.h / (f.shape === "portrait" ? 2 / 3 : 16 / 9);
      return r < 0.8 || r > 1.25;
    });
    expect(off.filter((f) => f.shape === "portrait")).toEqual([]);
    expect(off.length).toBeLessThanOrEqual(1);
    expect(new Set(frames.map((f) => f.shape))).toEqual(new Set(["portrait", "wide"]));
    const broken = await page.locator(".ptile img").evaluateAll((imgs) => imgs.filter((i) => !(i as HTMLImageElement).complete || (i as HTMLImageElement).naturalWidth === 0).length);
    expect(broken).toBe(0);

    await expect(page.locator(".front").locator(".nav, .nav-link, .badge, .meter, .chips, .state-line, input")).toHaveCount(0);
    await expect(page.locator(".top")).toHaveCSS("opacity", "0");
    await page.screenshot({ path: join(shots, `wall-${s.name}.png`) });
  });

  test(`the words and the arrow sit in empty slots on the plain page, inside the screen on ${s.name}`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await page.goto("/");
    await wallFrames(page);
    const words = page.getByRole("button", { name: "What are we watching tonight?" });
    const arrow = page.locator(".slot-arrow");
    await expect(words).toBeVisible();
    await expect(words.locator("em")).toHaveText("tonight?");

    const text = (await page.locator(".slot-words").boundingBox())!;
    const chevron = (await arrow.locator("svg").boundingBox())!;
    expect(text.x).toBeGreaterThanOrEqual(0);
    expect(text.x + text.width).toBeLessThanOrEqual(s.width);
    expect(text.y).toBeGreaterThanOrEqual(0);
    expect(text.y + text.height).toBeLessThanOrEqual(s.height);
    expect(Math.abs(text.y + text.height / 2 - s.height / 2)).toBeLessThan(s.height * 0.3);
    expect(chevron.y).toBeGreaterThan(text.y + text.height / 2);
    expect(chevron.y + chevron.height).toBeLessThanOrEqual(s.height);
    // The slots are not clipped: the text's own box fits what the words need.
    expect(await page.locator(".slot-words").evaluate((el) => el.scrollWidth <= el.clientWidth + 1 && el.scrollHeight <= el.clientHeight + 1)).toBe(true);

    for (const slot of [words, arrow]) {
      await expect(slot).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
      await expect(slot).toHaveCSS("border-top-width", "0px");
      await expect(slot).toHaveCSS("box-shadow", "none");
      await expect(slot).toHaveCSS("backdrop-filter", "none");
    }
    await expect(page.locator(".slot-arrow svg")).toHaveCSS("stroke-width", "1.5px");
  });
}

test("on a phone the front does not scroll sideways or down", async ({ page }) => {
  await page.setViewportSize({ width: screens[0].width, height: screens[0].height });
  await page.goto("/");
  await wallFrames(page);
  const over = await page.evaluate(() => ({ x: document.documentElement.scrollWidth - innerWidth, y: document.documentElement.scrollHeight - innerHeight, bx: document.body.scrollWidth - innerWidth }));
  expect(over.x).toBeLessThanOrEqual(0);
  expect(over.y).toBeLessThanOrEqual(0);
  expect(over.bx).toBeLessThanOrEqual(0);
});

test("the words take the colour of the poster under the pointer, and go back to rest when it leaves", async ({ page }) => {
  await page.setViewportSize({ width: screens[1].width, height: screens[1].height });
  await page.goto("/");
  await wallFrames(page);
  const slot = page.locator(".slot-text");
  await expect.poll(() => slot.evaluate((el) => el.style.getPropertyValue("--th"))).not.toBe("");
  const rest = await slot.evaluate((el) => el.style.getPropertyValue("--th"));
  const hues = new Set<string>([rest]);

  const frames = page.locator(".ptile-frame");
  for (let i = 0; i < (await frames.count()); i += 3) {
    const box = await frames.nth(i).boundingBox();
    if (!box || box.x < 0 || box.x + box.width > screens[1].width || box.y < 0 || box.y + box.height > screens[1].height) continue;
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.waitForTimeout(150);
    hues.add(await slot.evaluate((el) => el.style.getPropertyValue("--th")));
  }
  expect(hues.size, "different posters give the words different hues").toBeGreaterThan(2);

  await page.mouse.move(screens[1].width / 2, screens[1].height / 2);
  await page.mouse.move(-5, -5);
  await expect.poll(() => slot.evaluate((el) => el.style.getPropertyValue("--th"))).toBe(rest);
});

test("light mode darkens the words so they read against the page", async ({ browser }) => {
  const ctx = await browser.newContext({ colorScheme: "light", viewport: { width: screens[1].width, height: screens[1].height } });
  const page = await ctx.newPage();
  await page.goto("/");
  await wallFrames(page);
  await expect.poll(() => page.locator(".slot-text").evaluate((el) => el.style.getPropertyValue("--th"))).not.toBe("");
  const ratio = await page.evaluate(() => {
    const rgb = (c: string) => (c.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number);
    const lum = ([r, g, b]: number[]) => {
      const f = (v: number) => (v / 255 <= 0.03928 ? v / 255 / 12.92 : ((v / 255 + 0.055) / 1.055) ** 2.4);
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    const em = document.querySelector(".slot-words em")!;
    const a = lum(rgb(getComputedStyle(em).color));
    const b = lum(rgb(getComputedStyle(document.body).backgroundColor));
    return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
  });
  expect(ratio).toBeGreaterThanOrEqual(4.5);
  await page.screenshot({ path: join(shots, "wall-light.png") });
  await ctx.close();
});

test("with reduced motion the words change colour at once and the arrow does not blink", async ({ browser }) => {
  const ctx = await browser.newContext({ reducedMotion: "reduce", viewport: { width: screens[1].width, height: screens[1].height } });
  const page = await ctx.newPage();
  await page.goto("/");
  await wallFrames(page);
  await expect(page.locator(".slot-arrow svg")).toHaveCSS("animation-name", "none");
  await expect(page.locator(".slot-words em")).toHaveCSS("transition-duration", "1e-05s");
  await expect(page.locator(".front")).toHaveCSS("transition-delay", "0s");
  await page.keyboard.press("Enter");
  await expect(page.locator(".app")).toHaveAttribute("data-screen", "grid");
  await expect(page.locator(".front")).toHaveCSS("transition-delay", "0s");
  await expect(page.locator(".top")).toHaveCSS("transition-delay", "0s");
  await expect(page.locator(".tiles > li").first()).toHaveCSS("transition-delay", "0s");
  await ctx.close();
});

test("the wall opens the app by the words, the arrow, Enter or a letter; Esc and the back button return to it", async ({ page }) => {
  await page.setViewportSize({ width: screens[1].width, height: screens[1].height });
  const app = page.locator(".app");
  const wall = async () => {
    await page.goto("/");
    await wallFrames(page);
    await expect(app).toHaveAttribute("data-screen", "front");
  };

  await wall();
  await page.getByRole("button", { name: "What are we watching tonight?" }).click();
  await expect(app).toHaveAttribute("data-screen", "grid");
  await expect(page).toHaveURL(/#\/grid$/);
  await expect(page.getByRole("searchbox")).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(app).toHaveAttribute("data-screen", "front");

  await page.locator(".slot-arrow").click();
  await expect(app).toHaveAttribute("data-screen", "grid");
  await page.goBack();
  await expect(app).toHaveAttribute("data-screen", "front");

  await page.keyboard.press("Enter");
  await expect(app).toHaveAttribute("data-screen", "grid");
  await expect(page.getByRole("searchbox")).toHaveValue("");
  await page.keyboard.press("Escape");
  await expect(app).toHaveAttribute("data-screen", "front");

  await page.keyboard.type("dune");
  await expect(app).toHaveAttribute("data-screen", "grid");
  await expect(page.getByRole("searchbox")).toHaveValue("dune");
  await expect(page.getByRole("list", { name: "Results" }).getByRole("listitem").first()).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("searchbox")).toHaveValue("");
  await expect(app).toHaveAttribute("data-screen", "grid");
  await page.keyboard.press("Escape");
  await expect(app).toHaveAttribute("data-screen", "front");
  await page.screenshot({ path: join(shots, "wall-back-laptop.png") });
});

test("every control on the front and in the app is reachable by keyboard", async ({ page }) => {
  await page.setViewportSize({ width: screens[1].width, height: screens[1].height });
  await page.goto("/");
  await wallFrames(page);
  const seen = new Set<string>();
  for (let i = 0; i < 80; i++) {
    await page.keyboard.press("Tab");
    seen.add(await page.evaluate(() => document.activeElement?.getAttribute("aria-label") ?? document.activeElement?.tagName ?? ""));
  }
  expect(seen.has("What are we watching tonight?")).toBe(true);
  expect([...seen].some((n) => /\(\d{4}\)/.test(n))).toBe(true);

  await openApp(page);
  const inApp = new Set<string>();
  for (let i = 0; i < 13; i++) {
    await page.keyboard.press("Tab");
    inApp.add(await page.evaluate(() => document.activeElement?.textContent?.trim().split(/\d/)[0] || document.activeElement?.getAttribute("aria-label") || ""));
  }
  for (const want of ["Activity", "Health", "Library", "Trending", "Downloading", "Missing", "All", "Movies", "Series"]) expect([...inApp].some((n) => n.startsWith(want)), want).toBe(true);
});

for (const s of screens.slice(0, 2)) {
  test(`the app screen is an even 2:3 grid with filter chips, a state bar and quiet header links on ${s.name}`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await openApp(page);

    const arts = await page.locator(".cell-art").evaluateAll((els) => els.map((el) => el.getBoundingClientRect()).map((r) => ({ x: r.x, w: r.width, h: r.height, r: r.right })));
    expect(arts.length).toBeGreaterThan(4);
    for (const a of arts) expect(a.h / a.w, "2:3 poster").toBeCloseTo(1.5, 1);
    expect(new Set(arts.map((a) => Math.round(a.w))).size, "one even poster width").toBeLessThanOrEqual(1);
    expect(Math.max(...arts.map((a) => a.r))).toBeLessThanOrEqual(s.width);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(0);

    const chips = page.getByRole("group", { name: "Filter the library" }).getByRole("button");
    await expect(chips).toHaveText([/^Library\d+$/, /^Trending\d+$/, /^Downloading\d+$/, /^Missing\d+$/]);
    await expect(chips.first()).toHaveAttribute("aria-pressed", "true");
    await expect(page.locator(".meter").first()).toBeVisible();
    await expect(page.locator(".nav-link", { hasText: "Activity" })).toBeVisible();
    await expect(page.locator(".nav-link", { hasText: "Health" })).toBeVisible();
    await expect(page.locator(".nav-link").first()).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
    await page.waitForTimeout(900);
    await page.screenshot({ path: join(shots, `grid-${s.name}.png`) });

    const kinds = page.getByRole("group", { name: "Movies and series" }).getByRole("button");
    await expect(kinds).toHaveText([/^All\d+$/, /^Movies\d+$/, /^Series\d+$/]);
    await expect(kinds.first()).toHaveAttribute("aria-pressed", "true");
    await kinds.nth(1).click();
    await expect(kinds.nth(1)).toHaveAttribute("aria-pressed", "true");
    for (const label of await page.locator(".cell").evaluateAll((els) => els.map((el) => el.getAttribute("aria-label") ?? ""))) expect(label).toMatch(/\(\d{4}\)/);

    for (const edge of [chips.last(), kinds.last()]) {
      const box = await edge.boundingBox();
      expect(box!.x + box!.width, "the last control stays on screen").toBeLessThanOrEqual(s.width);
    }
    const strip = await page.getByRole("group", { name: "Filter the library" }).evaluate((el) => el.scrollWidth - el.clientWidth);
    expect(strip, "the four chips fit without scrolling").toBeLessThanOrEqual(0);
    await chips.nth(2).click();
    await expect(page.getByRole("group", { name: "Movies and series" })).toHaveCount(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(0);
  });
}

const inLibrary = /Downloaded|missing|Wanted|Searching|Not found|Downloading|Stalled|Caching/i;

for (const s of screens.slice(0, 2)) {
  test(`Library and Trending are separate sections that each keep their own Movies and Series choice on ${s.name}`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await openApp(page);
    const strip = page.getByRole("group", { name: "Filter the library" });
    const library = page.getByRole("list", { name: "Library titles" });
    const trending = page.getByRole("list", { name: "Trending titles" });
    for (const label of await library.locator(".cell").evaluateAll((els) => els.map((el) => el.getAttribute("aria-label") ?? ""))) expect(label).toMatch(inLibrary);
    await expect(trending).toHaveCount(0);

    const kinds = page.getByRole("group", { name: "Movies and series" });
    await kinds.getByRole("button", { name: /^Series/ }).click();
    await strip.getByRole("button", { name: /^Trending/ }).click();

    await expect(library).toHaveCount(0);
    await expect(strip.getByRole("button", { name: /^Trending/ })).toHaveAttribute("aria-pressed", "true");
    await expect(kinds.getByRole("button", { name: /^All/ })).toHaveAttribute("aria-pressed", "true");
    await kinds.getByRole("button", { name: /^Movies/ }).click();
    const names = await trending.locator(".cell").evaluateAll((els) => els.map((el) => el.getAttribute("aria-label") ?? ""));
    expect(names.length).toBeGreaterThan(0);
    for (const label of names) expect(label).not.toMatch(inLibrary);
    for (const label of names) expect(label).toMatch(/\(\d{4}\)/);
    await page.waitForTimeout(900);
    await page.screenshot({ path: join(shots, `trending-${s.name}.png`) });
    expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(0);

    await strip.getByRole("button", { name: /^Library/ }).click();
    await expect(library).toBeVisible();
    await expect(trending).toHaveCount(0);
    await expect(kinds.getByRole("button", { name: /^Series/ })).toHaveAttribute("aria-pressed", "true");
  });
}

test("a trending title opens its page with Add & monitor and no Remove", async ({ page }) => {
  await page.setViewportSize({ width: screens[1].width, height: screens[1].height });
  await openApp(page);
  await page.getByRole("button", { name: /^Trending/ }).click();
  await page.getByRole("list", { name: "Trending titles" }).locator(".cell").first().click();
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "Add & monitor" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Remove…" })).toHaveCount(0);
});

for (const s of screens.slice(0, 2)) {
  test(`typing in the app shows results in the same grid on ${s.name}; Esc brings the library back`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await openApp(page);
    const bar = page.getByRole("searchbox");
    await expect(bar).toBeFocused();

    await bar.pressSequentially("dune", { delay: 120 });

    await expect(page.locator(".app")).toHaveAttribute("data-mode", "search");
    const results = page.getByRole("list", { name: "Results" });
    const cards = results.getByRole("listitem");
    await expect(cards.first()).toBeVisible();
    await expect(results.getByText(/\d{4} · (Movie|Series)/).first()).toBeVisible();
    expect(await cards.count()).toBeGreaterThanOrEqual(2);
    const art = await results.locator(".cell-art").first().boundingBox();
    expect(art!.height / art!.width).toBeCloseTo(1.5, 1);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(0);
    await page.waitForTimeout(600);
    await page.screenshot({ path: join(shots, `search-${s.name}.png`) });

    await page.keyboard.press("Escape");

    await expect(page.locator(".app")).toHaveAttribute("data-mode", "library");
    await expect(bar).toHaveValue("");
    await expect(bar).toBeFocused();
    await page.waitForTimeout(600);
    await page.screenshot({ path: join(shots, `search-esc-${s.name}.png`) });
  });
}

test("a search that finds nothing says so", async ({ page }) => {
  await openApp(page);
  await page.getByRole("searchbox").fill("qqqzzz");
  await expect(page.getByText("Nothing found for “qqqzzz”.")).toBeVisible();
});

test("dark for a dark system, light when the system asks for it", async ({ browser }) => {
  const bg = async (colorScheme: "dark" | "light") => {
    const ctx = await browser.newContext({ colorScheme });
    const page = await ctx.newPage();
    await page.goto("/");
    await expect(page.locator(".ptile[data-shape] img").first()).toBeVisible();
    const color = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
    await ctx.close();
    return color;
  };
  expect(await bg("dark")).toBe("rgb(11, 11, 15)");
  expect(await bg("light")).toBe("rgb(244, 244, 247)");
});

// Each screen adds a different trending title: the fake keeps what was added for the whole run.
const pageTitles = [
  { ...screens[0], title: "Severance" },
  { ...screens[1], title: "Andor" },
];

async function noSideScroll(page: Page, width: number) {
  const overflow = await page.evaluate(() => {
    const el = document.querySelector(".ts, .page-layer")!;
    return el.scrollWidth - el.clientWidth;
  });
  expect(overflow, `page is ${width}px wide and must not scroll sideways`).toBeLessThanOrEqual(0);
}

for (const s of pageTitles) {
  test(`a result opens the title page on ${s.name}, adds on Add & monitor and then reads Search now`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await openApp(page);
    const bar = page.getByRole("searchbox");
    await bar.pressSequentially(s.title.toLowerCase(), { delay: 80 });
    await page.getByRole("list", { name: "Results" }).getByRole("button", { name: new RegExp(s.title) }).click();

    const title = page.getByRole("main");
    await expect(title.getByRole("heading", { name: s.title, level: 1 })).toBeVisible();
    await expect(title.getByText(/2022 · Series/)).toBeVisible();
    await expect(title.locator(".ts-poster img")).toBeVisible();
    await expect(title.getByRole("button", { name: "Add & monitor" })).toBeVisible();
    await expect(title.getByText("Seasons and episodes show up once the series is added.")).toBeVisible();
    await page.waitForTimeout(500);
    await noSideScroll(page, s.width);
    await page.screenshot({ path: join(shots, `title-${s.name}.png`) });

    await title.getByRole("button", { name: "Add & monitor" }).click();

    await expect(page.getByRole("status")).toContainText(s.title);
    await expect(title.getByRole("button", { name: "Search now", exact: true })).toBeVisible();
    await page.waitForTimeout(500);
    await page.screenshot({ path: join(shots, `title-added-${s.name}.png`) });

    await page.keyboard.press("Escape");
    await expect(title).toBeHidden();
    await expect(bar).toHaveValue(s.title.toLowerCase());
  });
}

async function tileOnScreen(page: Page, name: RegExp) {
  await expect(page.locator(".ptile[data-shape] img").first()).toBeVisible();
  const tiles = page.locator(".ptile").and(page.getByRole("button", { name }));
  const view = page.viewportSize()!;
  for (let i = 0; i < (await tiles.count()); i++) {
    const b = await tiles.nth(i).boundingBox();
    if (b && b.x > 0 && b.y > 0 && b.x + b.width < view.width && b.y + b.height < view.height) return tiles.nth(i);
  }
  throw new Error(`no poster named ${name} is fully on screen`);
}

test("a wall poster opens the title page and a title already in the library reads Search now", async ({ page }) => {
  await page.setViewportSize({ width: screens[1].width, height: screens[1].height });
  await page.goto("/");

  await (await tileOnScreen(page, inLibrary)).click();

  const title = page.getByRole("main");
  await expect(title.getByRole("button", { name: "Search now", exact: true })).toBeVisible();
  await expect(title.getByRole("button", { name: "Add & monitor" })).toHaveCount(0);
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(shots, "title-library-laptop.png") });
  await title.getByRole("button", { name: "Back" }).click();
  await expect(title).toBeHidden();
  await expect(page.locator(".app")).toHaveAttribute("data-screen", "grid");
});


for (const s of screens.slice(0, 2)) {
  test(`a series' page shows its seasons, episodes and history in words on ${s.name}`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await page.goto("/#/title/series-1408");

    await expect(page.getByRole("heading", { name: "House", level: 1 })).toBeVisible();
    await page.getByRole("tab", { name: /^Season 4,/ }).click();
    const season4 = page.getByRole("list", { name: "Season 4 episodes" });
    await expect(season4.getByRole("listitem")).toHaveCount(16);
    await expect(season4.getByText("Wanted").first()).toBeVisible();
    const big = season4.getByRole("listitem").filter({ hasText: "Big Baby" });
    await expect(big.getByText("Not found")).toBeVisible();
    await expect(big.getByText(/nothing found · next try in \d+h/)).toBeVisible();
    await expect(season4.getByText("Downloaded").first()).toBeVisible();
    await expect(page.getByRole("button", { name: /^Season 4/ })).toContainText("1 of 16");
    const overview = page.locator(".ts-overview");
    await expect(overview).toBeVisible();
    const ob = (await overview.boundingBox())!;
    expect(ob.width, "the overview has a readable line length").toBeGreaterThan(Math.min(240, s.width - 60));
    expect(ob.x + ob.width).toBeLessThanOrEqual(s.width);

    const tip = page.getByRole("tooltip");
    await page.getByRole("button", { name: "About these attempts" }).click();
    await expect(tip).toHaveAttribute("data-open", "true");
    await expect(tip).toContainText(/Tried 3 releases/);
    await page.keyboard.press("Escape");
    await expect(tip).not.toHaveAttribute("data-open", "true");
    await expect(page.getByRole("main")).toBeVisible();

    await page.getByRole("button", { name: "History" }).click();
    await expect(page.getByRole("list", { name: "History" }).getByText("Stalled").first()).toBeVisible();
    await noSideScroll(page, s.width);
    await page.waitForTimeout(500);
    await page.screenshot({ path: join(shots, `title-series-${s.name}.png`) });
    await page.locator(".ts").evaluate((el) => el.scrollTo(0, el.scrollHeight));
    await page.waitForTimeout(300);
    await page.screenshot({ path: join(shots, `title-series-${s.name}-scrolled.png`) });
  });
}

const waits = [
  { ...screens[0], id: "series-226745", name: "Camp Snoopy", tile: /^Camp Snoopy \(2024\), Wanted$/, state: "wanted", line: /Next search in [23]h/ },
  { ...screens[1], id: "movie-335984", name: "Blade Runner 2049", tile: /^Blade Runner 2049 \(2017\), Not found$/, state: "not_found", line: /Nothing found · next try in [56]h/ },
];

for (const w of waits) {
  test(`a title waiting for its search says so beside its facts, and Search now shows Searching then Nothing found on ${w.name}`, async ({ page }) => {
    await page.setViewportSize({ width: w.width, height: w.height });
    await openApp(page);
    await expect(page.getByRole("list", { name: "Library titles" }).getByRole("button", { name: w.tile })).toBeVisible();

    await page.goto(`/#/title/${w.id}`);
    await expect(page.getByRole("heading", { name: w.name, level: 1 })).toBeVisible();
    const line = page.locator(".ts-facts .ts-wait");
    await expect(line).toHaveAttribute("data-state", w.state);
    await expect(line).toContainText(w.line);
    await expect(page.locator(".ts-hist-head")).not.toContainText(/next|Search now/i);
    await page.screenshot({ path: join(shots, `title-${w.state}-${w.name}.png`) });
    await noSideScroll(page, w.width);

    const searchNow = page.getByRole("main").getByRole("button", { name: "Search now", exact: true });
    await expect(searchNow).toHaveCount(1);
    await searchNow.click();
    await expect(page.getByRole("status")).toContainText(`Searching for “${w.name}” now.`);
    await expect(line).toHaveAttribute("data-state", "searching");
    await expect(line).toContainText("Searching now");
    await expect(searchNow).toHaveAttribute("aria-disabled", "true");
    if (w.id.startsWith("series-")) {
      const eps = page.getByRole("list", { name: /episodes$/ });
      await expect(eps.getByText("Searching", { exact: true })).toHaveCount(1);
      await expect(eps.getByText("Wanted", { exact: true })).toHaveCount(1);
    }
    await page.screenshot({ path: join(shots, `title-searching-${w.name}.png`) });
    await expect(line).toHaveAttribute("data-state", "not_found", { timeout: 15_000 });
    await expect(line).toContainText(/Nothing found · next try in 6h/);
  });
}

test("a series page: Search automatically and Unmonitor on an episode change it in place, Remove asks first", async ({ page }) => {
  await page.setViewportSize({ width: screens[1].width, height: screens[1].height });
  await page.goto("/#/title/series-1408");
  await page.getByRole("tab", { name: /^Season 4,/ }).click();
  const season4 = page.getByRole("list", { name: "Season 4 episodes" });
  const big = season4.getByRole("listitem").filter({ hasText: "Big Baby" });

  await big.getByRole("button", { name: "Search automatically for Big Baby" }).click();
  await expect(big.getByText("Searching").first()).toBeVisible();
  await big.getByRole("button", { name: /^Monitored\. Stop monitoring Big Baby/ }).click();
  await expect(big.getByRole("button", { name: /^Not monitored\. Monitor Big Baby/ })).toBeVisible();
  await big.getByRole("button", { name: /^Not monitored\. Monitor Big Baby/ }).click();
  await expect(big.getByRole("button", { name: /^Monitored\. Stop monitoring Big Baby/ })).toBeVisible();

  const remove = page.getByRole("group", { name: "Remove from marquee" });
  await remove.getByRole("button", { name: /^Remove/ }).click();
  await expect(remove.getByText(/^Remove “House”\?$/)).toBeVisible();
  const keep = remove.getByRole("button", { name: "Keep it" });
  await expect(keep).toBeFocused();
  await page.waitForTimeout(300);
  await page.screenshot({ path: join(shots, "title-remove-laptop.png") });
  await page.keyboard.press("Escape");
  await expect(remove.getByRole("button", { name: /^Remove…/ })).toBeVisible();
  await expect(page.getByRole("main")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator(".app")).toHaveAttribute("data-mode", "library");
});

const retries = [
  { ...screens[0], retry: "Retry Arrival", queued: "Arrival" },
  { ...screens[1], retry: "Retry Blade Runner 2049", queued: "Blade Runner 2049" },
];

for (const s of retries) {
  test(`Activity shows the queue and history, progress moves live and Retry puts a failure back in the queue on ${s.name}`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    await openApp(page);
    await page.getByRole("link", { name: "Activity" }).click();

    const waiting = page.getByRole("list", { name: "Waiting" });
    const history = page.getByRole("region", { name: "History" });
    await expect(page.getByRole("heading", { name: "Downloading" })).toBeVisible();
    await expect(history.getByText("Fake file").first()).toBeVisible();
    await expect(history.getByText(/\.exe file, not a video/).first()).toBeVisible();
    await noSideScroll(page, s.width);

    const progress = page.getByRole("progressbar", { name: "Dune: Part Two progress" });
    const first = await progress.getAttribute("aria-valuenow");
    await page.waitForTimeout(500);
    await page.screenshot({ path: join(shots, `activity-${s.name}.png`) });
    await expect.poll(() => progress.getAttribute("aria-valuenow"), { timeout: 15_000 }).not.toBe(first);

    await history.getByRole("button", { name: s.retry }).click();

    await expect(history.getByRole("button", { name: s.retry })).toHaveCount(0);
    await expect(waiting.getByRole("listitem").filter({ hasText: s.queued }).getByText("Searching")).toBeVisible();
    await expect(page.getByRole("status")).toContainText(s.queued);
    await page.waitForTimeout(500);
    await page.screenshot({ path: join(shots, `activity-retried-${s.name}.png`) });

    await page.keyboard.press("Escape");
    await expect(page.locator(".app")).toHaveAttribute("data-mode", "library");
  });
}

test("a library title opens its page and Back returns to the grid", async ({ page }) => {
  await page.setViewportSize({ width: screens[1].width, height: screens[1].height });
  await openApp(page);
  await page.locator(".cell").first().click();
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await page.getByRole("button", { name: "Back" }).click();
  await expect(page.getByRole("main")).toHaveCount(0);
  await expect(page.locator(".app")).toHaveAttribute("data-mode", "library");
});

test("retrying a failed series episode puts it in the queue", async ({ page }) => {
  await page.setViewportSize({ width: screens[1].width, height: screens[1].height });
  await page.goto("/#/activity");
  const history = page.getByRole("region", { name: "History" });

  await history.getByRole("button", { name: "Retry House" }).click();

  const waiting = page.getByRole("list", { name: "Waiting" });
  await expect(waiting.getByRole("listitem").filter({ hasText: "House" }).getByRole("link")).toHaveAttribute("title", /S04E02/);
});

test("the title page and Activity read well in the light theme", async ({ browser }) => {
  const ctx = await browser.newContext({ colorScheme: "light", viewport: { width: screens[1].width, height: screens[1].height } });
  const page = await ctx.newPage();
  await page.goto("/#/title/series-1408");
  await expect(page.getByRole("heading", { name: "House", level: 1 })).toBeVisible();
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(shots, "title-light.png") });
  await page.goto("/#/activity");
  await expect(page.getByRole("heading", { name: "Downloading" })).toBeVisible();
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(shots, "activity-light.png") });
  await ctx.close();
});

const allWell = { status: "ok", dryRun: false, checks: [{ id: "tmdb", name: "TMDB", status: "ok", message: "Key accepted." }], libraryTotals: { movies: 0, episodes: 0 }, libraryProblems: [] };

for (const s of screens.slice(0, 2)) {
  test(`the Health link has no dot when all is well, a red dot when a check fails, and opens Health on ${s.name}`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    const pill = page.locator(".nav-link", { hasText: "Health" });
    const dot = pill.locator("i");
    const header = { x: s.width - 320, y: 0, width: 320, height: 80 };

    await page.route("**/api/health", (r) => r.fulfill({ json: allWell }));
    await openApp(page);
    await expect(pill).toBeVisible();
    await expect(pill).toHaveAccessibleName("Health");
    await expect(dot).toHaveCount(0);
    await page.screenshot({ path: join(shots, `health-link-ok-${s.name}.png`), clip: header });

    await page.unroute("**/api/health");
    await openApp(page);
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
    await page.screenshot({ path: join(shots, `health-link-failing-${s.name}.png`), clip: header });

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

async function openTitle(page: Page, query: string, name: RegExp) {
  await openApp(page);
  await page.getByRole("searchbox").pressSequentially(query, { delay: 60 });
  await page.getByRole("list", { name: "Results" }).getByRole("button", { name }).click();
  const title = page.getByRole("main");
  await expect(title.getByRole("heading", { level: 1 })).toBeVisible();
  // Clicking while the page is still fading in makes the browser scroll to reach it.
  await page.waitForTimeout(500);
  return title;
}

async function openReleases(page: Page, query: string, name: RegExp) {
  const title = await openTitle(page, query, name);
  await title.getByRole("button", { name: "Search releases" }).click();
  return title;
}

for (const s of screens.slice(0, 2)) {
  test(`a movie's releases are listed in marquee's order with quality, size, source and cached on ${s.name}`, async ({ page }) => {
    await page.setViewportSize({ width: s.width, height: s.height });
    const title = await openReleases(page, "dune", /Dune[\s\S]*2021/);

    const rows = title.getByRole("list", { name: /^Releases for/ }).getByRole("listitem");
    await expect(rows).toHaveCount(3);
    await expect(title.getByText("in marquee’s order")).toBeVisible();
    await expect(rows.first()).toContainText("Blade.Runner.2049.2017.1080p.BluRay.x264-SPARKS");
    await expect(rows.first()).toContainText("1080p");
    await expect(rows.first()).toContainText("12 GB");
    await expect(rows.first()).toContainText("Torrentio");
    await expect(rows.first()).toContainText("Cached");
    await expect(rows.first()).toContainText("marquee’s pick");
    await expect(rows.nth(1)).not.toContainText("marquee’s pick");
    await title.getByRole("button", { name: "Show 4 more releases" }).click();
    await expect(rows).toHaveCount(7);
    await expect(title.getByRole("group", { name: "Filter by resolution" }).getByRole("button", { name: "2160p" })).toHaveText("2160p");
    await noSideScroll(page, s.width);
    await page.waitForTimeout(500);
    await page.screenshot({ path: join(shots, `releases-movie-${s.name}.png`) });

    await title.getByRole("button", { name: /^Show \d+ dropped releases$/ }).click();
    const dropped = title.getByRole("list", { name: "Dropped releases" });
    await expect(dropped.getByText("a .exe file, not a video")).toBeVisible();
    await expect(dropped.getByRole("listitem").filter({ hasText: "a .exe file" }).getByRole("button")).toHaveCount(0);
    await expect(dropped.getByRole("button", { name: /^Pick anyway/ })).toHaveCount(4);
    await noSideScroll(page, s.width);
    await page.screenshot({ path: join(shots, `releases-movie-dropped-${s.name}.png`) });

    await title.getByRole("button", { name: "2160p", exact: true }).click();
    await expect(rows).toHaveCount(2);
  });
}

test("picking a release queues it: the toast confirms, the row reads Queued and Activity shows the download", async ({ page }) => {
  await page.setViewportSize({ width: screens[1].width, height: screens[1].height });
  const title = await openReleases(page, "dune", /Dune[\s\S]*2021/);

  await title.getByRole("button", { name: "2160p", exact: true }).click();
  await title.getByRole("button", { name: "Pick Blade.Runner.2049.2017.2160p.WEB-DL" }).click();

  await expect(page.getByRole("status")).toContainText("Queued Blade.Runner.2049.2017.2160p.WEB-DL");
  await expect(title.getByText("Queued", { exact: true })).toBeVisible();
  await page.waitForTimeout(400);
  await page.screenshot({ path: join(shots, "releases-picked-laptop.png") });

  await page.keyboard.press("Escape");
  await page.getByRole("link", { name: "Activity" }).click();
  await expect(page.locator(".board-main").getByText(/^Dune$/)).toBeVisible();
  await page.waitForTimeout(400);
  await page.screenshot({ path: join(shots, "releases-activity-laptop.png") });
});

test("a series not in the library says to add it first and offers no episodes", async ({ page }) => {
  await page.setViewportSize({ width: screens[0].width, height: screens[0].height });
  const title = await openTitle(page, "mr. robot", /Mr\. Robot[\s\S]*2015/);

  await expect(title.getByText("Seasons and episodes show up once the series is added.")).toBeVisible();
  await expect(title.getByRole("tab")).toHaveCount(0);
  await expect(title.getByRole("button", { name: "Search releases" })).toHaveCount(0);
  await noSideScroll(page, screens[0].width);
  await page.screenshot({ path: join(shots, "title-series-add-first-phone.png") });
  await expect(title.getByRole("button", { name: "Add & monitor" })).toBeVisible();
});

test("a library series lists an episode's releases and a blocklisted one can be picked by hand on phone", async ({ page }) => {
  await page.setViewportSize({ width: screens[0].width, height: screens[0].height });
  const title = await openTitle(page, "camp snoopy", /Camp Snoopy[\s\S]*2024/);

  const second = title.getByRole("button", { name: /^S01E02 / });
  await second.click();
  await expect(second).toHaveAttribute("aria-expanded", "true");
  const list = title.getByRole("list", { name: /^Releases for/ });
  await expect(list.getByRole("listitem").first()).toBeVisible();
  await noSideScroll(page, screens[0].width);
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(shots, "releases-series-phone.png") });

  await title.getByRole("button", { name: /^Show \d+ dropped releases$/ }).click();
  const blocked = title.getByRole("list", { name: "Dropped releases" }).getByRole("listitem").filter({ hasText: "failed before" });
  await expect(blocked).toBeVisible();
  await blocked.scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(shots, "releases-blocklisted-phone.png") });
  await blocked.getByRole("button", { name: /^Pick anyway/ }).click();

  await expect(page.getByRole("status")).toContainText("Queued House.S01E02.480p.DVDRip.XviD");
  await expect(blocked.getByText("Queued", { exact: true })).toBeVisible();

  await page.keyboard.press("Escape");
  await expect(second).toHaveAttribute("aria-expanded", "false");
  await page.keyboard.press("Escape");
  await page.getByRole("link", { name: "Activity" }).click();
  await expect(page.locator(".board-main").getByText(/Camp Snoopy/).first()).toBeVisible();
});

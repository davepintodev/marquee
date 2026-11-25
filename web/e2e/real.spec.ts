import { expect, test } from "@playwright/test";

// The service behind this page is the real one, serving the same UI; only
// TMDB, AIOStreams, the debrid file server, TorBox and Jellyfin are fakes.
test("a title added from its page downloads, imports and reads Downloaded in the grid without a reload", async ({ page }) => {
  await page.goto("/#/grid");
  await page.getByRole("button", { name: /^Trending/ }).click();
  await expect(page.getByRole("button", { name: /^Arrival/ }).first()).toBeVisible();
  await page.getByRole("button", { name: /^Library/ }).click();

  await page.getByRole("searchbox").fill("dune");
  await page.getByRole("list", { name: "Results" }).getByRole("button", { name: /Dune/ }).click();
  const title = page.getByRole("main");
  await title.getByRole("button", { name: "Add & monitor" }).click();
  await expect(page.getByRole("status")).toContainText("Dune");
  await expect(title.getByRole("button", { name: "Search now", exact: true })).toBeVisible();

  await title.getByRole("button", { name: "Back" }).click();
  await expect(page.getByRole("main")).toHaveCount(0);
  await page.keyboard.press("Escape");

  const dune = page.getByRole("button", { name: /^Dune \(2021\)/ }).first();
  await expect(dune).toHaveAccessibleName(/Downloading \d+%/, { timeout: 30_000 });
  await expect(dune).toHaveAccessibleName("Dune (2021), Downloaded", { timeout: 60_000 });

  await page.getByRole("link", { name: "Activity" }).click();
  await expect(page.getByRole("region", { name: "History" }).getByText(/Imported Dune/)).toBeVisible();
});

test("a release picked by hand on the title page is queued and shows in Activity", async ({ page }) => {
  await page.goto("/#/grid");
  await page.getByRole("button", { name: /^Trending/ }).click();
  await page.getByRole("button", { name: /^Arrival/ }).first().click();
  const title = page.getByRole("main");
  await title.getByRole("button", { name: "Search releases" }).click();
  await title.getByRole("button", { name: "Pick Arrival.2016.1080p.WEB-DL.mkv" }).click();
  await expect(page.getByRole("status")).toContainText("Queued Arrival.2016.1080p.WEB-DL.mkv");

  await page.keyboard.press("Escape");
  await page.getByRole("link", { name: "Activity" }).click();
  await expect(page.locator(".board-main").getByRole("link", { name: "Arrival", exact: true })).toBeVisible();
});

test("a title with no cached release is fetched by TorBox: Caching on the wall, then Downloaded", async ({ page }) => {
  await page.goto("/#/grid");
  await page.getByRole("searchbox").fill("heat");
  await page.getByRole("list", { name: "Results" }).getByRole("button", { name: /Heat/ }).click();
  const title = page.getByRole("main");
  await title.getByRole("button", { name: "Add & monitor" }).click();
  await expect(page.getByRole("status")).toContainText("Heat");

  await title.getByRole("button", { name: "Back" }).click();
  await page.keyboard.press("Escape");

  const heat = page.getByRole("button", { name: /^Heat \(1995\)/ }).first();
  await expect(heat).toHaveAccessibleName(/Caching \d+%/, { timeout: 30_000 });
  await expect(heat).toHaveAccessibleName("Heat (1995), Downloaded", { timeout: 90_000 });
});

test("a downloaded title can change its quality profile, rescan, and be removed with its files", async ({ page }) => {
  await page.goto("/#/grid");
  await page.getByRole("button", { name: /^Dune \(2021\)/ }).first().click();
  const title = page.getByRole("main");

  const profile = title.getByRole("combobox", { name: "Quality profile" });
  await expect(profile).toHaveValue("tv-safe");
  await profile.selectOption("any-1080p");
  await expect(page.getByRole("status")).toContainText("now uses the any-1080p profile");

  await title.getByRole("button", { name: "Rescan", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("Rescanned “Dune”");

  await title.getByRole("button", { name: "Remove…" }).click();
  await title.getByLabel("Also delete files from disk").check();
  await title.getByRole("button", { name: /^Yes, remove “Dune” and delete its files/ }).click();
  await expect(page.getByRole("status")).toContainText("deleted its files from disk");
});

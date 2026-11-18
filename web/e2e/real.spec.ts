import { expect, test } from "@playwright/test";

// The service behind this page is the real one, serving the same UI; only
// TMDB, AIOStreams, the debrid file server and Jellyfin are fakes.
test("a title added through the popup downloads, imports and reads Downloaded on the wall without a reload", async ({ page }) => {
  await page.goto("/");
  const wall = page.getByRole("button", { name: /^Arrival/ }).first();
  await expect(wall).toBeVisible();

  await page.getByRole("searchbox").fill("dune");
  await page.getByRole("list", { name: "Results" }).getByRole("button", { name: /Dune/ }).click();
  const popup = page.getByRole("dialog");
  await popup.getByRole("button", { name: "Add & monitor" }).click();
  await expect(page.getByRole("status")).toContainText("Dune");
  await expect(popup.getByRole("button", { name: "Search now" })).toBeVisible();

  await page.keyboard.press("Escape");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);

  const dune = page.getByRole("button", { name: /^Dune \(2021\)/ }).first();
  await expect(dune).toHaveAccessibleName(/Downloading \d+%/, { timeout: 30_000 });
  await expect(dune).toHaveAccessibleName("Dune (2021), Downloaded", { timeout: 60_000 });

  await page.getByRole("link", { name: "Activity" }).click();
  await expect(page.getByRole("list", { name: "History" }).getByText(/Imported Dune/)).toBeVisible();
});

test("a release picked by hand in interactive search is queued and shows in Activity", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: /^Arrival/ }).first().click();
  const popup = page.getByRole("dialog");
  await expect(popup.getByRole("button", { name: "Interactive search" })).toBeEnabled();
  await popup.getByRole("button", { name: "Interactive search" }).click();
  await popup.getByRole("button", { name: "Pick Arrival.2016.1080p.WEB-DL.mkv" }).click();
  await expect(page.getByRole("status")).toContainText("Queued Arrival.2016.1080p.WEB-DL.mkv");

  await page.keyboard.press("Escape");
  await page.keyboard.press("Escape");
  await page.getByRole("link", { name: "Activity" }).click();
  await expect(page.getByRole("list", { name: "Queue" }).getByRole("listitem").filter({ hasText: "Arrival.2016.1080p.WEB-DL.mkv" })).toBeVisible();
});

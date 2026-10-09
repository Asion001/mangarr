import { test, expect } from "@playwright/test";

const longTitle = "Einz: The Laid-Off Cheat-Granting Mage Lives a Leisurely Life in the Border Territory While His Former Party Falls Apart Without Him";

test("on a phone, titles not in the library put their buttons under a clamped title", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en" })));
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["library.manage", "library.add"] } } });
    if (path.endsWith("/series/search")) return route.fulfill({ json: { page: 1, pageSize: 36, total: 0, totalSize: 0, languages: [], genres: [], items: [] } });
    if (path.endsWith("/series/lookup")) return route.fulfill({ json: { providers: 1, results: [{ moduleId: 2, provider: "anilist", id: "99", moduleName: "AniList", title: longTitle, year: 2024, format: "manga", status: "ongoing", description: "A mage is let go and moves to the border." }] } });
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    return route.fulfill({ json: [] });
  });

  await page.goto("/?q=einz");
  const title = page.getByText(longTitle);
  await expect(title).toBeVisible();
  const add = page.getByRole("button", { name: "Add…" });
  await expect(add).toBeVisible();
  const titleBox = (await title.boundingBox())!;
  const addBox = (await add.boundingBox())!;
  // the buttons sit below the text instead of squeezing it
  expect(addBox.y).toBeGreaterThan(titleBox.y + titleBox.height);
  // and the long title is cut to two lines
  const lineHeight = await title.evaluate((el) => parseFloat(getComputedStyle(el).lineHeight));
  expect(titleBox.height).toBeLessThanOrEqual(lineHeight * 2 + 1);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await page.screenshot({ path: test.info().outputPath("phone.png"), fullPage: true });
});

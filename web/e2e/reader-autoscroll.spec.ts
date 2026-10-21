import { expect, test, type Page } from "@playwright/test";

const image = (width: number, height: number) => `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}"><rect width="100%" height="100%" fill="#555"/></svg>`;

async function mock(page: Page, settings: Record<string, unknown>, pages: number) {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (/\/read\/chapters\/1\/pages\/\d+$/.test(path)) return route.fulfill({ contentType: "image/svg+xml", body: image(600, 900) });
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
    if (path.endsWith("/read/settings")) {
      return route.fulfill({ json: { defaults: { mode: "webtoon", showTapZones: false, ...settings }, series: null } });
    }
    if (path.endsWith("/read/chapters/1") && route.request().method() === "GET") {
      return route.fulfill({ json: {
        id: 1, seriesId: 1, seriesTitle: "Strip", number: "1", readingDirection: "webtoon", downloaded: true, canDownload: false,
        pages: Array.from({ length: pages }, (_, i) => ({ number: i + 1, mediaType: "image/svg+xml" })),
        progress: { page: 0, completed: false },
      } });
    }
    return route.fulfill({ json: [] });
  });
}

test("webtoon auto scroll runs from the bottom bar, pauses and changes speed", async ({ page }) => {
  await page.setViewportSize({ width: 400, height: 700 });
  await mock(page, { autoScrollSpeed: 10 }, 10);
  await page.goto("/read/1");
  await expect(page.locator('[data-page="1"]')).toBeVisible();
  // in a browser tab the strip scrolls the page itself
  const scroller = page.locator("html");

  await page.getByRole("button", { name: "Start auto scroll" }).click();
  await expect.poll(() => scroller.evaluate((el) => el.scrollTop)).toBeGreaterThan(200);

  // the bars hide; the pill pauses it and changes the speed
  const pill = page.getByRole("group", { name: "Auto scroll" });
  await expect(pill).toBeVisible();
  await pill.getByRole("button", { name: "Slower" }).click();
  // kept on this device for the series
  await expect.poll(() => page.evaluate(() => JSON.parse(localStorage.getItem("mangarr:reader:anonymous:0") ?? "{}").series?.["1"]?.autoScrollSpeed)).toBe(9);
  await pill.getByRole("button", { name: "Pause auto scroll" }).click();
  await expect(pill).toBeHidden();
  const stopped = await scroller.evaluate((el) => el.scrollTop);
  await page.waitForTimeout(400);
  expect(await scroller.evaluate((el) => el.scrollTop)).toBe(stopped);
});

test("webtoon auto scroll starts on open when set, and stops at the end", async ({ page }) => {
  await page.setViewportSize({ width: 400, height: 700 });
  await mock(page, { autoScrollSpeed: 10, autoScrollOnOpen: true }, 2);
  await page.goto("/read/1");
  // in a browser tab the strip scrolls the page itself
  const scroller = page.locator("html");
  await expect.poll(() => scroller.evaluate((el) => el.scrollTop + el.clientHeight >= el.scrollHeight - 2), { timeout: 15_000 }).toBe(true);
  // at the end it switches itself off
  await expect(page.getByRole("button", { name: "Start auto scroll" })).toBeAttached();
});

import { expect, test } from "@playwright/test";

const image = (width: number, height: number) => `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}"><rect width="100%" height="100%" fill="#555"/></svg>`;

test("in a browser tab the webtoon strip scrolls the page, and the reader paints the browser bars black", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 700 });
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (/\/read\/chapters\/1\/pages\/\d+$/.test(path)) return route.fulfill({ contentType: "image/svg+xml", body: image(600, 1800) });
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
    if (path.endsWith("/read/settings")) return route.fulfill({ json: { defaults: { mode: "webtoon", showTapZones: false }, series: null } });
    if (path.endsWith("/read/chapters/1") && route.request().method() === "GET") {
      return route.fulfill({ json: {
        id: 1, seriesId: 1, seriesTitle: "Strip", number: "1", readingDirection: "webtoon", downloaded: true, canDownload: false,
        pages: [1, 2, 3].map((number) => ({ number, mediaType: "image/svg+xml" })),
        progress: { page: 0, completed: false },
      } });
    }
    return route.fulfill({ json: [] });
  });

  await page.goto("/read/1");
  await expect(page.locator('[data-page="1"]')).toBeVisible();
  const themeColor = () => page.locator('meta[name="theme-color"]').getAttribute("content");
  expect(await themeColor()).toBe("#000000");
  expect(await page.evaluate(() => getComputedStyle(document.body).backgroundColor)).toBe("rgb(0, 0, 0)");

  await page.mouse.move(200, 350);
  await page.mouse.wheel(0, 1000);
  await expect.poll(() => page.evaluate(() => document.scrollingElement!.scrollTop)).toBeGreaterThan(500);
  // the bars hide on their own and come back on screen over the scrolled page
  const settings = page.getByRole("button", { name: "Reader settings" });
  await expect(settings).not.toBeInViewport();
  // and leave the page, so Safari doesn't paint its own bars in their colour over the strip
  await expect(page.locator("[data-reader-bar]")).toHaveCount(2);
  await expect(page.locator("[data-reader-bar]:visible")).toHaveCount(0);
  await page.keyboard.press("m");
  await expect(settings).toBeInViewport();

  // leaving the reader gives the app its own colors back
  await page.getByLabel("Back to the series").click();
  await expect.poll(themeColor).not.toBe("#000000");
  expect(await page.evaluate(() => document.body.style.backgroundColor)).toBe("");
});

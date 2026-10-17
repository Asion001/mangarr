import { expect, test } from "@playwright/test";

const image = (width: number, height: number) => `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}"><rect width="100%" height="100%" fill="#555"/></svg>`;

test("webtoon asks for the pages ahead before they scroll near", async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 700 });
  const asked = new Set<number>();
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const pageImage = path.match(/\/read\/chapters\/1\/pages\/(\d+)$/);
    if (pageImage) {
      if (url.searchParams.get("w") !== "320") asked.add(Number(pageImage[1]));
      await route.fulfill({ contentType: "image/svg+xml", body: image(600, 1800) }).catch(() => undefined);
      return;
    }
    if (path.endsWith("/auth/status")) {
      await route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
      return;
    }
    if (path.endsWith("/read/settings")) {
      await route.fulfill({ json: { defaults: { autoNext: false, direction: "vertical", keepAwake: false, mode: "webtoon", preload: 3 }, series: null } });
      return;
    }
    if (path.endsWith("/read/chapters/1") && route.request().method() === "GET") {
      await route.fulfill({ json: {
        id: 1, seriesId: 1, seriesTitle: "Strip", number: "1", readingDirection: "webtoon", downloaded: true, canDownload: false,
        pages: Array.from({ length: 20 }, (_, i) => ({ number: i + 1, mediaType: "image/svg+xml" })),
        progress: { page: 0, completed: false },
      } });
      return;
    }
    await route.fulfill({ json: {} });
  });

  await page.goto("/read/1");
  // each page is several screens tall: page 4 starts about ten screens down
  await expect.poll(() => [1, 2, 3, 4].every((n) => asked.has(n))).toBe(true);

  // reading on keeps the same distance ahead
  await page.locator('[data-page="8"]').evaluate((el) => el.scrollIntoView({ block: "start" }));
  await expect.poll(() => [9, 10, 11].every((n) => asked.has(n))).toBe(true);
});

test("webtoon page down scrolls as far as the setting says", async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 700 });
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (/\/read\/chapters\/1\/pages\/\d+$/.test(path)) return route.fulfill({ contentType: "image/svg+xml", body: image(600, 1800) });
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
    if (path.endsWith("/read/settings")) return route.fulfill({ json: { defaults: { mode: "webtoon", tapScroll: 0.5, showTapZones: false }, series: null } });
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
  const scroller = page.locator(".overflow-y-auto").first();
  await page.keyboard.press("PageDown");
  await expect.poll(() => scroller.evaluate((el) => Math.round(el.scrollTop / el.clientHeight * 100))).toBe(50);
});

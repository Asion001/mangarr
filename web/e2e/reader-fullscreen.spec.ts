import { expect, test, type Page } from "@playwright/test";

const svg = '<svg xmlns="http://www.w3.org/2000/svg" width="700" height="1000"><rect width="100%" height="100%" fill="#333"/></svg>';

const chapter = {
  id: 1, seriesId: 7, seriesTitle: "Full Screen Test", number: "1", readingDirection: "ltr", downloaded: true, canDownload: false,
  pages: Array.from({ length: 3 }, (_, index) => ({ number: index + 1, mediaType: "image/svg+xml" })),
  progress: { page: 0, completed: false },
};

const iPhone = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1";
const iPhoneChrome = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/141.0.0.0 Mobile/15E148 Safari/604.1";

async function mock(page: Page, standalone: boolean) {
  // iPhone Safari: no Fullscreen API for pages, and navigator.standalone when opened from the Home Screen
  await page.addInitScript((standalone) => {
    for (const name of ["fullscreenEnabled", "webkitFullscreenEnabled"]) Object.defineProperty(document, name, { get: () => false });
    Object.defineProperty(navigator, "standalone", { get: () => standalone });
  }, standalone);
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (/\/read\/chapters\/\d+\/pages\/\d+$/.test(path)) return route.fulfill({ contentType: "image/svg+xml", body: svg });
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
    if (path.endsWith("/read/settings")) return route.fulfill({ json: { defaults: { direction: "ltr", mode: "paged", spread: "single" }, series: null } });
    if (route.request().method() === "GET" && path.endsWith("/read/chapters/1")) return route.fulfill({ json: chapter });
    return route.fulfill({ json: {} });
  });
}

test.describe("in an iPhone browser", () => {
  test.use({ userAgent: iPhone, viewport: { width: 390, height: 844 } });

  test("the full screen button explains Add to Home Screen", async ({ page }) => {
    await mock(page, false);
    await page.goto("/read/1");
    await expect(page.getByText("1 / 3")).toBeVisible();
    await page.getByRole("button", { name: "Full screen" }).click();
    await expect(page.getByText("iPhone browsers can't go full screen")).toBeVisible();
    await expect(page.getByText(/Add to Home Screen/)).toBeVisible();
  });

  test("opened from the Home Screen there's no full screen button", async ({ page }) => {
    await mock(page, true);
    await page.goto("/read/1");
    await expect(page.getByText("1 / 3")).toBeVisible();
    await expect(page.getByRole("button", { name: "Full screen" })).toHaveCount(0);
  });
});

test.describe("in Chrome on iPhone", () => {
  test.use({ userAgent: iPhoneChrome, viewport: { width: 390, height: 844 } });

  test("the full screen button explains Add to Home Screen too", async ({ page }) => {
    await mock(page, false);
    await page.goto("/read/1");
    await expect(page.getByText("1 / 3")).toBeVisible();
    await page.getByRole("button", { name: "Full screen" }).click();
    await expect(page.getByText("iPhone browsers can't go full screen")).toBeVisible();
  });
});

test("leaving full screen keeps the webtoon strip where you read to", async ({ page }) => {
  await page.setViewportSize({ width: 820, height: 1180 });
  const tall = '<svg xmlns="http://www.w3.org/2000/svg" width="600" height="1200"><rect width="100%" height="100%" fill="#555"/></svg>';
  const progress: { chapter: number; page: number }[] = [];
  const strip = (id: number) => ({
    id, seriesId: 7, seriesTitle: "Strip", number: String(id), readingDirection: "webtoon", downloaded: true, canDownload: false,
    pages: [1, 2, 3].map((number) => ({ number, mediaType: "image/svg+xml" })),
    progress: { page: 0, completed: false },
    ...(id === 1 ? { next: { id: 2, number: "2" } } : { prev: { id: 1, number: "1" } }),
  });
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (/\/read\/chapters\/\d+\/pages\/\d+$/.test(path)) return route.fulfill({ contentType: "image/svg+xml", body: tall });
    const saved = path.match(/\/read\/chapters\/(\d+)\/progress$/);
    if (saved) {
      progress.push({ chapter: Number(saved[1]), page: route.request().postDataJSON().page });
      return route.fulfill({ json: {} });
    }
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
    if (path.endsWith("/read/settings")) return route.fulfill({ json: { defaults: { mode: "webtoon", keepAwake: false, preload: 2, showTapZones: false }, series: null } });
    const ch = path.match(/\/read\/chapters\/(\d+)$/);
    if (ch && route.request().method() === "GET") return route.fulfill({ json: strip(Number(ch[1])) });
    return route.fulfill({ json: [] });
  });

  await page.goto("/read/1");
  const second = page.locator('[data-chapter="2"][data-page="2"]');
  await expect(second).toBeAttached();
  await second.evaluate((el) => el.scrollIntoView({ block: "start" }));
  await expect(page).toHaveURL(/\/read\/2$/);
  await expect.poll(() => progress.some((p) => p.chapter === 2 && p.page === 2)).toBe(true);
  progress.length = 0;

  // Safari leaving full screen: the page goes back to where it was when full
  // screen began, a moment after the change
  await page.evaluate(() => {
    document.dispatchEvent(new Event("fullscreenchange"));
    setTimeout(() => (document.scrollingElement!.scrollTop = 0), 300);
  });
  await page.waitForTimeout(2000);

  await expect(page).toHaveURL(/\/read\/2$/);
  await expect(second).toBeInViewport();
  expect(progress.filter((p) => p.chapter === 1)).toEqual([]);
});

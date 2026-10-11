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

import { expect, test } from "@playwright/test";

const image = `<svg xmlns="http://www.w3.org/2000/svg" width="600" height="1800"><rect width="100%" height="100%" fill="#555"/></svg>`;

test("a webtoon page that failed keeps its space and fills it on retry", async ({ page }) => {
  await page.setViewportSize({ width: 600, height: 800 });
  let fail = true;
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const pageImage = path.match(/\/read\/chapters\/1\/pages\/(\d+)$/);
    if (pageImage) {
      if (pageImage[1] === "1" && url.searchParams.get("w") !== "320" && fail) return route.fulfill({ status: 500, body: "" });
      return route.fulfill({ contentType: "image/svg+xml", body: image });
    }
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
    if (path.endsWith("/read/settings")) return route.fulfill({ json: { defaults: { mode: "webtoon", preload: 2, showTapZones: false }, series: null } });
    if (path.endsWith("/read/chapters/1") && route.request().method() === "GET") {
      return route.fulfill({ json: {
        id: 1, seriesId: 1, seriesTitle: "Retry", number: "1", readingDirection: "webtoon", downloaded: true, canDownload: false,
        pages: [1, 2].map((number) => ({ number, mediaType: "image/svg+xml" })),
        progress: { page: 0, completed: false },
      } });
    }
    return route.fulfill({ json: [] });
  });

  await page.goto("/read/1");
  const first = page.locator('[data-page="1"]');
  const retry = first.getByRole("button", { name: /retry/ });
  await expect(retry).toBeVisible();
  // the error holds the page's own height (from its placeholder)
  await expect.poll(() => first.evaluate((el) => el.getBoundingClientRect().height)).toBeCloseTo(1800, 0);

  fail = false;
  await retry.click();
  const full = first.locator("img:not([aria-hidden])");
  await expect.poll(() => full.evaluate((el: HTMLImageElement) => el.complete && el.naturalHeight > 0)).toBe(true);
  await expect.poll(() => full.evaluate((el) => getComputedStyle(el).opacity)).toBe("1");
  expect(await first.evaluate((el) => el.getBoundingClientRect().height)).toBeCloseTo(1800, 0);
});

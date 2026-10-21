import { expect, test } from "@playwright/test";

const svg = '<svg xmlns="http://www.w3.org/2000/svg" width="700" height="1000"><rect width="100%" height="100%" fill="#333"/></svg>';

// Opened with no server (offline from a closed browser), the account's reader
// settings can't be fetched: the reader opens a saved chapter anyway instead
// of waiting on them behind a spinner.
test("opens the chapter when the reader settings can't be fetched", async ({ page }) => {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (/\/read\/chapters\/1\/pages\/\d+$/.test(path)) return route.fulfill({ contentType: "image/svg+xml", body: svg });
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
    if (path.endsWith("/read/settings")) return route.abort("internetdisconnected");
    if (path.endsWith("/read/chapters/1") && route.request().method() === "GET") {
      return route.fulfill({ json: {
        id: 1, seriesId: 1, seriesTitle: "Offline Test", number: "1", readingDirection: "ltr", downloaded: true, canDownload: false,
        pages: Array.from({ length: 3 }, (_, index) => ({ number: index + 1, mediaType: "image/svg+xml" })),
        progress: { page: 0, completed: false },
      } });
    }
    return route.fulfill({ json: {} });
  });
  await page.goto("/read/1");
  await expect(page.getByText("1 / 3").first()).toBeVisible({ timeout: 5_000 });
  await expect(page.locator('img[src*="/pages/1"]').last()).toBeVisible();
});

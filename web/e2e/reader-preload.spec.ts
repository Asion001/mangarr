import { expect, test } from "@playwright/test";

const svg = '<svg xmlns="http://www.w3.org/2000/svg" width="700" height="1000"><rect width="100%" height="100%" fill="#333"/></svg>';

test("preloads upcoming reader pages and cancels pages skipped by a fast jump", async ({ page }) => {
  const requested = new Set<number>();
  const cancelled = new Set<number>();
  const timeReports: { sessionId: string; activeSeconds: number }[] = [];
  await page.addInitScript(() => {
    let layoutWidth = window.innerWidth;
    let scale = 1;
    const visual = new EventTarget();
    Object.defineProperties(visual, {
      scale: { get: () => scale },
      width: { get: () => layoutWidth / scale },
      height: { get: () => window.innerHeight / scale },
    });
    Object.defineProperty(window, "visualViewport", { configurable: true, value: visual });
    Object.defineProperty(window, "innerWidth", { configurable: true, get: () => layoutWidth });
    (window as Window & { simulateVisualZoom?: () => void }).simulateVisualZoom = () => {
      layoutWidth = 320;
      scale = 2;
      visual.dispatchEvent(new Event("resize"));
      window.dispatchEvent(new Event("resize"));
    };
  });
  page.on("requestfailed", (request) => {
    const match = new URL(request.url()).pathname.match(/\/read\/chapters\/1\/pages\/(\d+)$/);
    if (match) cancelled.add(Number(match[1]));
  });
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const image = path.match(/\/read\/chapters\/1\/pages\/(\d+)$/);
    if (image) {
      const number = Number(image[1]);
      requested.add(number);
      if (number >= 2 && number <= 5 && url.searchParams.get("w") !== "320") {
        await new Promise((resolve) => setTimeout(resolve, 2_000));
      }
      await route.fulfill({ contentType: "image/svg+xml", body: svg }).catch(() => undefined);
      return;
    }
    if (path.endsWith("/auth/status")) {
      await route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
      return;
    }
    if (path.endsWith("/read/settings")) {
      await route.fulfill({ json: { defaults: { direction: "ltr", keepAwake: false, mode: "paged", preload: 4, spread: "single" }, series: null } });
      return;
    }
    if (path.endsWith("/read/chapters/1/time")) {
      timeReports.push(route.request().postDataJSON());
      await route.fulfill({ json: {} });
      return;
    }
    if (path.endsWith("/read/chapters/1") && route.request().method() === "GET") {
      await route.fulfill({ json: {
        id: 1, seriesId: 1, seriesTitle: "Preload Test", number: "1", readingDirection: "ltr", downloaded: true, canDownload: false,
        pages: Array.from({ length: 20 }, (_, index) => ({ number: index + 1, mediaType: "image/svg+xml" })),
        progress: { page: 0, completed: false },
      } });
      return;
    }
    await route.fulfill({ json: {} });
  });

  await page.goto("/read/1");
  await expect(page.getByText("1 / 20")).toBeVisible();
  await expect(page.getByRole("button", { name: "Mark chapter read" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Mark previous chapters read" })).toHaveCount(0);
  const firstPage = page.locator('img[src*="/pages/1"]').last();
  await expect(firstPage).toBeVisible();
  const widthBeforeZoom = await firstPage.evaluate((image) => image.getBoundingClientRect().width);
  await page.evaluate(() => (window as Window & { simulateVisualZoom?: () => void }).simulateVisualZoom?.());
  await page.waitForTimeout(50);
  expect(await firstPage.evaluate((image) => image.getBoundingClientRect().width)).toBe(widthBeforeZoom);
  await page.waitForTimeout(1_100);
  await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent("pagehide")));
  await expect.poll(() => timeReports.at(-1)?.activeSeconds).toBeGreaterThanOrEqual(1);
  expect(timeReports.at(-1)?.sessionId.length).toBeGreaterThanOrEqual(16);
  await expect.poll(() => [2, 3, 4, 5].every((number) => requested.has(number))).toBe(true);

  // the bars hide on their own after a moment; on a slow machine they already have
  const slider = page.getByRole("slider", { name: "Page" });
  if (!(await slider.isVisible())) await page.keyboard.press("m");
  await slider.fill("10");
  await expect(page.getByText("10 / 20").first()).toBeVisible();
  await expect.poll(() => [11, 12, 13, 14].every((number) => requested.has(number))).toBe(true);
  await expect.poll(() => [2, 3, 4, 5].some((number) => cancelled.has(number))).toBe(true);
});

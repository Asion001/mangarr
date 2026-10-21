import { expect, test } from "@playwright/test";

test.use({ viewport: { width: 375, height: 812 }, hasTouch: true, isMobile: true });

test("on a phone, tab rows only scroll sideways and fields don't zoom the page", async ({ page }) => {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin"] } } });
    if (path.endsWith("/queue")) return route.fulfill({ json: { total: 0, state: { paused: false }, items: [] } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });

  await page.goto("/system/status");
  const tabs = page.getByRole("navigation", { name: "System" });
  await expect(tabs).toBeVisible();
  // the tabs' underline hangs a pixel below the row; it must not make the row scroll up and down
  expect(await tabs.evaluate((el) => el.scrollHeight > el.clientHeight && getComputedStyle(el).overflowY !== "hidden")).toBe(false);

  // the page itself never scrolls sideways
  expect(await page.locator("main").evaluate((el) => getComputedStyle(el).overflowX)).toBe("hidden");
  // pinch zoom is left to the reader
  expect(await page.getByTestId("desktop-navigation").evaluate((el) => getComputedStyle(el.parentElement!).touchAction)).toBe("pan-x pan-y");

  await page.goto("/settings/media");
  // iOS zooms in on a focused field with text under 16px
  const field = page.locator("main input[type=text], main input:not([type])").first();
  await expect(field).toBeVisible();
  expect(await field.evaluate((el) => getComputedStyle(el).fontSize)).toBe("16px");
});

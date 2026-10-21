import { expect, test } from "@playwright/test";

// Chapters asked to be saved wait their turn in a queue the page lists, so a
// long "save all unread" shows what is still to come and can be called off.
test("chapters waiting to be saved are listed and can be taken out", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en" }));
    localStorage.setItem("mangarr.offline.v1", JSON.stringify({ 5: { id: 5, seriesId: 1, seriesTitle: "Queue Test", number: "5", kind: "kept", pages: 20, saved: 8, bytes: 900, width: 720, at: 1 } }));
    localStorage.setItem("mangarr.offline.queue", JSON.stringify([
      { id: 6, seriesId: 1, seriesTitle: "Queue Test", number: "6" },
      { id: 7, seriesId: 1, seriesTitle: "Queue Test", number: "7" },
    ]));
  });
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
    return route.fulfill({ json: [] });
  });
  await page.goto("/on-device");
  await expect(page.getByText("Saving 3 chapters")).toBeVisible();
  await expect(page.getByText("Waiting: 2")).toBeVisible();
  await expect(page.getByText("Queue Test · Ch. 7")).toBeVisible();
  await page.getByRole("button", { name: "Don't save chapter 7" }).click();
  await expect(page.getByText("Waiting: 1")).toBeVisible();
  await page.getByRole("button", { name: "Cancel all" }).click();
  await expect(page.getByText("Saving 1 chapters")).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem("mangarr.offline.queue"))).toBe("[]");
});

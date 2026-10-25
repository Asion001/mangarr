import { expect, test } from "@playwright/test";

const series = {
  id: 1, workId: 1, title: "Queue Series", sortTitle: "queue series", status: "ongoing", monitored: true, monitorNew: "all",
  rootFolderId: 1, path: "Queue Series", profileId: 1, language: "en", sourcePriorityMode: "inherit", readingDirection: "ltr",
  tags: [], metadata: {}, addOptions: { pending: false }, addedAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z",
  coverUrl: "", following: false, sources: [], editions: [],
  stats: { chapterCount: 30, monitoredCount: 30, fileCount: 30, missingCount: 0, cleanedCount: 0, sizeOnDisk: 0, spaceSaved: 0, lastChapter: 30, readCount: 3, inProgressCount: 0 },
};
const file = { pageCount: 20, size: 1000, format: "cbz", sourceName: "Source", avgWidth: 800, upscaled: false, processState: "done" };
const chapters = Array.from({ length: 30 }, (_, index) => ({
  id: index + 1, seriesId: 1, number: String(index + 1), numberSort: index + 1, title: `Chapter ${index + 1}`,
  monitored: true, releaseDate: "2026-09-01T00:00:00Z", state: "imported", releases: [], file: { ...file, id: index + 1 },
  readBy: index < 3 ? [{ userId: 1, name: "me", completed: true, page: 20, readAt: "2026-09-01T00:00:00Z" }] : [],
}));

// Like Mihon: "next 5 unread" takes the next five not saved or waiting yet,
// so asking twice queues ten.
test("asking for the next unread chapters again queues the ones after them", async ({ page }) => {
  await page.addInitScript(() => {
    localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en" }));
    // as if a service worker answered, so chapters can be saved here
    Object.defineProperty(navigator.serviceWorker, "controller", { get: () => ({}) });
  });
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
    if (path.endsWith("/series/1/chapters")) return route.fulfill({ json: chapters });
    if (path.endsWith("/series/1")) return route.fulfill({ json: series });
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    // saving itself can't happen here: chapters stay queued
    if (path.includes("/read/chapters/")) return route.fulfill({ status: 503, json: {} });
    return route.fulfill({ json: [] });
  });
  const queued = () => page.evaluate(() => (JSON.parse(localStorage.getItem("mangarr.offline.queue") || "[]") as { number: string }[]).map((q) => q.number));
  await page.goto("/series/1");
  const menu = page.getByRole("button", { name: "Save on device" }).first();
  await menu.click();
  await page.getByRole("menuitem", { name: /Next 5 unread/ }).click();
  await expect.poll(queued).toEqual(["4", "5", "6", "7", "8"]);
  await menu.click();
  await page.getByRole("menuitem", { name: /Next 5 unread/ }).click();
  await expect.poll(queued).toEqual(["4", "5", "6", "7", "8", "9", "10", "11", "12", "13"]);
  await menu.click();
  await page.getByRole("menuitem", { name: /Next 15 unread/ }).click();
  await expect.poll(async () => (await queued()).length).toBe(25);
});

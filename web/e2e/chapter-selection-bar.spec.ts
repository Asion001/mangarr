import { expect, test, type Page } from "@playwright/test";

const series = {
  id: 1, workId: 1, title: "Long Series", sortTitle: "long series", status: "ongoing", monitored: true, monitorNew: "all",
  rootFolderId: 1, path: "Long Series", profileId: 1, language: "en", sourcePriorityMode: "inherit", readingDirection: "ltr",
  tags: [], metadata: {}, addOptions: { pending: false }, addedAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z",
  coverUrl: "", following: false, sources: [], editions: [],
  reading: { nextUnread: { chapterId: 69, number: "69", page: 5 } },
  stats: { chapterCount: 120, monitoredCount: 120, fileCount: 120, missingCount: 0, cleanedCount: 0, sizeOnDisk: 0, spaceSaved: 0, lastChapter: 120, readCount: 68, inProgressCount: 1 },
};
const file = { id: 1, pageCount: 20, size: 1000, format: "cbz", sourceName: "Source", avgWidth: 800, upscaled: false, processState: "done" };
const chapters = Array.from({ length: 120 }, (_, index) => ({
  id: index + 1, seriesId: 1, number: String(index + 1), numberSort: index + 1, title: `Chapter ${index + 1}`,
  monitored: true, releaseDate: "2026-09-01T00:00:00Z", state: "imported", releases: [], file: { ...file, id: index + 1 },
  readBy: index + 1 < 69 ? [{ readerId: 1, reader: "me", completed: true, page: 20, readAt: "2026-09-01T00:00:00Z" }] : index + 1 === 69 ? [{ readerId: 1, reader: "me", completed: false, page: 5, readAt: "2026-09-01T00:00:00Z" }] : [],
}));

async function mock(page: Page, marked: number[]) {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const mark = path.match(/\/read\/chapters\/(\d+)\/mark$/);
    if (mark) {
      marked.push(Number(mark[1]));
      return route.fulfill({ json: {} });
    }
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin", "library.manage", "library.delete", "queue.manage"] } } });
    if (path.endsWith("/series/1/chapters")) return route.fulfill({ json: chapters });
    if (path.endsWith("/series/1")) return route.fulfill({ json: series });
    if (path.endsWith("/queue")) return route.fulfill({ json: { total: 0, state: { paused: false }, items: [] } });
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/recycle-bin")) return route.fulfill({ json: { items: [], total: 0 } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });
}

test.use({ viewport: { width: 390, height: 844 } });

test("a long chapter list opens on the page with the chapter to read next", async ({ page }) => {
  await mock(page, []);
  await page.goto("/series/1");
  // 25 a page: chapter 69 is on page 3
  await expect(page.getByText("51–75 / 120")).toBeVisible();
  await expect(page.locator("article", { hasText: "Chapter 69" }).getByText("Up next")).toBeVisible();
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await page.getByRole("button", { name: "Up next: 69" }).click();
  await expect(page.getByText("51–75 / 120")).toBeVisible();
  // the list keeps the order it came in: chapter 110 is on the last page
  await page.getByRole("textbox", { name: "Jump to chapter" }).fill("110");
  await page.getByRole("textbox", { name: "Jump to chapter" }).press("Enter");
  await expect(page.getByText("101–120 / 120")).toBeVisible();
  await expect(page.locator("article", { hasText: "Chapter 110" })).toBeInViewport();
  await page.getByRole("combobox", { name: "Chapters on this page" }).selectOption({ index: 0 });
  await expect(page.getByText("1–25 / 120")).toBeVisible();
});

test("selected chapters get a bar along the bottom with the rest under More", async ({ page }) => {
  const marked: number[] = [];
  await mock(page, marked);
  await page.goto("/series/1");
  const bar = page.getByRole("toolbar", { name: "Selected chapters" });
  await expect(bar).toHaveCount(0);
  await page.getByRole("checkbox", { name: "Select Chapter 70" }).check();
  await page.getByRole("checkbox", { name: "Select Chapter 71" }).check();
  await expect(page.getByText("2 selected")).toBeVisible();
  await expect(bar).toBeVisible();
  // the bar covers the tab bar while chapters are selected
  const box = await bar.boundingBox();
  expect(box!.y + box!.height).toBeGreaterThanOrEqual(844 - 1);
  await bar.getByRole("button", { name: "Mark read" }).click();
  await expect.poll(() => marked.sort()).toEqual([70, 71]);

  await bar.getByRole("button", { name: "More" }).click();
  const sheet = page.getByRole("dialog", { name: "2 chapters selected" });
  await expect(sheet.getByRole("button", { name: "Monitor", exact: true })).toBeVisible();
  await expect(sheet.getByRole("group", { name: "In the queue" })).toHaveCount(0);
  await sheet.getByRole("button", { name: "Done" }).click();
  await expect(sheet).toHaveCount(0);

  await page.getByRole("button", { name: "Cancel" }).click();
  await expect(bar).toHaveCount(0);
});

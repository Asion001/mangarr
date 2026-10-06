import { expect, test } from "@playwright/test";

const editions = [
  { id: 1, title: "Edition EN", language: "en", coverUrl: "" },
  { id: 2, title: "Edition RU", language: "ru", coverUrl: "" },
];
const series = {
  id: 1, workId: 1, title: "Edition EN", sortTitle: "edition en", status: "ongoing", monitored: true, monitorNew: "all",
  rootFolderId: 1, path: "Edition EN", profileId: 1, language: "en", sourcePriorityMode: "inherit", readingDirection: "ltr",
  tags: [], metadata: {}, addOptions: { pending: false }, addedAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z",
  coverUrl: "", following: false, sources: [], editions,
  stats: { chapterCount: 2, monitoredCount: 2, fileCount: 2, missingCount: 0, cleanedCount: 0, sizeOnDisk: 0, spaceSaved: 0, lastChapter: 45, readCount: 0, inProgressCount: 0 },
};
const chapter = (id: number, seriesId: number, n: number) => ({
  id, seriesId, number: String(n), numberSort: n, title: `Chapter ${n}`, monitored: true, releaseDate: "2026-09-01T00:00:00Z",
  state: "imported", releases: [], readBy: [], file: { id, path: `${n}.cbz`, size: 1, pages: 20 },
});

test("chapters only in another language collapse to one summary row", async ({ page }) => {
  await page.setViewportSize({ width: 768, height: 1024 });
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en" })));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["library.manage"] } } });
    if (path.endsWith("/series/1/chapters")) return route.fulfill({ json: [chapter(1001, 1, 45), chapter(1002, 1, 44)] });
    if (path.endsWith("/series/2/chapters")) return route.fulfill({ json: Array.from({ length: 20 }, (_, i) => chapter(i + 1, 2, i + 2)) });
    if (path.endsWith("/series/1")) return route.fulfill({ json: series });
    if (path.endsWith("/series")) return route.fulfill({ json: [series] });
    if (path.endsWith("/queue")) return route.fulfill({ json: { total: 0, state: { paused: false }, items: [] } });
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });

  await page.goto("/series/1");
  const block = page.getByRole("region", { name: "Chapters from other languages" });
  const toggle = block.getByRole("button", { name: /Chapters only in Russian: 20/ });
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(toggle).toContainText("2–21");
  await expect(block.getByRole("link", { name: "Read" })).toHaveCount(0);
  await block.scrollIntoViewIfNeeded();
  await page.screenshot({ path: "test-results/other-language-collapsed.png" });

  await toggle.click();
  await expect(block.getByRole("link", { name: "Read" })).toHaveCount(20);
  await page.screenshot({ path: "test-results/other-language-expanded.png" });
});

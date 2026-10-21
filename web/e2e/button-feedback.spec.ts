import { expect, test } from "@playwright/test";

const series = {
  id: 1, workId: 1, title: "Some title", sortTitle: "some title", status: "ongoing", monitored: true, monitorNew: "all",
  rootFolderId: 1, path: "Some title", profileId: 1, language: "en", sourcePriorityMode: "inherit", readingDirection: "ltr",
  tags: [], metadata: {}, addOptions: { pending: false }, addedAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z",
  coverUrl: "", following: false, sources: [], editions: [],
  stats: { chapterCount: 2, monitoredCount: 2, fileCount: 2, missingCount: 0, cleanedCount: 0, sizeOnDisk: 0, spaceSaved: 0, lastChapter: 2, readCount: 0, inProgressCount: 0 },
};
const chapter = (id: number, n: number) => ({
  id, seriesId: 1, number: String(n), numberSort: n, title: `Chapter ${n}`, monitored: true, releaseDate: "2026-09-01T00:00:00Z",
  state: "imported", releases: [], readBy: [], file: { id, path: `${n}.cbz`, size: 1, pages: 20 },
});

test("a chapter's mark button spins until the mark is saved, then says so", async ({ page }) => {
  await page.setViewportSize({ width: 768, height: 1024 });
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en" })));
  let release = () => {};
  const saved = new Promise<void>((resolve) => { release = resolve; });
  let marked: unknown;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["library.manage"] } } });
    if (path.endsWith("/read/chapters/1002/mark")) {
      marked = route.request().postDataJSON();
      await saved;
      return route.fulfill({ json: {} });
    }
    if (path.endsWith("/series/1/chapters")) return route.fulfill({ json: [chapter(1002, 2), chapter(1001, 1)] });
    if (path.endsWith("/series/1")) return route.fulfill({ json: series });
    if (path.endsWith("/queue")) return route.fulfill({ json: { total: 0, state: { paused: false }, items: [] } });
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });

  await page.goto("/series/1");
  await page.getByRole("button", { name: /Chapter 2/ }).first().click();
  const button = page.getByRole("button", { name: "Mark previous chapters read" });
  await button.click();

  await expect(button).toBeDisabled();
  await expect(button).toHaveAttribute("aria-busy", "true");
  expect(marked).toEqual({ read: true, scope: "previous" });

  release();
  await expect(button).toBeEnabled();
  await expect(button).not.toHaveAttribute("aria-busy");
  await expect(page.getByText("Earlier chapters marked read")).toBeVisible();
});

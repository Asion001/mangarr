import { expect, test, type Page } from "@playwright/test";

const series = {
  id: 1, workId: 1, title: "Long Series", sortTitle: "long series", status: "ongoing", monitored: true, monitorNew: "all",
  rootFolderId: 1, path: "Long Series", profileId: 1, language: "en", sourcePriorityMode: "inherit", readingDirection: "ltr",
  tags: [], metadata: {}, addOptions: { pending: false }, addedAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z",
  coverUrl: "", following: false, sources: [], editions: [],
  stats: { chapterCount: 250, monitoredCount: 250, fileCount: 0, missingCount: 250, cleanedCount: 0, sizeOnDisk: 0, spaceSaved: 0, lastChapter: 250, readCount: 0, inProgressCount: 0 },
};

async function mockSeriesPage(page: Page, chapters: Record<string, unknown>[]) {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["library.manage"] } } });
    if (path.endsWith("/series/1/chapters")) return route.fulfill({ json: chapters });
    if (path.endsWith("/series/1")) return route.fulfill({ json: series });
    if (path.endsWith("/series")) return route.fulfill({ json: [series] });
    if (path.endsWith("/queue")) return route.fulfill({ json: { total: 0, state: { paused: false }, items: [] } });
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });
}

test("large chapter tables render a small remembered page while bulk selection still covers all", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
  const chapters = Array.from({ length: 250 }, (_, index) => ({
    id: index + 1, seriesId: 1, number: String(index + 1), numberSort: index + 1, title: `Chapter ${index + 1}`,
    monitored: true, releaseDate: "2026-09-01T00:00:00Z", state: "missing", releases: [], readBy: [],
  }));
  await mockSeriesPage(page, chapters);

  await page.goto("/series/1");
  const table = page.getByRole("table");
  await expect(page.getByText("Chapters (250)")).toBeVisible();
  await expect(table.locator("tbody > tr")).toHaveCount(25);
  await table.locator('thead input[type="checkbox"]').check();
  await expect(page.getByText("250 selected")).toBeVisible();

  const pageSize = page.getByRole("combobox", { name: "per page" });
  await pageSize.selectOption("50");
  await expect(table.locator("tbody > tr")).toHaveCount(50);
  await page.reload();
  await expect(pageSize).toHaveValue("50");
  await expect(page.getByRole("table").locator("tbody > tr")).toHaveCount(50);

  // the page survives a reload too
  await page.getByRole("button", { name: "Next" }).click();
  await expect(page).toHaveURL(/chapterPage=2/);
  await page.reload();
  await expect(page.getByText("51–100 / 250")).toBeVisible();
});

test("desktop reading actions stay aligned and queue controls expand below the row", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
  const release = { id: 1, sourceName: "Reader source", name: "Release", uploadDate: "2026-09-01T00:00:00Z", removed: false, blocklisted: false };
  let markBody: unknown;
  await mockSeriesPage(page, [
    { id: 1, seriesId: 1, number: "1", numberSort: 1, title: "Queued chapter", monitored: true, releaseDate: "2026-09-01T00:00:00Z", state: "queued", releases: [release], readBy: [], job: { id: 9, status: "queued", priority: 3, progress: 0 } },
    { id: 2, seriesId: 1, number: "2", numberSort: 2, title: "Regular chapter", monitored: true, releaseDate: "2026-09-02T00:00:00Z", state: "missing", releases: [{ ...release, id: 2 }], readBy: [] },
  ]);
  await page.route("**/api/v1/read/chapters/1/mark", async (route) => {
    markBody = route.request().postDataJSON();
    await route.fulfill({ json: {} });
  });

  await page.goto("/series/1");
  const queuedRow = page.getByRole("row").filter({ hasText: "Queued chapter" });
  const regularRow = page.getByRole("row").filter({ hasText: "Regular chapter" });
  const queuedRead = queuedRow.getByRole("button", { name: "Continue", exact: true });
  const regularRead = regularRow.getByRole("button", { name: "Read", exact: true });
  const [queuedBox, regularBox] = await Promise.all([queuedRead.boundingBox(), regularRead.boundingBox()]);
  expect(queuedBox).not.toBeNull();
  expect(regularBox).not.toBeNull();
  expect(Math.abs(queuedBox!.x - regularBox!.x)).toBeLessThan(1);
  await expect(page.getByRole("button", { name: "Top", exact: true })).toHaveCount(0);
  await queuedRow.getByRole("button", { expanded: false }).click();
  await expect(page.getByRole("button", { name: "Top", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Mark previous chapters read", exact: true }).click();
  await expect.poll(() => markBody).toEqual({ read: true, scope: "previous" });
});

for (const viewport of [{ width: 390, height: 844 }, { width: 768, height: 1024 }]) {
  test(`chapter actions fit without horizontal scrolling at ${viewport.width}px`, async ({ page }) => {
    await page.setViewportSize(viewport);
    await page.addInitScript(() => {
      localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" }));
      localStorage.setItem("mangarr:nav-collapsed", "true");
    });
    await mockSeriesPage(page, [{
      id: 1, seriesId: 1, number: "12.5", numberSort: 12.5, title: "A chapter title that must truncate instead of widening the page",
      monitored: true, releaseDate: "2026-09-01T00:00:00Z", state: "queued",
      releases: [{ id: 1, sourceName: "Reader source", name: "Chapter 12.5", uploadDate: "2026-09-01T00:00:00Z", removed: false, blocklisted: false }],
      readBy: [], job: { id: 9, status: "queued", priority: 3, progress: 0 },
    }]);

    await page.goto("/series/1");
    const row = page.locator("article").filter({ hasText: "A chapter title" });
    await expect(row).toBeVisible();
    await expect(row.getByRole("button", { name: "Continue", exact: true })).toBeVisible();
    expect(await row.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    await row.getByRole("button", { expanded: false }).click();
    await expect(row.getByRole("button", { name: "Top", exact: true })).toBeVisible();
    await expect(row.getByRole("button", { name: "Mark read", exact: true })).toBeVisible();
  });
}

test("compact reading mode keeps processing labels inside chapter details", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "reading" })));
  await mockSeriesPage(page, [{
    id: 1, seriesId: 1, number: "7", numberSort: 7, title: "Reader chapter", monitored: true,
    releaseDate: "2026-09-01T00:00:00Z", state: "processing", readBy: [],
    releases: [{ id: 1, sourceName: "Reader source", name: "Chapter 7", uploadDate: "2026-09-01T00:00:00Z", removed: false, blocklisted: false }],
    job: { id: 9, status: "processing", priority: 0, progress: 45 },
  }]);

  await page.goto("/series/1");
  const row = page.locator("article").filter({ hasText: "Reader chapter" });
  await expect(row.getByRole("button", { name: "Continue", exact: true })).toBeVisible();
  await expect(row.getByText("processing", { exact: true })).toHaveCount(0);
  await row.getByRole("button", { expanded: false }).click();
  await expect(row.getByText("processing", { exact: true })).toBeVisible();
});

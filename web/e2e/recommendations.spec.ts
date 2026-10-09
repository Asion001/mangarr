import { expect, test, type Page } from "@playwright/test";

const series = {
  id: 9, workId: 9, title: "Moonlit Garden", sortTitle: "moonlit garden", status: "completed", monitored: true, monitorNew: "none", preview: false,
  rootFolderId: 1, path: "Moonlit Garden", profileId: 1, language: "en", sourcePriorityMode: "inherit", readingDirection: "rtl",
  tags: [], metadata: {}, addOptions: { pending: false },
  addedAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z", coverUrl: "", following: false, editions: [], sources: [], adaptations: [],
  stats: { chapterCount: 0, monitoredCount: 0, fileCount: 0, missingCount: 0, cleanedCount: 0, sizeOnDisk: 0, spaceSaved: 0, lastChapter: 0, readCount: 0, inProgressCount: 0 },
};

const item = (over: Record<string, unknown>) => ({ provider: "anilist", moduleId: 1, moduleName: "AniList", id: "0", title: "", ...over });

const recommendations = {
  related: [item({ id: "20", title: "Moonlit Garden: After Hours", relation: "sequel", format: "manga", year: 2021 })],
  similar: [
    item({ id: "30", title: "Lantern Street", votes: 120, existingSeriesId: 31, seriesCoverUrl: "api/v1/series/31/cover" }),
    item({ id: "40", title: "Paper Comet", votes: 80 }),
    item({ id: "50", title: "Quiet Harbor", votes: 40, request: { id: 5, status: "pending", mine: true } }),
    { ...item({ id: "", moduleId: 0, moduleName: "", title: "Rain Archive" }), existingSeriesId: 61, sharedGenres: ["Drama", "Fantasy"] },
  ],
  errors: [],
};

async function mock(page: Page) {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:user:3", JSON.stringify({ locale: "en", mode: "reading" })));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status"))
      return route.fulfill({ json: { authenticated: true, account: { kind: "user", id: 3, username: "requester", permissions: ["requests.create"] } } });
    if (path.endsWith("/series/9")) return route.fulfill({ json: series });
    if (path.endsWith("/series/9/recommendations")) return route.fulfill({ json: recommendations });
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });
}

test("a title page recommends related and similar titles", async ({ page }) => {
  await mock(page);
  await page.goto("/series/9");
  const story = page.getByRole("region", { name: "Same story" });
  await expect(story).toContainText("Moonlit Garden: After Hours");
  await expect(story).toContainText("Sequel · manga · 2021");
  await expect(story.getByRole("button", { name: "Request", exact: true })).toBeVisible();

  const like = page.getByRole("region", { name: "More like this" });
  await expect(like.getByRole("link", { name: /Lantern Street/ })).toHaveAttribute("href", "/series/31");
  await expect(like).toContainText("Recommended on AniList");
  await expect(like).toContainText("Same genres: Drama, Fantasy");
  await expect(like.getByText("Requested")).toBeVisible();
  // Paper Comet is the only one left to ask for
  await expect(like.getByRole("button", { name: "Request", exact: true })).toHaveCount(1);

  await like.getByRole("button", { name: "In library 2" }).click();
  await expect(like).not.toContainText("Paper Comet");
  await expect(like).toContainText("Rain Archive");
  await like.getByRole("button", { name: "Not added 2" }).click();
  await expect(like).toContainText("Paper Comet");
  await expect(like).not.toContainText("Lantern Street");
});

test("on a phone the recommendations fit the screen", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mock(page);
  await page.goto("/series/9");
  await expect(page.getByRole("region", { name: "Same story" })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});

import { expect, test } from "@playwright/test";

const preview = {
  id: 7, workId: 7, title: "Tower of Ash", sortTitle: "tower of ash", status: "ongoing", monitored: false, monitorNew: "none", preview: true,
  rootFolderId: 1, path: "Tower of Ash", profileId: 1, language: "en", sourcePriorityMode: "inherit", readingDirection: "rtl",
  tags: [], metadata: {}, addOptions: { pending: false }, addedAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z",
  coverUrl: "", following: false, editions: [], adaptations: [],
  sources: [{ id: 3, seriesId: 7, moduleId: 1, sourceId: "paper", sourceName: "MangaDex", lang: "en", mangaUrl: "/tower", title: "Tower of Ash", webUrl: "", priority: 0, enabled: true }],
  stats: { chapterCount: 2, monitoredCount: 0, fileCount: 0, missingCount: 0, cleanedCount: 0, sizeOnDisk: 0, spaceSaved: 0, lastChapter: 2, readCount: 0, inProgressCount: 0 },
};

test("search Read opens a preview, and Add to library adds it", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
  const posts: { path: string; body: unknown }[] = [];
  await page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["library.manage"] } } });
    if (path.endsWith("/series/lookup")) return route.fulfill({ json: { providers: 1, results: [{ moduleId: 2, provider: "anilist", id: "99", moduleName: "AniList", title: "Tower of Ash", altTitles: ["Ash Tower"], format: "manhwa" }] } });
    if (path.endsWith("/previews")) {
      posts.push({ path, body: req.postDataJSON() });
      return route.fulfill({ json: { seriesId: 7, created: true, inLibrary: false } });
    }
    if (path.endsWith("/series") && req.method() === "POST") {
      posts.push({ path, body: req.postDataJSON() });
      return route.fulfill({ json: { ...preview, preview: false, monitored: true } });
    }
    if (path.endsWith("/series/7/chapters")) return route.fulfill({ json: [
      { id: 71, seriesId: 7, number: "1", numberSort: 1, title: "The Courier", monitored: false, releaseDate: "2021-03-03T00:00:00Z", state: "missing", releases: [{ id: 1 }], readBy: [] },
    ] });
    if (path.endsWith("/series/7")) return route.fulfill({ json: preview });
    if (path.endsWith("/profiles")) return route.fulfill({ json: [{ id: 1, name: "Standard", isDefault: true, config: {} }] });
    if (path.endsWith("/queue")) return route.fulfill({ json: { total: 0, state: { paused: false }, items: [] } });
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });

  await page.goto("/add?q=tower");
  await page.getByTitle(/Read without adding/).click();
  await expect(page).toHaveURL(/\/series\/7$/);
  expect(posts[0].body).toEqual({ metadata: { moduleId: 2, provider: "anilist", id: "99" }, title: "Tower of Ash", titles: ["Ash Tower"] });

  const banner = page.getByRole("status").filter({ hasText: "Preview." });
  await expect(banner).toContainText("MangaDex");
  await expect(page.getByRole("switch", { name: /Monitored/ })).toHaveCount(0);
  await page.getByRole("button", { name: "Add to library…" }).click();
  await page.getByRole("button", { name: "Add without downloading" }).click();
  await expect.poll(() => posts.find((p) => p.path.endsWith("/series"))?.body).toMatchObject({
    title: "Tower of Ash", language: "en", monitor: "all", searchMissing: false,
    sources: [{ moduleId: 1, sourceId: "paper", url: "/tower", sourceName: "MangaDex", lang: "en", title: "Tower of Ash" }],
  });
});

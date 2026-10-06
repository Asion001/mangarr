import { expect, test, type Page } from "@playwright/test";

const series = {
  id: 9, workId: 9, title: "Moonlit Garden", sortTitle: "moonlit garden", status: "completed", monitored: true, monitorNew: "none", preview: false,
  rootFolderId: 1, path: "Moonlit Garden", profileId: 1, language: "en", sourcePriorityMode: "inherit", readingDirection: "rtl",
  tags: [], metadata: { links: { AniList: "https://anilist.example/manga/9" } }, addOptions: { pending: false },
  addedAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z", coverUrl: "", following: false, editions: [], sources: [],
  stats: { chapterCount: 0, monitoredCount: 0, fileCount: 0, missingCount: 0, cleanedCount: 0, sizeOnDisk: 0, spaceSaved: 0, lastChapter: 0, readCount: 0, inProgressCount: 0 },
  adaptations: [
    { title: "Moonlit Garden", format: "tv", year: 2018, externalIds: { anilist: "11" }, links: { AniList: "https://anilist.example/anime/11" },
      watchLinks: [{ serverName: "Home Jellyfin", kind: "jellyfin", url: "https://media.example/items/11" }] },
    { title: "Moonlit Garden: Last Bloom", format: "movie", year: 2020, externalIds: { anilist: "12" }, links: { AniList: "https://anilist.example/anime/12" },
      watchLinks: [{ serverName: "Home Jellyfin", kind: "jellyfin", url: "https://media.example/items/12" }] },
    { title: "Moonlit Garden: Night Shift", format: "ova", year: 2019, externalIds: { anilist: "13" }, links: { AniList: "https://anilist.example/anime/13" }, watchLinks: [] },
  ],
};

async function mock(page: Page) {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "reading" })));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
    if (path.endsWith("/series/9")) return route.fulfill({ json: series });
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });
}

test("the adaptations chip lists anime with watch links", async ({ page }) => {
  await mock(page);
  await page.goto("/series/9");
  const chip = page.getByRole("button", { name: "Anime · 3 · 2 on Home Jellyfin" });
  await chip.click();
  const dialog = page.getByRole("dialog", { name: "Adaptations · from AniList" });
  await expect(dialog).toContainText("Movie · 2020");
  await expect(dialog.getByRole("link", { name: "Watch" })).toHaveCount(2);
  await expect(dialog.getByRole("link", { name: "Watch" }).first()).toHaveAttribute("href", "https://media.example/items/11");
  await expect(dialog.getByRole("link", { name: "AniList" })).toHaveCount(3);
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(chip).toBeFocused();
});

test("on a phone the adaptations open as a sheet", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mock(page);
  await page.goto("/series/9");
  await page.getByRole("button", { name: /^Anime · 3/ }).click();
  const dialog = page.getByRole("dialog", { name: "Adaptations · from AniList" });
  const box = await dialog.boundingBox();
  expect(box && Math.round(box.y + box.height)).toBe(844);
  // a watch link is enough on a phone; AniList shows only where there is none
  await expect(dialog.getByRole("link", { name: "AniList" }).and(page.locator(":visible"))).toHaveCount(1);
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog).toHaveCount(0);
});

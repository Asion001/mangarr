import { expect, test, type Page } from "@playwright/test";

const day = 86_400_000;
const series = {
  id: 4, workId: 4, title: "Steel Lantern", sortTitle: "steel lantern", status: "ongoing", monitored: true, monitorNew: "all", preview: false,
  rootFolderId: 1, path: "Steel Lantern", profileId: 1, language: "en", sourcePriorityMode: "inherit", readingDirection: "rtl",
  tags: [], addOptions: { pending: false }, addedAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z", coverUrl: "", following: false, sources: [],
  metadata: {
    format: "manga", year: 2023, ageRating: "16+", authors: ["Ren Aoki"], artists: ["Ren Aoki"], altTitles: ["Stahllaterne"],
    genres: ["Action", "Supernatural"], tags: ["supernatural", "Swords", "Revenge"], description: "A smith's son hunts the blades stolen from his father.",
    links: { AniList: "https://anilist.example/manga/4" },
  },
  editions: [
    { id: 4, language: "en", title: "Steel Lantern" },
    { id: 5, language: "uk", title: "Сталевий ліхтар" },
  ],
  stats: { chapterCount: 12, monitoredCount: 12, fileCount: 10, missingCount: 2, cleanedCount: 0, sizeOnDisk: 1_500_000_000, spaceSaved: 0, lastChapter: 12, readCount: 7, inProgressCount: 0 },
  adaptations: [],
};
// weekly releases, the last one three days ago
const chapters = Array.from({ length: 12 }, (_, i) => ({
  id: i + 1, seriesId: 4, number: String(i + 1), numberSort: i + 1, title: "", monitored: true, state: "downloaded", releases: [], readBy: [],
  releaseDate: new Date(Date.now() - (3 + (11 - i) * 7) * day).toISOString(),
}));

async function mock(page: Page) {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin"] } } });
    if (path.endsWith("/series/4/chapters")) return route.fulfill({ json: chapters });
    if (path.endsWith("/series/4")) return route.fulfill({ json: series });
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });
}

test("the title header shows facts, merged tags and stat cards", async ({ page }) => {
  await mock(page);
  await page.goto("/series/4");
  await expect(page.getByRole("heading", { name: "Steel Lantern" })).toBeVisible();
  // genres and tags in one row, without the repeated one
  const tags = page.getByRole("link", { name: /^(Action|Supernatural|Swords|Revenge)$/ });
  await expect(tags).toHaveCount(4);
  const facts = page.locator("dl:visible");
  await expect(facts).toContainText("Ongoing");
  await expect(facts).toContainText("Manga · 2023");
  await expect(facts).toContainText("Right to left");
  await expect(facts).toContainText("Story & art");
  await expect(page.getByText("10 / 12")).toBeVisible();
  await expect(page.getByText("2 missing")).toBeVisible();
  await expect(page.getByRole("button", { name: /^Next chapter/ })).toContainText("in 4 days");
  await expect(page.getByRole("switch", { name: "Monitored" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Language editions" }).getByRole("link")).toHaveCount(2);
  await page.getByRole("button", { name: "Manage", expanded: false }).click();
  await expect(page.getByRole("menuitem", { name: "Refresh sources" })).toBeVisible();
});

test("on a phone the facts move under the description", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mock(page);
  await page.goto("/series/4");
  const heading = await page.getByRole("heading", { name: "Steel Lantern" }).boundingBox();
  const facts = await page.locator("dl:visible").boundingBox();
  expect(heading && facts && facts.y > heading.y).toBe(true);
  await expect(page.locator("dl:visible")).toHaveCount(1);
});

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

test("the title header keeps to the title, Continue and Follow, with the rest under Details", async ({ page }) => {
  await mock(page);
  await page.goto("/series/4");
  await expect(page.getByRole("heading", { name: "Steel Lantern" })).toBeVisible();
  await expect(page.getByText("Ongoing", { exact: true })).toBeVisible();
  await expect(page.getByText("Manga, 2023")).toBeVisible();
  await expect(page.getByText("7 of 12 read")).toBeVisible();
  await expect(page.getByText("2 not downloaded")).toBeVisible();
  await expect(page.getByRole("button", { name: /Updated .* next in 4 days/ })).toBeVisible();
  // facts and tags stay folded until asked for
  await expect(page.locator("dl")).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Swords" })).toHaveCount(0);
  await page.getByRole("button", { name: "Details, tags and links" }).click();
  const facts = page.locator("dl");
  await expect(facts).toContainText("Right to left");
  await expect(facts).toContainText("Story & art");
  await expect(facts).toContainText("Stahllaterne");
  await expect(facts).toContainText("10 / 12");
  // genres and tags in one row, without the repeated one
  await expect(page.getByRole("link", { name: /^(Action|Supernatural|Swords|Revenge)$/ })).toHaveCount(4);
  // language editions sit behind the language button
  await page.getByRole("button", { name: "English", expanded: false }).click();
  await expect(page.getByRole("menuitem", { name: /Ukrainian/ })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: "Add language" })).toBeVisible();
  await page.keyboard.press("Escape");
  // monitoring and the rest of the admin actions are in one menu
  await page.getByRole("button", { name: "Manage", expanded: false }).click();
  await expect(page.getByRole("menuitem", { name: "Stop monitoring" })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: "Refresh sources" })).toBeVisible();
});

test("on a phone the cover sits beside the title and the actions run full width", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mock(page);
  await page.goto("/series/4");
  const heading = await page.getByRole("heading", { name: "Steel Lantern" }).boundingBox();
  const manage = await page.getByRole("button", { name: "Manage" }).boundingBox();
  // the title is indented by the cover beside it; the actions start at the left edge, under the cover
  expect(heading && manage && heading.x > manage.x + 100).toBe(true);
  expect(heading && manage && manage.y > heading.y + heading.height).toBe(true);
});

test("on a narrow phone the read, request and follow buttons each keep to one line", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  // one chapter downloaded, 300 more listed but not asked for
  const listed = Array.from({ length: 301 }, (_, i) => ({ ...chapters[0], id: i + 1, number: String(i + 1), numberSort: i + 1, monitored: i === 0, state: i === 0 ? "downloaded" : "missing", file: i === 0 ? { path: "1.cbz", size: 1 } : undefined }));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, account: { kind: "user", id: 3, username: "reader", permissions: ["requests.create"] } } });
    if (path.endsWith("/series/4/chapters")) return route.fulfill({ json: listed });
    if (path.endsWith("/series/4")) return route.fulfill({ json: series });
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });
  await page.goto("/series/4");
  const read = page.getByRole("link", { name: "Start reading ch. 1" });
  const request = page.getByRole("button", { name: "Request download", exact: true });
  const follow = page.getByRole("button", { name: "Follow", exact: true });
  await expect(follow).toBeVisible();
  const [r, q, f] = await Promise.all([read.boundingBox(), request.boundingBox(), follow.boundingBox()]);
  for (const b of [r, q, f]) {
    expect(b!.height).toBeLessThanOrEqual(44);
    expect(b!.x + b!.width).toBeLessThanOrEqual(375);
  }
  // read gets its own row; request and follow share the next one
  expect(q!.y).toBeGreaterThan(r!.y + r!.height - 1);
  expect(Math.abs(q!.y - f!.y)).toBeLessThan(1);
});

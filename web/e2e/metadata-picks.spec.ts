import { expect, test, type Page } from "@playwright/test";

const series = {
  id: 4, workId: 4, title: "Steel Lantern", sortTitle: "steel lantern", status: "ongoing", monitored: true, monitorNew: "all", preview: false,
  rootFolderId: 1, path: "Steel Lantern", profileId: 1, language: "en", sourcePriorityMode: "inherit", readingDirection: "rtl",
  tags: [], addOptions: { pending: false }, addedAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z", coverUrl: "", following: false, sources: [],
  metadata: {
    format: "manga", year: 2023, genres: ["Action"], tags: ["Swords"], description: "A smith's son hunts the blades stolen from his father.",
    coverUrl: "https://anilist.example/cover.jpg", externalIds: { anilist: "4", mangadex: "md-4" },
    provenance: { description: "anilist", coverUrl: "anilist", genres: "anilist" },
  },
  editions: [{ id: 4, language: "en", title: "Steel Lantern" }],
  stats: { chapterCount: 0, monitoredCount: 0, fileCount: 0, missingCount: 0, cleanedCount: 0, sizeOnDisk: 0, spaceSaved: 0, lastChapter: 0, readCount: 0, inProgressCount: 0 },
  adaptations: [],
};
const options = [
  { moduleId: 1, moduleName: "AniList", provider: "anilist", id: "4", title: "Steel Lantern", year: 2023, status: "ongoing",
    description: "A smith's son hunts the blades stolen from his father.", coverUrl: "https://anilist.example/cover.jpg", genres: ["Action"], tags: ["Swords"] },
  { moduleId: 3, moduleName: "MangaDex", provider: "mangadex", id: "md-4", title: "Steel Lantern", year: 2023, status: "ongoing",
    description: "Sparks fly when a blacksmith's son goes after the swords taken from his family.", genres: ["Action", "Drama"], tags: [],
    covers: [{ url: "https://mangadex.example/v3.jpg", label: "vol. 3" }, { url: "https://mangadex.example/v1.jpg", label: "vol. 1" }] },
];

async function mock(page: Page, calls: { path: string; body: unknown }[]) {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
  await page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin"] } } });
    if (req.method() !== "GET") {
      calls.push({ path, body: req.postDataJSON() });
      return route.fulfill({ json: series });
    }
    if (path.endsWith("/series/4/metadata/options")) return route.fulfill({ json: { options } });
    if (path.endsWith("/series/4")) return route.fulfill({ json: series });
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });
}

test("the cover and description are picked from another provider", async ({ page }) => {
  const calls: { path: string; body: unknown }[] = [];
  await mock(page, calls);
  await page.goto("/series/4");
  await page.getByRole("button", { name: "Change the cover…" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("Linked: AniList, MangaDex.", { exact: false })).toBeVisible();
  await expect(dialog.getByText("now from AniList").first()).toBeVisible();
  await dialog.getByRole("button", { name: "MangaDex · vol. 1" }).click();
  await dialog.getByRole("button", { name: /Sparks fly/ }).click();
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect.poll(() => calls.find((c) => c.path.endsWith("/series/4/metadata/picks"))?.body).toEqual({
    coverUrl: "https://mangadex.example/v1.jpg",
    description: "Sparks fly when a blacksmith's son goes after the swords taken from his family.",
    from: { coverUrl: "mangadex", description: "mangadex" },
    automatic: [],
  });
});

import { expect, test, type Page } from "@playwright/test";

const stats = { chapterCount: 10, monitoredCount: 10, fileCount: 0, missingCount: 0, cleanedCount: 0, sizeOnDisk: 0, spaceSaved: 0, lastChapter: 10, readCount: 0, inProgressCount: 0 };
const title = (id: number, name: string, language: string, metadata: Record<string, unknown> = {}) => ({
  id, workId: 100 + id, title: name, displayTitle: name, sortTitle: name.toLowerCase(), status: "ongoing", monitored: true, monitorNew: "all", preview: false,
  rootFolderId: 1, path: name, profileId: 1, language, sourcePriorityMode: "inherit", readingDirection: "rtl", tags: [], addOptions: { pending: false },
  addedAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z", coverUrl: "", following: false, sources: [], metadata, stats, adaptations: [],
  editions: [{ id, language, title: name, coverUrl: "" }],
});
// the Russian edition added by an import, with no metadata of its own
const russian = title(7, "Начало после конца", "ru");
const library = [
  title(3, "Solo Leveling", "en"),
  title(1, "The Beginning After the End", "en", { altTitles: ["Начало после конца", "TBATE"] }),
  title(2, "Berserk", "en"),
  russian,
  title(4, "A Returner's Magic", "en", { titles: { ru: "Магия вернувшегося" } }),
];

async function mock(page: Page, onWork: (body: unknown) => void) {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin"] } } });
    if (path.endsWith("/series/7/work")) {
      onWork(route.request().postDataJSON());
      return route.fulfill({ status: 204 });
    }
    if (path.endsWith("/series/7")) return route.fulfill({ json: russian });
    if (path.endsWith("/api/v1/series")) return route.fulfill({ json: library });
    if (path.endsWith("/history")) return route.fulfill({ json: { items: [], total: 0, page: 1, pageSize: 50 } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });
}

test("grouping an edition searches the library by any name, sorted, with the same title on top", async ({ page }) => {
  let body: unknown;
  await mock(page, (b) => (body = b));
  await page.goto("/series/7");
  await page.getByRole("button", { name: "Manage", expanded: false }).click();
  await page.getByRole("menuitem", { name: "Group with another language…" }).click();
  const options = page.getByRole("listbox", { name: "Titles" }).getByRole("option");
  // the title it is the edition of comes first, then the rest by name, and not itself
  await expect(options).toHaveText([/The Beginning After the End/, /A Returner's Magic/, /Berserk/, /Solo Leveling/]);
  await expect(page.getByText("Looks like the same title")).toBeVisible();
  // a name in another language finds a title
  await page.getByRole("combobox", { name: "Search titles…" }).fill("магия");
  await expect(options).toHaveText([/A Returner's Magic.*also “Магия вернувшегося”/]);
  await page.getByRole("combobox", { name: "Search titles…" }).fill("tbate");
  await page.keyboard.press("Enter");
  await expect(page.getByText("Group with The Beginning After the End")).toBeVisible();
  await page.getByRole("button", { name: "Group edition" }).click();
  await expect.poll(() => body).toEqual({ workId: 101 });
});

test("the History title filter is a search with covers", async ({ page }) => {
  await mock(page, () => {});
  await page.goto("/activity/history");
  await page.getByRole("button", { name: "All titles" }).click();
  await page.getByRole("combobox", { name: "Search titles…" }).fill("ber");
  await page.getByRole("option", { name: /Berserk/ }).click();
  await expect(page).toHaveURL(/series=2/);
  await expect(page.getByRole("button", { name: "Berserk" })).toBeVisible();
  await page.getByRole("button", { name: "Clear" }).click();
  await expect(page).not.toHaveURL(/series=/);
});

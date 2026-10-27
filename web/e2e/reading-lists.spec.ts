import { expect, test, type Page } from "@playwright/test";

const series = {
  id: 4, workId: 4, title: "Steel Lantern", sortTitle: "steel lantern", status: "ongoing", monitored: true, monitorNew: "all", preview: false,
  rootFolderId: 1, path: "Steel Lantern", profileId: 1, language: "en", sourcePriorityMode: "inherit", readingDirection: "rtl",
  tags: [], addOptions: { pending: false }, addedAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z", coverUrl: "", following: false, sources: [],
  metadata: { genres: [], tags: [], altTitles: [] }, editions: [{ id: 4, language: "en", title: "Steel Lantern" }],
  stats: { chapterCount: 2, monitoredCount: 2, fileCount: 2, missingCount: 0, cleanedCount: 0, sizeOnDisk: 1, spaceSaved: 0, lastChapter: 2, readCount: 0, inProgressCount: 0 },
  adaptations: [],
};

const statuses = (reading: number, planned: number) => [
  { status: "reading", count: reading }, { status: "planned", count: planned }, { status: "completed", count: 0 }, { status: "onhold", count: 0 }, { status: "dropped", count: 0 },
];

async function mock(page: Page, permissions: string[], saved: { status: string; lists: number[] }[], searches: string[]) {
  let entry = { status: "", lists: [] as number[] };
  let lists = [{ id: 1, name: "Favorites", kind: "favorites", createdAt: "2026-09-01T00:00:00Z", count: 0 }];
  await page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: false, account: { kind: "user", id: 1, username: "ann", group: "Users", groupId: 1, permissions } } });
    if (path.endsWith("/series/4/lists")) {
      if (req.method() === "PUT") {
        entry = req.postDataJSON();
        saved.push(entry);
      }
      return route.fulfill({ json: entry });
    }
    if (path.endsWith("/me/lists") && req.method() === "POST") {
      const made = { id: 2, name: req.postDataJSON().name, kind: "", createdAt: "2026-09-01T00:00:00Z", count: 0 };
      lists = [...lists, made];
      return route.fulfill({ json: made });
    }
    if (path.endsWith("/me/lists")) return route.fulfill({ json: { statuses: statuses(entry.status === "reading" ? 1 : 0, 0), lists: lists.map((l) => ({ ...l, count: entry.lists.includes(l.id) ? 1 : 0 })) } });
    if (path.endsWith("/users/7/lists")) return route.fulfill({ json: { statuses: statuses(0, 3), lists: [{ id: 9, name: "Favorites", kind: "favorites", createdAt: "2026-09-01T00:00:00Z", count: 0 }] } });
    if (path.endsWith("/reading-lists/owners")) return route.fulfill({ json: [{ id: 1, username: "ann" }, { id: 7, username: "bob", displayName: "Bob" }] });
    if (path.endsWith("/series/4/chapters")) return route.fulfill({ json: [] });
    if (path.endsWith("/series/4")) return route.fulfill({ json: series });
    if (path.endsWith("/series/search")) {
      searches.push(url.search);
      return route.fulfill({ json: { page: 1, pageSize: 36, total: 1, totalSize: 1, languages: ["en"], genres: [], genreCounts: {}, items: [{ ...series, coverUrl: "" }] } });
    }
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    return route.fulfill({ json: [] });
  });
}

test("a title goes in one status and any lists, and the library filters by them", async ({ page }) => {
  const saved: { status: string; lists: number[] }[] = [];
  const searches: string[] = [];
  await mock(page, ["requests.create"], saved, searches);
  await page.goto("/series/4");
  await page.getByRole("button", { name: "Add to list" }).click();
  const menu = page.getByRole("menu", { name: "Lists" });
  await menu.getByRole("menuitemradio", { name: "Reading" }).click();
  await expect(page.getByRole("button", { name: "Reading", exact: true })).toBeVisible();
  // one status at a time
  await menu.getByRole("menuitemradio", { name: "Planned" }).click();
  await expect(menu.getByRole("menuitemradio", { name: "Planned" })).toHaveAttribute("aria-checked", "true");
  await expect(menu.getByRole("menuitemradio", { name: "Reading" })).toHaveAttribute("aria-checked", "false");
  await menu.getByRole("menuitemcheckbox", { name: "Favorites" }).click();
  await menu.getByRole("menuitem", { name: "New list…" }).click();
  await menu.getByPlaceholder("List name").fill("Isekai");
  await menu.getByRole("button", { name: "Add" }).click();
  await expect(menu.getByRole("menuitemcheckbox", { name: "Isekai" })).toHaveAttribute("aria-checked", "true");
  expect(saved.at(-1)).toEqual({ status: "planned", lists: [1, 2] });

  // back to reading, then the library's List field
  await menu.getByRole("menuitemradio", { name: "Reading" }).click();
  await page.goto("/");
  await page.getByRole("button", { name: "Filters" }).click();
  const panel = page.locator("#library-filters");
  await expect(panel.getByLabel("Whose lists")).toHaveCount(0);
  await panel.getByLabel("List", { exact: true }).selectOption("reading");
  await expect.poll(() => searches.some((s) => s.includes("list=reading") && !s.includes("listUser"))).toBe(true);
  await expect(page.getByRole("button", { name: "Remove filter List: Reading" })).toBeVisible();
});

test("people who manage the library filter by someone else's lists", async ({ page }) => {
  const searches: string[] = [];
  await mock(page, ["library.manage"], [], searches);
  await page.goto("/");
  await page.getByRole("button", { name: "Filters" }).click();
  const panel = page.locator("#library-filters");
  await panel.getByLabel("Whose lists").selectOption("7");
  await panel.getByLabel("List", { exact: true }).selectOption("planned");
  await expect.poll(() => searches.some((s) => s.includes("list=planned") && s.includes("listUser=7"))).toBe(true);
  await expect(page.getByRole("button", { name: "Remove filter Bob: Planned" })).toBeVisible();
});

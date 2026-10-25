import { expect, test, type Page } from "@playwright/test";

async function mockDiscover(page: Page) {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) {
      await route.fulfill({ json: { authenticated: true, account: { kind: "local", id: 7, username: "reader", permissions: ["requests.create"] } } });
      return;
    }
    if (path.endsWith("/discover")) {
      await route.fulfill({
        json: {
          generatedAt: "2026-09-19T12:00:00Z",
          popularCached: false,
          recommendations: [{ seriesId: 1, title: "Recommended One", language: "en", status: "ongoing", books: 10, unread: 4, changedAt: "2026-09-19T11:00:00Z", coverUrl: "", genres: ["Drama"], matchingGenres: ["Drama"], reason: "matches-genres" }],
          updates: [{ seriesId: 2, title: "Updated One", language: "uk", status: "ongoing", books: 8, unread: 1, changedAt: "2026-09-19T10:00:00Z", coverUrl: "", genres: [], latestChapter: "8", reason: "recent-update" }],
          popular: [{ moduleId: 4, moduleName: "source", sourceId: "catalog", sourceName: "Catalog", title: "Popular One", language: "ru", url: "/popular-one" }],
          sourceErrors: [{ source: "slow", name: "Slow source", error: "timed out" }],
        },
      });
      return;
    }
    if (path.endsWith("/reading/shelf")) {
      await route.fulfill({ json: { user: "reader", items: [] } });
      return;
    }
    if (path.endsWith("/tags")) {
      await route.fulfill({ json: [] });
      return;
    }
    await route.fulfill({ json: {} });
  });
}

test("reader Discover page combines recommendations, updates and source titles", async ({ page }) => {
  await mockDiscover(page);
  await page.goto("/discover");

  await expect(page.getByRole("heading", { name: "Discover", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "Discover", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Recommendations" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Open series: Recommended One" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Recently updated" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Popular from your sources" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Request: Popular One" })).toHaveAttribute("href", "/requests?tab=ask&q=Popular%20One");
  await expect(page.getByText("Some sources are unavailable")).toBeVisible();
  for (const shelf of ["recommendations", "recently-updated", "popular"]) {
    await expect(page.locator(`a[href="/discover/${shelf}"]`)).toHaveText("See all");
  }
});

function libraryItem(id: number, title = `Lantern Journey ${id}`) {
  return { seriesId: id, title, language: "en", status: "ongoing", books: 12, unread: 3, changedAt: "2026-09-19T11:00:00Z", coverUrl: "", genres: ["Adventure"], matchingGenres: ["Adventure"], reason: "matches-genres" };
}

function sourceItem(id: number, existingSeriesId = 0) {
  return { moduleId: 4, moduleName: "source", sourceId: "catalog", sourceName: "Cloud Catalog", title: `Cloudbound Ledger ${id}`, language: "en", url: `/cloud-${id}`, existingSeriesId };
}

test("shelves follow empty continuation pages and stop at the end", async ({ page }) => {
  await mockDiscover(page);
  const cursors: (string | null)[] = [];
  await page.route("**/api/v1/discover/popular?**", async (route) => {
    const cursor = new URL(route.request().url()).searchParams.get("cursor");
    cursors.push(cursor);
    await route.fulfill({ json: { library: [], popular: cursor === null ? Array.from({ length: 36 }, (_, i) => sourceItem(i)) : cursor === "empty" ? [] : [sourceItem(99)], sourceErrors: [], nextCursor: cursor === null ? "empty" : cursor === "empty" ? "last" : undefined } });
  });
  await page.goto("/discover/popular");
  await expect(page.getByRole("link", { name: "Request: Cloudbound Ledger 0", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Load more", exact: true }).scrollIntoViewIfNeeded();
  await expect(page.getByText("You’ve reached the end of this shelf.")).toBeVisible();
  // StrictMode can cancel and replay the first request; continuation requests must not repeat.
  expect(cursors.filter(Boolean)).toEqual(["empty", "last"]);
  await expect(page.getByRole("link", { name: "Request: Cloudbound Ledger 99", exact: true })).toHaveAttribute("href", "/requests?tab=ask&q=Cloudbound%20Ledger%2099");
});

test("filters and sort survive reload, Back and a return to the shelf", async ({ page }) => {
  await mockDiscover(page);
  const queries: URLSearchParams[] = [];
  await page.route("**/api/v1/discover/recommendations?**", async (route) => {
    const params = new URL(route.request().url()).searchParams;
    queries.push(params);
    await route.fulfill({ json: { library: [libraryItem(1)], popular: [], sourceErrors: [] } });
  });
  await page.goto("/discover/recommendations");
  await page.getByRole("button", { name: /^Filters/ }).click();
  await page.getByRole("textbox", { name: "Genre", exact: true }).fill("Adventure");
  await page.getByRole("combobox", { name: "Language", exact: true }).selectOption("uk");
  await page.screenshot({ path: test.info().outputPath("discover-filters-desktop.png") });
  await page.getByRole("combobox", { name: "Format", exact: true }).selectOption("manhwa");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page).toHaveURL(/genre=Adventure/);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /^Filters/ })).toHaveText("Filters3");
  await page.getByRole("combobox", { name: "Sort by" }).selectOption("title");
  await expect(page).toHaveURL(/sort=title/);
  await page.reload();
  await page.getByRole("button", { name: /^Filters/ }).click();
  await expect(page.getByRole("textbox", { name: "Genre", exact: true })).toHaveValue("Adventure");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("combobox", { name: "Sort by" })).toHaveValue("title");
  await page.getByRole("combobox", { name: "Sort by" }).selectOption("newest");
  await page.goBack();
  await expect(page.getByRole("combobox", { name: "Sort by" })).toHaveValue("title");
  await page.getByRole("link", { name: "Back to Discover" }).click();
  await page.locator('a[href="/discover/recommendations"]').click();
  await page.getByRole("button", { name: /^Filters/ }).click();
  await expect(page.getByRole("textbox", { name: "Genre", exact: true })).toHaveValue("Adventure");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("combobox", { name: "Sort by" })).toHaveValue("title");
  expect(queries.some((params) => params.get("lang") === "uk" && params.get("format") === "manhwa" && params.get("genre") === "Adventure")).toBe(true);
  expect(queries.every((params) => !params.has("cursor"))).toBe(true);
  // An explicit shared URL must not inherit the receiver's remembered filters.
  await page.goto("/discover/recommendations?sort=newest");
  await page.getByRole("button", { name: /^Filters/ }).click();
  await expect(page.getByRole("textbox", { name: "Genre", exact: true })).toHaveValue("");
});

test("popular strips unsupported URL filters and offers only supported controls on mobile", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mockDiscover(page);
  await page.route("**/api/v1/discover/popular?**", async (route) => {
    const params = new URL(route.request().url()).searchParams;
    expect(params.has("genre")).toBe(false);
    expect(params.has("format")).toBe(false);
    expect(params.get("sort")).toBe("popularity");
    await route.fulfill({ json: { library: [], popular: [sourceItem(1), sourceItem(2, 9), sourceItem(3), sourceItem(4)], sourceErrors: [] } });
  });
  await page.goto("/discover/popular?genre=Adventure&format=manga&sort=title");
  await expect(page.getByRole("heading", { name: "Popular from your sources" })).toBeVisible();
  await page.getByRole("button", { name: /^Filters/ }).click();
  await expect(page.getByRole("textbox", { name: "Genre", exact: true })).toHaveCount(0);
  await expect(page.getByRole("combobox", { name: "Format", exact: true })).toHaveCount(0);
  await expect(page.getByRole("combobox", { name: "Sort by" }).locator("option")).toHaveText(["Popularity", "Recently updated"]);
  await expect(page.getByRole("link", { name: "Open series: Cloudbound Ledger 2" })).toHaveAttribute("href", "/series/9");
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect(await page.locator("main").evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("discover-popular-phone-filters.png") });
  await page.keyboard.press("Escape");
  await page.screenshot({ path: testInfo.outputPath("discover-popular-phone-grid.png") });
});

test("an expired cursor keeps cards visible and restarts without the cursor", async ({ page }) => {
  await mockDiscover(page);
  let firstPages = 0;
  let continuations = 0;
  await page.route("**/api/v1/discover/recently-updated?**", async (route) => {
    const params = new URL(route.request().url()).searchParams;
    if (params.has("cursor")) {
      continuations++;
      await route.fulfill({ status: 410, json: { detail: "Cursor expired" } });
    } else {
      firstPages++;
      await route.fulfill({ json: { library: [libraryItem(continuations ? 2 : 1)], popular: [], sourceErrors: [], nextCursor: continuations ? undefined : "expired" } });
    }
  });
  await page.goto("/discover/recently-updated");
  await expect(page.getByRole("button", { name: "Restart browsing" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Open series: Lantern Journey 1" })).toBeVisible();
  await page.getByRole("button", { name: "Restart browsing" }).click();
  await expect(page.getByRole("link", { name: "Open series: Lantern Journey 2" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Open series: Lantern Journey 1" })).toHaveCount(0);
  await expect(page.getByText("You’ve reached the end of this shelf.")).toBeVisible();
  expect(firstPages).toBeGreaterThanOrEqual(2);
  expect(continuations).toBe(1);
});

test("initial and continuation failures retry, and source warnings preserve results", async ({ page }) => {
  await mockDiscover(page);
  let failInitial = true;
  let failContinuation = true;
  const cursors: (string | null)[] = [];
  await page.route("**/api/v1/discover/popular?**", async (route) => {
    const cursor = new URL(route.request().url()).searchParams.get("cursor");
    cursors.push(cursor);
    if ((!cursor && failInitial) || (cursor && failContinuation)) {
      await route.fulfill({ status: 503, json: { detail: "Temporarily unavailable" } });
      return;
    }
    const continuation = new URL(route.request().url()).searchParams.has("cursor");
    await route.fulfill({ json: { library: [], popular: [sourceItem(continuation ? 2 : 1)], sourceErrors: continuation ? [] : [{ source: "4:quiet", name: "Quiet Catalog", error: "Timed out" }], nextCursor: continuation ? undefined : "next" } });
  });
  await page.goto("/discover/popular");
  await expect(page.getByText("Temporarily unavailable")).toBeVisible();
  failInitial = false;
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.getByRole("link", { name: "Request: Cloudbound Ledger 1" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Try again" })).toBeVisible();
  failContinuation = false;
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.getByText("You’ve reached the end of this shelf.")).toBeVisible();
  await expect(page.getByText("Some sources are unavailable")).toBeVisible();
  await expect(page.getByRole("link", { name: "Request: Cloudbound Ledger 2" })).toBeVisible();
  expect(cursors.filter(Boolean)).toEqual(["next", "next"]);
});

test("manager shelf cards preserve Add actions and library cards use the shared design", async ({ page }, testInfo) => {
  await mockDiscover(page);
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:local:7", JSON.stringify({ mode: "editing", locale: "en" })));
  await page.route("**/api/v1/auth/status", (route) => route.fulfill({ json: { authenticated: true, account: { kind: "local", id: 7, username: "manager", permissions: ["library.manage"] } } }));
  await page.route("**/api/v1/catalogs", (route) => route.fulfill({ json: { items: [], errors: [], generation: 1 } }));
  await page.route("**/api/v1/rootfolders", (route) => route.fulfill({ json: [] }));
  await page.route("**/api/v1/tags", (route) => route.fulfill({ json: [] }));
  await page.route("**/api/v1/discover/popular?**", (route) => route.fulfill({ json: { library: [], popular: [sourceItem(1)], sourceErrors: [] } }));
  await page.goto("/discover/popular");
  await expect(page.getByRole("link", { name: "Add series: Cloudbound Ledger 1" })).toHaveAttribute("href", "/add/manual/-/sources?title=Cloudbound%20Ledger%201&sq=Cloudbound%20Ledger%201&src=4%3Acatalog");
  await page.route("**/api/v1/discover/recommendations?**", (route) => route.fulfill({ json: { library: Array.from({ length: 12 }, (_, i) => libraryItem(i + 1)), popular: [], sourceErrors: [] } }));
  await page.goto("/discover/recommendations?genre=Adventure&sort=recommended");
  await expect(page.getByRole("link", { name: "Open series: Lantern Journey 1", exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("discover-recommendations-desktop.png") });
});

test("changing filters after paging replaces the grid and starts without a cursor", async ({ page }) => {
  await mockDiscover(page);
  const requests: URLSearchParams[] = [];
  await page.route("**/api/v1/discover/recommendations?**", async (route) => {
    const params = new URL(route.request().url()).searchParams;
    requests.push(params);
    const filtered = params.get("status") === "completed";
    const next = params.has("cursor");
    await route.fulfill({ json: { library: [libraryItem(filtered ? 3 : next ? 2 : 1)], popular: [], sourceErrors: [], nextCursor: !filtered && !next ? "next" : undefined } });
  });
  await page.goto("/discover/recommendations");
  await expect(page.getByRole("link", { name: "Open series: Lantern Journey 2" })).toBeVisible();
  await page.getByRole("button", { name: /^Filters/ }).click();
  await page.getByRole("combobox", { name: "Status", exact: true }).selectOption("completed");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page.getByRole("link", { name: "Open series: Lantern Journey 3" })).toBeVisible();
  await expect(page.getByRole("link", { name: "Open series: Lantern Journey 1" })).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Open series: Lantern Journey 2" })).toHaveCount(0);
  expect(requests.filter((params) => params.has("status")).every((params) => !params.has("cursor"))).toBe(true);
});

test("empty library shelves can clear filters on a phone while keeping their sort", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mockDiscover(page);
  await page.route("**/api/v1/discover/recently-updated?**", async (route) => {
    const empty = new URL(route.request().url()).searchParams.get("inLibrary") === "false";
    await route.fulfill({ json: { library: empty ? [] : [libraryItem(1)], popular: [], sourceErrors: [] } });
  });
  await page.goto("/discover/recently-updated?inLibrary=false&sort=title");
  await expect(page.getByText("No matching titles", { exact: true })).toBeVisible();
  expect(await page.locator("main").evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  await page.getByRole("button", { name: "Clear filters" }).last().click();
  await expect(page.getByRole("link", { name: "Open series: Lantern Journey 1" })).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Sort by" })).toHaveValue("title");
  await expect(page).not.toHaveURL(/inLibrary/);
});

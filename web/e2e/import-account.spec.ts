import { test, expect, type Page } from "@playwright/test";

const account = {
  id: 7, format: "account", fileName: "Senkuro account", status: "review", progress: "", error: "", busy: false,
  createdAt: "2026-10-24T10:00:00Z", updatedAt: "2026-10-24T10:00:00Z",
  options: { onlyFavorites: false, rootFolderId: 1, profileId: 0, monitor: "unread", monitorNew: "all", searchMissing: true, categoryTags: true,
    categories: { Брошено: { skip: true } }, readState: true, readerId: 0, pushProgress: true, matchMetadata: true, findByTitle: false, blockScanlators: false },
  info: { backupDate: "2026-10-24T10:00:00Z", categories: ["Читаю", "Брошено"], sources: { "8010": "Senkuro" }, entries: 1, moduleId: 3,
    lists: { Читаю: "reading", Брошено: "dropped" } },
  counts: { all: 1, ready: 1, selected: 1, "selected:ready": 1 },
};

const entry = {
  id: 1, importId: 7, position: 0, title: "Berserk", state: "ready", selected: true, message: "", updatedAt: "2026-10-24T10:00:00Z", chapterCount: 0, readCount: 111,
  data: { sourceId: "8010", sourceName: "Senkuro", url: "/manga/berserk", title: "Berserk", status: "ongoing", favorite: true, categories: ["Читаю"],
    readUpTo: { chapter: 112, page: 7 } },
  source: { moduleId: 3, sourceId: "8010", sourceName: "Senkuro", lang: "ru", url: "/manga/berserk", title: "Berserk", how: "exact" },
};

/** mockImports serves the import pages; it records the account request. */
async function mockImports(page: Page) {
  const sent: { body?: any } = {};
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin"] } } });
    if (path.endsWith("/imports/accounts") && route.request().method() === "POST") {
      sent.body = route.request().postDataJSON();
      if (sent.body.token !== "good") return route.fulfill({ status: 422, json: { title: "Unprocessable Entity", status: 422, detail: "the site didn't accept the sign-in token; copy a fresh one" } });
      return route.fulfill({ status: 201, json: account });
    }
    if (path.endsWith("/imports/accounts"))
      return route.fulfill({ json: [{ moduleId: 3, sourceId: "8010", name: "Senkuro", lang: "ru", field: "Sign-in token", secret: true,
        steps: ["Sign in at senkuro.me in a computer browser.", "Copy the value of access_token and paste it here."] }] });
    if (path.endsWith("/imports/7/entries")) return route.fulfill({ json: { items: [entry], total: 1, page: 1, pageSize: 50 } });
    if (path.endsWith("/imports/7")) return route.fulfill({ json: account });
    if (path.endsWith("/imports")) return route.fulfill({ json: [] });
    if (path.endsWith("/rootfolders")) return route.fulfill({ json: [{ id: 1, path: "/library/manga" }] });
    return route.fulfill({ json: [] });
  });
  return sent;
}

test("reads a site account into an import and shows its lists", async ({ page }) => {
  const sent = await mockImports(page);
  await page.goto("/import");
  const card = page.locator("section", { has: page.getByRole("heading", { name: "From a site account" }) });
  await expect(card.locator("select")).toHaveValue("3:8010");
  await card.getByText("How to find it").click();
  await expect(card.getByText("Copy the value of access_token and paste it here.")).toBeVisible();

  await card.locator("input[type=password]").fill("bad");
  await card.getByRole("button", { name: "Read my lists" }).click();
  await expect(page.getByText(/didn't accept the sign-in token/)).toBeVisible();

  await card.locator("input[type=password]").fill(" good ");
  await card.getByRole("button", { name: "Read my lists" }).click();
  await expect(page).toHaveURL(/\/import\/7$/);
  expect(sent.body).toEqual({ moduleId: 3, sourceId: "8010", token: "good" });

  await expect(page.getByRole("columnheader", { name: "Manga in the account" })).toBeVisible();
  await expect(page.getByText("reached ch. 112, page 7")).toBeVisible();
  await expect(page.getByText("Lists", { exact: true })).toBeVisible();
  await expect(page.getByText("Search by title when a source can't be matched")).toHaveCount(0);
});

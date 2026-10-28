import { expect, test, type Page } from "@playwright/test";

const side = (id: number, title: string, language: string, extra: Record<string, unknown> = {}) => ({
  id, workId: id, title, language, coverUrl: "", addedAt: "2026-10-24T21:28:47Z", chapters: 259, files: 0, sources: ["Senkuro"], hasMetadata: false, ...extra,
});
const finding = (id: number, kind: string, a: ReturnType<typeof side>, b: ReturnType<typeof side> | undefined, extra: Record<string, unknown> = {}) => ({
  id, kind, seriesA: a.id, seriesB: b?.id ?? 0, keepId: a.id, sameLanguage: false, sure: true, reasons: [], state: "open",
  checkedAt: "2026-10-27T10:00:00Z", updatedAt: "2026-10-27T10:00:00Z", a, b, ...extra,
});
const match = (title: string, id: string, score = 0.95) => ({ moduleId: 1, provider: "anilist", id, title, score, year: 2018 });

const findings = [
  finding(1, "pair", side(98, "The Beginning After the End", "en", { sources: ["MangaDex"], files: 410 }), side(253, "Начало после конца", "ru"), {
    reasons: [{ code: "mal" }, { code: "name", params: { name: "The Beginning After the End" } }],
    match: match("The Beginning After the End", "100"),
  }),
  finding(5, "pair", side(400, "Поднятие уровня в одиночку", "ru", { hasMetadata: true, files: 200 }), side(401, "Solo Leveling", "ru", { files: 14 }), {
    sameLanguage: true, sure: false, reasons: [{ code: "name", params: { name: "Solo Leveling" } }],
  }),
  finding(2, "metadata", side(300, "Возвращение мага 8-го класса", "ru"), undefined, { match: match("The 8th Class Mage Returns", "8", 0.86) }),
  finding(3, "metadata", side(301, "Мой сосед-вампир", "ru"), undefined, { state: "none" }),
  finding(4, "metadata", side(302, "Ранкер, который живёт второй раз", "ru"), undefined, { match: match("Second Life Ranker", "9", 0.93) }),
  finding(6, "primary", side(500, "Absolute Sword Sense", "en", { hasMetadata: true, providers: ["mangadex"] }), undefined, {
    match: { ...match("Absolute Sword Sense", "21", 1), how: "id", by: "mal" },
  }),
  finding(7, "primary", side(501, "Мой сосед-вампир", "ru", { hasMetadata: true, providers: ["mangadex"] }), undefined, { state: "none", sure: false }),
];

async function mock(page: Page, calls: { path: string; body: unknown }[]) {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
  await page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin"] } } });
    if (req.method() !== "GET") {
      calls.push({ path, body: req.postDataJSON() });
      if (path.endsWith("/duplicates/link")) return route.fulfill({ json: { linked: 2 } });
      return route.fulfill({ status: 204 });
    }
    if (path.endsWith("/api/v1/duplicates")) return route.fulfill({ json: { findings, scanning: false, primary: { moduleId: 1, name: "AniList", implementation: "anilist" } } });
    if (path.endsWith("/settings/duplicates")) return route.fulfill({ json: { autoGroup: true } });
    if (path.endsWith("/settings/locks")) return route.fulfill({ json: {} });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });
}

test("a pair in two languages is grouped with the metadata found for it", async ({ page }) => {
  const calls: { path: string; body: unknown }[] = [];
  await mock(page, calls);
  await page.goto("/duplicates");
  const card = page.getByRole("region", { name: "The Beginning After the End · Начало после конца" });
  await expect(card.getByText("Same title, another language")).toBeVisible();
  await expect(card.getByText("Why: MyAnimeList gives both the same id; both are called “The Beginning After the End”.")).toBeVisible();
  await expect(card.getByText("Metadata to link")).toBeVisible();
  await card.getByRole("button", { name: "Group and link metadata" }).click();
  await expect.poll(() => calls.map((c) => c.path)).toContain("/api/v1/duplicates/1/group");
});

test("titles without metadata are linked in a batch", async ({ page }) => {
  const calls: { path: string; body: unknown }[] = [];
  await mock(page, calls);
  await page.goto("/duplicates");
  await page.getByRole("button", { name: /No metadata · 3/ }).click();
  await page.getByRole("checkbox", { name: /Select all/ }).check();
  await expect(page.getByText("2 have a match")).toBeVisible();
  await page.getByRole("button", { name: "Link 2 matches" }).click();
  await expect.poll(() => calls.find((c) => c.path.endsWith("/duplicates/link"))?.body).toEqual({ seriesIds: [302, 300] });
  // the rest are looked up again
  await page.getByRole("checkbox", { name: "Select Мой сосед-вампир" }).check();
  await page.getByRole("button", { name: "Search selected again" }).click();
  await expect.poll(() => calls.find((c) => c.path.endsWith("/duplicates/scan"))?.body).toEqual({ seriesIds: [301] });
});

test("a pair in one language is merged into the title picked to keep", async ({ page }) => {
  const calls: { path: string; body: unknown }[] = [];
  await mock(page, calls);
  await page.goto("/duplicates");
  const card = page.getByRole("region", { name: "Поднятие уровня в одиночку · Solo Leveling" });
  await expect(card.getByText("Same title, same language")).toBeVisible();
  await card.getByRole("button", { name: "Swap which to keep" }).click();
  await page.getByRole("region", { name: "Solo Leveling · Поднятие уровня в одиночку" }).getByRole("button", { name: "Merge into the left one" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Merge", exact: true }).click();
  await expect.poll(() => calls.find((c) => c.path.endsWith("/duplicates/5/merge"))?.body).toEqual({ keepId: 401 });
});

test("titles not on the primary provider are moved to it", async ({ page }) => {
  const calls: { path: string; body: unknown }[] = [];
  await mock(page, calls);
  await page.goto("/duplicates");
  await expect(page.getByRole("heading", { name: "Library check" })).toBeVisible();
  await page.getByRole("button", { name: /Not on AniList · 2/ }).click();
  await expect(page.getByText("Now: MangaDex").first()).toBeVisible();
  await expect(page.getByText("same MyAnimeList id")).toBeVisible();
  await expect(page.getByText("AniList has no entry for it")).toBeVisible();
  await page.getByRole("checkbox", { name: /Select all/ }).check();
  await page.getByRole("button", { name: "Move 1 to AniList" }).click();
  await expect.poll(() => calls.find((c) => c.path.endsWith("/duplicates/link"))?.body).toEqual({ seriesIds: [500], kind: "primary" });
});

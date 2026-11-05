import { expect, test, type Page } from "@playwright/test";

// webtoon pages far taller than the shape a page borrows before it loads
const tall = `<svg xmlns="http://www.w3.org/2000/svg" width="600" height="3000"><rect width="100%" height="100%" fill="#555"/></svg>`;

type Saved = { chapter: number; page: number };

async function mockReader(page: Page, opts: { mode: "webtoon" | "paged"; pages: number; progress?: Record<number, number>; slowPages?: boolean }) {
  const saved: Saved[] = [];
  const marks: { chapter: number; read: boolean; scope: string }[] = [];
  const chapter = (id: number) => ({
    id, seriesId: 7, seriesTitle: "Strip", number: String(id), readingDirection: "webtoon", downloaded: true, canDownload: true,
    pages: Array.from({ length: opts.pages }, (_, i) => ({ number: i + 1, mediaType: "image/svg+xml" })),
    progress: { page: opts.progress?.[id] ?? 0, completed: false },
    ...(id > 1 ? { prev: { id: id - 1, number: String(id - 1) } } : {}),
    ...(id < 5 ? { next: { id: id + 1, number: String(id + 1) } } : {}),
  });
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (/\/read\/chapters\/\d+\/pages\/\d+$/.test(path)) {
      if (opts.slowPages) await new Promise((r) => setTimeout(r, 300));
      return route.fulfill({ contentType: "image/svg+xml", body: tall });
    }
    const progress = path.match(/\/read\/chapters\/(\d+)\/progress$/);
    if (progress) {
      saved.push({ chapter: Number(progress[1]), page: route.request().postDataJSON().page });
      return route.fulfill({ json: {} });
    }
    const mark = path.match(/\/read\/chapters\/(\d+)\/mark$/);
    if (mark) {
      const body = route.request().postDataJSON();
      marks.push({ chapter: Number(mark[1]), read: body.read, scope: body.scope });
      return route.fulfill({ json: { changed: 1 } });
    }
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
    if (path.endsWith("/read/settings")) return route.fulfill({ json: { defaults: { mode: opts.mode, preload: 2, showTapZones: false }, series: null } });
    if (path.endsWith("/read/series/7/chapters")) {
      const read = new Set(marks.filter((m) => m.read).map((m) => m.chapter));
      const items = [1, 2, 3, 4, 5].map((id) => ({ id, number: String(id), numberSort: id, available: true, downloaded: true, completed: read.has(id) }));
      return route.fulfill({ json: { items, total: items.length, page: 1 } });
    }
    const ch = path.match(/\/read\/chapters\/(\d+)$/);
    if (ch && route.request().method() === "GET") return route.fulfill({ json: chapter(Number(ch[1])) });
    return route.fulfill({ json: [] });
  });
  return { saved, marks };
}

test("the chapter list marks a chapter read along with the ones before it, and back", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  const { marks } = await mockReader(page, { mode: "paged", pages: 3 });
  await page.goto("/read/2");
  await page.getByRole("button", { name: "Choose chapter" }).first().click();
  const list = page.locator("[data-current]").locator("..");
  await expect(list.getByRole("button", { name: "Mark read" })).toHaveCount(5);
  // no download marks in the list
  await expect(list.getByLabel("Downloaded")).toHaveCount(0);

  await list.getByRole("button", { name: "Mark read" }).nth(3).click();
  await expect.poll(() => marks).toEqual([
    { chapter: 4, read: true, scope: "chapter" },
    { chapter: 4, read: true, scope: "previous" },
  ]);

  await list.getByRole("button", { name: "Mark unread" }).first().click();
  await expect.poll(() => marks.at(-1)).toEqual({ chapter: 4, read: false, scope: "chapter" });
});

test("webtoon holds its spot while the pages above it load taller", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  const { saved } = await mockReader(page, { mode: "webtoon", pages: 8, progress: { 2: 5 }, slowPages: true });
  await page.goto("/read/2");
  const opened = page.locator('[data-chapter="2"][data-page="5"]');
  await expect(opened.locator("img:not([aria-hidden])")).toHaveCount(1);
  // let every page around it load and grow
  await page.waitForTimeout(2500);
  await expect(page).toHaveURL(/\/read\/2$/);
  const top = await opened.evaluate((el) => el.getBoundingClientRect().top);
  const bottom = await opened.evaluate((el) => el.getBoundingClientRect().bottom);
  // the reading line (a third down the screen) is still on the page it opened at
  expect(top).toBeLessThanOrEqual(800 / 3);
  expect(bottom).toBeGreaterThan(800 / 3);
  expect(saved.filter((s) => s.chapter !== 2)).toEqual([]);
});

test("Previous chapter on a webtoon opens the end of that chapter, not the one after", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  const { saved } = await mockReader(page, { mode: "webtoon", pages: 4, slowPages: true });
  await page.goto("/read/3");
  await page.getByRole("button", { name: /Previous chapter: ch\. 2/ }).click();
  await expect(page).toHaveURL(/\/read\/2\?page=last$/);
  const last = page.locator('[data-chapter="2"][data-page="4"]');
  await expect(last.locator("img:not([aria-hidden])")).toHaveCount(1);
  await page.waitForTimeout(2500);
  // still on chapter 2's last page, and opening it there didn't mark it read
  await expect(page).toHaveURL(/\/read\/2\?page=last$/);
  const box = await last.evaluate((el) => el.getBoundingClientRect().toJSON());
  expect(box.top).toBeLessThanOrEqual(800 / 3);
  expect(box.bottom).toBeGreaterThan(800 / 3);
  expect(saved.filter((s) => s.chapter === 2)).toEqual([]);
});

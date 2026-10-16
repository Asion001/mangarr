import { expect, test } from "@playwright/test";

const image = `<svg xmlns="http://www.w3.org/2000/svg" width="600" height="1200"><rect width="100%" height="100%" fill="#555"/></svg>`;

test("webtoon scrolls on past a divider into the next chapter", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  const progress: { chapter: number; page: number }[] = [];
  const chapter = (id: number) => ({
    id, seriesId: 7, seriesTitle: "Strip", number: String(id), readingDirection: "webtoon", downloaded: true, canDownload: false,
    pages: [1, 2, 3].map((number) => ({ number, mediaType: "image/svg+xml" })),
    progress: { page: 0, completed: false },
    ...(id === 1 ? { next: { id: 2, number: "2", title: "The Gate" } } : { prev: { id: 1, number: "1" } }),
  });
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (/\/read\/chapters\/\d+\/pages\/\d+$/.test(path)) return route.fulfill({ contentType: "image/svg+xml", body: image });
    const saved = path.match(/\/read\/chapters\/(\d+)\/progress$/);
    if (saved) {
      progress.push({ chapter: Number(saved[1]), page: route.request().postDataJSON().page });
      return route.fulfill({ json: {} });
    }
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: [] } } });
    if (path.endsWith("/read/settings")) return route.fulfill({ json: { defaults: { direction: "vertical", keepAwake: false, mode: "webtoon", preload: 2, showTapZones: false }, series: null } });
    const ch = path.match(/\/read\/chapters\/(\d+)$/);
    if (ch && route.request().method() === "GET") return route.fulfill({ json: chapter(Number(ch[1])) });
    return route.fulfill({ json: [] });
  });

  await page.goto("/read/1");
  const divider = page.locator('[data-divider="1"]');
  await expect(divider).toContainText("The Gate");
  await expect(divider.getByRole("button")).toHaveCount(0);

  // the next chapter's pages follow the divider, already loading
  const nextFirst = page.locator('[data-chapter="2"][data-page="1"]');
  await expect(nextFirst.locator("img:not([aria-hidden])")).toHaveCount(1);
  await nextFirst.evaluate((el) => el.scrollIntoView({ block: "start" }));

  await expect(page).toHaveURL(/\/read\/2$/);
  await expect.poll(() => progress.some((p) => p.chapter === 1 && p.page === 3)).toBe(true);

  // the last chapter ends with a way back instead of a next chapter
  await expect(page.locator('[data-divider="2"]').getByRole("link", { name: "Back to the series" })).toBeVisible();
});

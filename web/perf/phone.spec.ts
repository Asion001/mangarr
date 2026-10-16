import { expect, test, type Page } from "@playwright/test";

// Loads the app the way a phone on a slow connection does and prints how
// long each step takes. The API is mocked and answers at once, so the
// numbers are the web app's own cost: downloading, parsing and rendering.
// A warm reload isn't measured: Playwright turns the HTTP cache off while it
// intercepts requests.
// docs/architecture.md, "Phone-profile load times", has the last results.

const runs = Number(process.env.PERF_RUNS ?? 3);
const at = "2026-09-01T00:00:00Z";
const series = Array.from({ length: 60 }, (_, i) => ({
  id: i + 1, workId: i + 1, title: `Series ${i + 1}`, sortTitle: `series ${i + 1}`, status: "ongoing", monitored: true, monitorNew: "all",
  rootFolderId: 1, path: `Series ${i + 1}`, profileId: 1, language: "en", sourcePriorityMode: "inherit", readingDirection: "rtl",
  tags: [], metadata: {}, addOptions: { pending: false }, addedAt: at, updatedAt: at, coverUrl: "", following: false, sources: [], editions: [],
  stats: { chapterCount: 120, monitoredCount: 120, fileCount: 80, missingCount: 40, cleanedCount: 0, sizeOnDisk: 1e9, spaceSaved: 0, lastChapter: 120, readCount: 30, inProgressCount: 1 },
}));
const chapters = Array.from({ length: 120 }, (_, i) => ({
  id: i + 1, seriesId: 1, number: String(i + 1), numberSort: i + 1, title: `Chapter ${i + 1}`, monitored: true, releaseDate: at,
  state: i < 80 ? "downloaded" : "missing", releases: [], readBy: [],
}));

async function mockApi(page: Page) {
  await page.route("**/api/v1/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin", "activity.view", "library.manage", "library.add", "library.edit", "queue.manage"] } } });
    if (path.endsWith("/series/search")) return route.fulfill({ json: { page: 1, pageSize: 60, total: series.length, totalSize: 0, languages: [], genres: [], items: series } });
    if (path.endsWith("/series/1/chapters")) return route.fulfill({ json: chapters });
    if (path.endsWith("/series/1")) return route.fulfill({ json: series[0] });
    if (path.endsWith("/series")) return route.fulfill({ json: series });
    if (path.endsWith("/queue")) return route.fulfill({ json: { total: 0, state: { paused: false }, items: [] } });
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    if (path.endsWith("/updates")) return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/events")) return route.abort();
    return route.fulfill({ json: [] });
  });
}

// Lighthouse's mobile profile: 4x slower CPU, 150 ms round trips, 1.6 Mbit/s down.
async function throttle(page: Page) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Network.enable");
  await cdp.send("Network.emulateNetworkConditions", { offline: false, latency: 150, downloadThroughput: (1.6 * 1024 * 1024) / 8, uploadThroughput: (750 * 1024) / 8 });
  await cdp.send("Emulation.setCPUThrottlingRate", { rate: 4 });
  return cdp;
}

const median = (xs: number[]) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)];

// navigate taps an in-app link, opening the navigation drawer first when the
// link is in it, and times it until the new page shows `done`.
async function navigate(page: Page, href: string, done: () => Promise<void>) {
  const link = page.locator(`a[href="${href}"]`).first();
  if (!(await link.count())) await page.getByRole("button", { name: "Open navigation" }).click();
  const start = Date.now();
  await link.evaluate((a: HTMLAnchorElement) => a.click());
  await done();
  return Date.now() - start;
}

// pageShown waits for the URL and the new page's heading.
async function pageShown(page: Page, url: RegExp, heading: string) {
  await expect(page).toHaveURL(url, { timeout: 60_000 });
  await expect(page.locator("main").getByRole("heading", { name: heading }).first()).toBeVisible({ timeout: 60_000 });
}

test("phone profile: cold load and route transitions", async ({ page }) => {
  test.setTimeout(runs * 120_000);
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
  await mockApi(page);
  const cdp = await throttle(page);
  const results: Record<string, number[]> = {};
  const record = (name: string, ms: number) => (results[name] ??= []).push(Math.round(ms));

  for (let run = 0; run < runs; run++) {
    await cdp.send("Network.clearBrowserCache");
    const start = Date.now();
    await page.goto("/");
    await expect(page.getByText("Series 1", { exact: true }).first()).toBeVisible({ timeout: 60_000 });
    record("cold load: library visible", Date.now() - start);
    const paint = await page.evaluate(() => performance.getEntriesByName("first-contentful-paint")[0]?.startTime ?? 0);
    record("cold load: first contentful paint", paint);
    const kb = await page.evaluate(() => performance.getEntriesByType("resource").filter((r) => /\.(js|css)$/.test(r.name)).reduce((n, r) => n + (r as PerformanceResourceTiming).transferSize, 0) / 1024);
    record("cold load: JS and CSS transferred (kB)", kb);

    record("to series page", await navigate(page, "/series/1", () => expect(page.getByText("Chapter 1", { exact: true }).first()).toBeVisible({ timeout: 60_000 })));
    record("to Updates", await navigate(page, "/updates", () => pageShown(page, /\/updates$/, "Updates")));
    record("to Activity", await navigate(page, "/activity/downloads", () => pageShown(page, /\/activity\/downloads$/, "Download queue")));
    record("to Settings", await navigate(page, "/settings/media", () => pageShown(page, /\/settings\/media$/, "Media management")));
    record("back to the library", await navigate(page, "/", () => expect(page.getByText("Series 1", { exact: true }).first()).toBeVisible({ timeout: 60_000 })));
  }

  const lines = Object.entries(results).map(([name, xs]) => `| ${name} | ${median(xs)} | ${Math.min(...xs)}–${Math.max(...xs)} |`);
  console.log(`\n| Step (median of ${runs} runs) | ms or kB | range |\n|---|---|---|\n${lines.join("\n")}\n`);
});

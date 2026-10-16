import { expect, test, type Page } from "@playwright/test";

// The README screenshots: a made-up library with drawn covers, so no real
// titles or artwork end up in the repository. npm run screenshots rewrites
// docs/screenshots/*.png.

const out = (name: string) => `../docs/screenshots/${name}.png`;
const day = 86_400_000;
const now = Date.now();
const iso = (msAgo: number) => new Date(now - msAgo).toISOString();

const titles: [string, string, string[]][] = [
  ["Steel Lantern", "ongoing", ["Action", "Fantasy"]],
  ["The Quiet Orchard", "completed", ["Slice of Life", "Drama"]],
  ["Ninth Moon Courier", "ongoing", ["Adventure", "Sci-Fi"]],
  ["Paper Cranes at Dusk", "ongoing", ["Romance", "Drama"]],
  ["Hollow Crown Academy", "ongoing", ["Fantasy", "School"]],
  ["Saltwater Requiem", "hiatus", ["Mystery", "Thriller"]],
  ["Glass Garden", "completed", ["Romance"]],
  ["Ember & Ash", "ongoing", ["Action", "Supernatural"]],
  ["Last Train to Kesuna", "ongoing", ["Horror", "Mystery"]],
  ["Kitchen of the Seven Winds", "ongoing", ["Comedy", "Cooking"]],
  ["Starfall Cartographer", "ongoing", ["Adventure", "Fantasy"]],
  ["The Lighthouse Keeper's Daughter", "completed", ["Drama", "Historical"]],
  ["Velvet Static", "ongoing", ["Music", "Romance"]],
  ["Iron Lotus", "ongoing", ["Martial Arts", "Action"]],
  ["A Thousand Paper Doors", "ongoing", ["Fantasy", "Mystery"]],
  ["Northbound", "completed", ["Adventure"]],
  ["Clockwork Heron", "ongoing", ["Steampunk", "Action"]],
  ["Moss and Marrow", "ongoing", ["Horror", "Fantasy"]],
];

// A drawn cover: a two-colour gradient, a few shapes and the title.
function cover(id: number, title: string) {
  const hue = (id * 47) % 360;
  const words = title.split(" ");
  const lines: string[] = [];
  for (const w of words) {
    const last = lines.length - 1;
    if (last >= 0 && (lines[last] + " " + w).length <= 11) lines[last] += " " + w;
    else lines.push(w);
  }
  const text = lines.map((l, i) => `<text x="24" y="${330 - (lines.length - 1 - i) * 34}" font-family="Georgia, serif" font-size="30" font-weight="700" fill="#fff">${l.replace(/&/g, "&amp;")}</text>`).join("");
  return `<svg xmlns="http://www.w3.org/2000/svg" width="300" height="450" viewBox="0 0 300 450">
  <defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="hsl(${hue},55%,42%)"/><stop offset="1" stop-color="hsl(${(hue + 60) % 360},60%,16%)"/></linearGradient></defs>
  <rect width="300" height="450" fill="url(#g)"/>
  <circle cx="${200 + (id % 3) * 20}" cy="${110 + (id % 4) * 15}" r="${60 + (id % 5) * 10}" fill="hsl(${(hue + 180) % 360},70%,70%)" opacity="0.35"/>
  <path d="M0 ${260 + (id % 3) * 10} Q150 ${200 + (id % 4) * 20} 300 ${250 + (id % 2) * 30} V450 H0 Z" fill="#000" opacity="0.35"/>
  ${text}
  <text x="24" y="410" font-family="Helvetica, Arial, sans-serif" font-size="13" letter-spacing="3" fill="#fff" opacity="0.8">VOL. ${(id % 9) + 1}</text>
</svg>`;
}

const series = titles.map(([title, status, genres], i) => {
  const id = i + 1;
  const chapterCount = 20 + ((id * 37) % 140);
  const fileCount = status === "completed" ? chapterCount : chapterCount - (id % 4);
  const readCount = Math.floor(fileCount * ((id * 13) % 100) / 100);
  return {
    id, workId: id, title, sortTitle: title.toLowerCase(), status, monitored: true, monitorNew: "all", preview: false,
    rootFolderId: 1, path: title, profileId: 1, language: "en", sourcePriorityMode: "inherit", readingDirection: "rtl",
    tags: [], addOptions: { pending: false }, addedAt: iso((40 - id) * day), updatedAt: iso(id * 3_600_000),
    coverUrl: `demo/cover/${id}.svg`, following: id % 3 === 0, sources: [], adaptations: [],
    metadata: {
      format: "manga", year: 2016 + (id % 9), authors: ["Mika Tsuruga"], artists: ["Mika Tsuruga"], altTitles: [],
      genres, tags: [], description: "", links: {},
    },
    editions: [{ id, language: "en", title, coverUrl: `demo/cover/${id}.svg` }],
    stats: { chapterCount, monitoredCount: chapterCount, fileCount, missingCount: chapterCount - fileCount, cleanedCount: 0, sizeOnDisk: fileCount * 38_000_000, spaceSaved: fileCount * 21_000_000, lastChapter: chapterCount, readCount, inProgressCount: readCount < fileCount ? 1 : 0 },
  };
});

const lantern = {
  ...series[0],
  metadata: {
    ...series[0].metadata, year: 2023, ageRating: "16+", altTitles: ["Stahllaterne", "Сталевий ліхтар"],
    genres: ["Action", "Fantasy"], tags: ["Swords", "Revenge", "Blacksmith"],
    description: "Kaito grew up hammering horseshoes in his father's forge, until the night soldiers took every blade the old smith had ever made. Now he follows the trail of stolen steel across a country at war, one sword at a time, with a lantern that should not still be burning.",
    links: { AniList: "https://anilist.example/manga/1" },
  },
  editions: [
    { id: 1, language: "en", title: "Steel Lantern", coverUrl: "demo/cover/1.svg" },
    { id: 40, language: "uk", title: "Сталевий ліхтар", coverUrl: "demo/cover/1.svg" },
  ],
  reading: { readers: [], nextUnread: { chapterId: 47, number: "47" } },
  sources: [
    { id: 1, seriesId: 1, moduleId: 1, sourceId: "a", sourceName: "Weekly Reader", lang: "en", mangaUrl: "/steel-lantern", title: "Steel Lantern", webUrl: "", priority: 0, enabled: true, checkIntervalMinutes: 0, nextCheckAt: iso(-3_600_000), consecutiveFailures: 0, lastError: "", lastSuccessAt: iso(2 * 3_600_000), createdAt: iso(40 * day), chapters: 52, files: 44 },
    { id: 2, seriesId: 1, moduleId: 1, sourceId: "b", sourceName: "Lantern Scans", lang: "en", mangaUrl: "/sl", title: "Steel Lantern", webUrl: "", priority: 1, enabled: true, checkIntervalMinutes: 0, nextCheckAt: iso(-3_600_000), consecutiveFailures: 0, lastError: "", lastSuccessAt: iso(2 * 3_600_000), createdAt: iso(40 * day), chapters: 50, files: 2 },
  ],
  stats: { chapterCount: 52, monitoredCount: 52, fileCount: 50, missingCount: 2, cleanedCount: 0, sizeOnDisk: 1_740_000_000, spaceSaved: 960_000_000, lastChapter: 52, readCount: 46, inProgressCount: 1 },
};

const chapters = Array.from({ length: 52 }, (_, i) => {
  const n = 52 - i;
  const downloaded = n <= 50;
  const release = { id: n, seriesId: 1, chapterId: n, seriesSourceId: 1, chapterUrl: `/c/${n}`, webUrl: "", name: `Ch. ${n}`, scanlator: "Weekly Reader", rawNumber: n, uploadDate: iso((52 - n) * 7 * day + 2 * day), removed: false, createdAt: iso((52 - n) * 7 * day), sourceName: "Weekly Reader", priority: 0, blocklisted: false };
  return {
    id: n, seriesId: 1, number: String(n), numberSort: n, volume: "", title: n === 52 ? "The Forge Remembers" : n === 51 ? "Smoke Over Harrow" : n === 50 ? "A Blade With No Name" : "",
    monitored: true, state: downloaded ? "imported" : n === 51 ? "downloading" : "missing",
    releaseDate: iso((52 - n) * 7 * day + 2 * day), firstSeenAt: iso((52 - n) * 7 * day), updatedAt: iso((52 - n) * 7 * day),
    releases: [release],
    readBy: n < 46 ? [{ readerId: 1, reader: "You", completed: true, page: 0, readAt: iso((52 - n) * 6 * day) }] : n === 46 ? [{ readerId: 1, reader: "You", completed: false, page: 12, readAt: iso(day) }] : [],
    file: downloaded ? { id: n, seriesId: 1, chapterId: n, relativePath: `Steel Lantern Ch.${String(n).padStart(4, "0")}.cbz`, size: 38_000_000, sizeBefore: 59_000_000, sizeOriginal: 59_000_000, format: "avif", pageCount: 24, avgWidth: 1600, sha256: "", importedAt: iso((52 - n) * 7 * day), scanlator: "Weekly Reader", sourceName: "Weekly Reader", upscaled: n > 40, upscaleModel: "", processAttempts: 0, processState: "done" } : undefined,
    job: n === 51 ? { id: 9, status: "downloading", priority: 0, progress: 0.42, pagesDone: 10, pagesTotal: 24 } : undefined,
  };
});

async function mock(page: Page) {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
  await page.route("**/demo/cover/*.svg", (route) => {
    const id = Number(new URL(route.request().url()).pathname.match(/(\d+)\.svg$/)![1]);
    route.fulfill({ contentType: "image/svg+xml", body: cover(id, titles[id - 1][0]) });
  });
  await page.route("**/api/v1/**", (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin", "activity.view", "library.manage", "library.add", "library.edit", "queue.manage"] } } });
    if (path.endsWith("/series/search")) return route.fulfill({ json: { page: 1, pageSize: 36, total: series.length, totalSize: series.reduce((n, s) => n + s.stats.sizeOnDisk, 0), languages: ["en"], genres: [], items: series } });
    if (path.endsWith("/series/1/chapters")) return route.fulfill({ json: chapters });
    if (path.endsWith("/series/1")) return route.fulfill({ json: lantern });
    if (path.endsWith("/series")) return route.fulfill({ json: series });
    if (path.endsWith("/rootfolders")) return route.fulfill({ json: [{ id: 1, path: "/data/manga/en", language: "en" }] });
    if (path.endsWith("/queue")) return route.fulfill({ json: { total: 1, state: { paused: false }, items: [] } });
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    if (path.endsWith("/events")) return route.abort();
    return route.fulfill({ json: [] });
  });
}

test("library", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await mock(page);
  await page.goto("/");
  await expect(page.getByText("Steel Lantern").first()).toBeVisible();
  await expect.poll(() => page.evaluate(() => [...document.images].filter((i) => i.src.includes("demo/cover")).every((i) => i.complete && i.naturalWidth > 0))).toBe(true);
  await page.screenshot({ path: out("library") });
});

test("title page", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 });
  await mock(page);
  await page.goto("/series/1");
  await expect(page.getByText("The Forge Remembers").filter({ visible: true }).first()).toBeVisible();
  await page.waitForTimeout(300);
  await page.screenshot({ path: out("title") });
});

test("title page on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mock(page);
  await page.goto("/series/1");
  await expect(page.getByText("Steel Lantern").first()).toBeVisible();
  await page.waitForTimeout(300);
  await page.screenshot({ path: out("title-phone") });
});

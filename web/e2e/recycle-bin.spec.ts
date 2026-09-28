import { expect, test } from "@playwright/test";

const file = (pages: number, extra: Record<string, unknown> = {}) => ({
  id: 7, chapterId: 44, seriesId: 3, relativePath: "Tower of Ash Ch.0044.cbz", size: 39_700_000, pageCount: pages, avgWidth: 800, format: "jpeg",
  sha256: "x", sizeBefore: 0, sizeOriginal: 0, sourceName: "MangaDex", scanlator: "Ashen Scans", upscaled: false, upscaleModel: "",
  processAttempts: 0, importedAt: "2026-09-02T10:00:00Z", ...extra,
});

const item = {
  id: 11, seriesId: 3, chapterId: 44, chapterFileId: 7, kind: "file", reason: "reprocessed", jobId: 90, jobKind: "reprocess", profileName: "Webtoon HD",
  processParams: "", seriesTitle: "Tower of Ash", originalRelativePath: "Tower of Ash Ch.0044.cbz", recycledPath: "Tower of Ash/20260926-140211.000000000-Tower of Ash Ch.0044.cbz",
  size: 39_700_000, pageCount: 14, sha256: "abc", sourceName: "MangaDex", scanlator: "Ashen Scans", recycledAt: "2026-09-26T14:02:11Z",
  chapter: "44", file: file(14), current: file(41, { upscaled: true, upscaleModel: "realcugan", format: "avif" }), purgeAt: "2099-10-03T14:02:11Z", countChanged: true,
};

for (const width of [1280, 390]) {
  test(`recycle bin lists, compares and restores at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
    const calls: { method: string; path: string; body?: unknown }[] = [];
    await page.route("**/api/v1/**", async (route) => {
      const req = route.request();
      const path = new URL(req.url()).pathname;
      if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin"] } } });
      if (path.endsWith("/recycle-bin") && req.method() === "GET") {
        return route.fulfill({ json: { items: [item], groups: [{ seriesId: 3, seriesTitle: "Tower of Ash", count: 1, size: item.size }], reasons: { reprocessed: 1 }, total: 1, size: item.size, page: 1, pageSize: 50, retentionDays: 7, retentionLocked: false } });
      }
      if (path.endsWith("/recycle-bin/restore")) {
        calls.push({ method: req.method(), path, body: req.postDataJSON() });
        return route.fulfill({ json: [{ id: 11, seriesId: 3 }] });
      }
      if (path.endsWith("/settings/media")) return route.fulfill({ json: { recycleBinDays: 7, recycleBinPath: "" } });
      if (path.endsWith("/system/tasks")) return route.fulfill({ json: [] });
      if (path.includes("/cover") || path.includes("/pages/")) return route.fulfill({ status: 404 });
      if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
      if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
      return route.fulfill({ json: [] });
    });
    await page.goto("/system/recycle-bin");

    const row = page.getByRole("row").filter({ hasText: "Ch. 44" });
    await expect(row).toContainText("Reprocessed");
    await expect(row).toContainText("14 → 41 now");
    await expect(row).toContainText("Count changed");
    await expect(page.getByRole("button", { name: "Reprocessed · 1" })).toBeVisible();
    await expect(page.getByLabel("Keep recycled files for")).toHaveValue("7");
    await page.screenshot({ path: testInfo.outputPath(`recycle-${width}.png`), fullPage: true });

    await row.getByRole("button", { name: "Open" }).click();
    const drawer = page.getByRole("dialog");
    await expect(drawer).toContainText("Compare versions");
    await expect(drawer).toContainText("Page count changed from 14 to 41");
    await expect(drawer.getByRole("link", { name: "Open in reader" }).first()).toHaveAttribute("href", "/recycle-bin/11/read");
    await page.screenshot({ path: testInfo.outputPath(`recycle-drawer-${width}.png`), fullPage: true });
    await drawer.getByRole("button", { name: "Restore this version" }).click();
    await expect.poll(() => calls[0]?.body).toEqual({ ids: [11] });
    await expect(drawer).toBeHidden();

    await row.getByRole("checkbox").check();
    await page.getByRole("button", { name: "Reprocess from these…" }).click();
    await expect(page.getByRole("dialog")).toContainText("Start from");
    await page.screenshot({ path: testInfo.outputPath(`recycle-reprocess-${width}.png`), fullPage: true });
  });
}

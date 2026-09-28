import { expect, test } from "@playwright/test";

const tasks = () => [
  {
    name: "Housekeeping", description: "Purge recycle bin, old queue entries, commands and caches", scheduled: true,
    paused: true, custom: true, intervalMinutes: 0, minIntervalMinutes: 15,
    schedule: { kind: "daily", timesOfDay: ["03:30"], weekdays: [] }, defaultSchedule: { kind: "interval", intervalMinutes: 1440 },
    nextExecution: null, nextRuns: [], timezone: "Europe/Warsaw",
    lastRun: { status: "completed", durationMs: 38000, message: "Finished", error: "", endedAt: "2026-09-24T05:00:00Z" },
  },
  {
    name: "RefreshMetadata", description: "Refresh series metadata from metadata modules", scheduled: true,
    paused: false, custom: false, intervalMinutes: 1440, minIntervalMinutes: 15,
    schedule: { kind: "interval", intervalMinutes: 1440 }, defaultSchedule: { kind: "interval", intervalMinutes: 1440 },
    nextExecution: "2026-09-29T05:00:00Z", nextRuns: ["2026-09-29T05:00:00Z"], timezone: "Europe/Warsaw",
    lastRun: { status: "failed", durationMs: 280000, message: "", error: "3 of 212 series could not be refreshed", endedAt: "2026-09-24T05:04:00Z" },
  },
  { name: "LibraryRescan", description: "Ask library servers to rescan every series folder", scheduled: false, paused: false, custom: false, intervalMinutes: 0, minIntervalMinutes: 0, nextExecution: null, nextRuns: [], timezone: "Europe/Warsaw" },
];

for (const width of [1280, 390]) {
  test(`Tasks page controls at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
    const calls: { method: string; path: string; body?: unknown }[] = [];
    await page.route("**/api/v1/**", async (route) => {
      const req = route.request();
      const path = new URL(req.url()).pathname;
      if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin"] } } });
      if (path.endsWith("/system/tasks")) return route.fulfill({ json: tasks() });
      if (path.endsWith("/system/tasks/preview")) return route.fulfill({ json: { nextRuns: ["2026-09-29T02:00:00Z", "2026-10-01T02:00:00Z"], timezone: "Europe/Warsaw" } });
      if (path.includes("/system/tasks/") || (path.endsWith("/commands") && req.method() === "POST")) {
        calls.push({ method: req.method(), path, body: req.postDataJSON() ?? undefined });
        return route.fulfill(path.endsWith("/commands") ? { json: { id: 1, name: "RefreshMetadata", status: "queued" } } : { status: 204 });
      }
      if (path.endsWith("/commands")) return route.fulfill({ json: [
        { id: 9, name: "RefreshMetadata", body: {}, status: "failed", trigger: "scheduled", message: "", queuedAt: "2026-09-24T05:00:00Z", startedAt: "2026-09-24T05:00:00Z", endedAt: "2026-09-24T05:04:40Z", durationMs: 280000, error: "3 of 212 series could not be refreshed" },
        { id: 8, name: "RefreshMetadata", body: {}, status: "completed", trigger: "manual · roma", message: "Refreshed 212 series", queuedAt: "2026-09-23T21:17:00Z", startedAt: "2026-09-23T21:17:00Z", endedAt: "2026-09-23T21:21:00Z", durationMs: 238000, error: "" },
      ] });
      if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
      if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
      return route.fulfill({ json: [] });
    });
    await page.goto("/system/tasks");

    await expect(page.getByText("Housekeeping is paused")).toBeVisible();
    const housekeeping = page.getByRole("row").filter({ hasText: "Housekeeping" });
    await expect(housekeeping).toContainText("Daily at 03:30");
    await expect(housekeeping).toContainText("Custom");
    const metadata = page.getByRole("row").filter({ hasText: "RefreshMetadata" });
    await expect(metadata).toContainText("Every 24 hours");
    await expect(metadata).toContainText("Failed: 3 of 212 series");
    await expect(page.getByText("LibraryRescan")).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`tasks-${width}.png`), fullPage: true });

    await metadata.getByRole("switch").click();
    await expect.poll(() => calls.map((c) => `${c.method} ${c.path}`)).toContain("POST /api/v1/system/tasks/RefreshMetadata/pause");
    await metadata.getByRole("button", { name: "Run now" }).click();
    await expect.poll(() => calls.some((c) => c.path.endsWith("/commands"))).toBe(true);

    await metadata.getByRole("button", { name: "Edit schedule" }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("radio", { name: "At a time of day" }).check();
    await dialog.getByRole("button", { name: "Sat" }).click();
    await dialog.getByRole("button", { name: "Sun" }).click();
    await expect(dialog.getByText("Next runs")).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`tasks-edit-${width}.png`), fullPage: true });
    await dialog.getByRole("button", { name: "Save" }).click();
    await expect.poll(() => calls.find((c) => c.method === "PUT")?.body).toEqual({ kind: "daily", timesOfDay: ["03:30"], weekdays: ["mon", "tue", "wed", "thu", "fri"] });

    await metadata.getByRole("button", { name: "Run history" }).click();
    const drawer = page.getByRole("dialog");
    await expect(drawer).toContainText("1 ok · 1 failed");
    await expect(drawer).toContainText("Manual · roma");
    await expect(drawer.getByRole("link", { name: "Open in Logs" })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`tasks-history-${width}.png`), fullPage: true });
  });
}

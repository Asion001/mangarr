import { expect, test } from "@playwright/test";

const now = new Date().toISOString();
const models = [
  { name: "waifu2x-cunet", description: "", scales: [2, 4, 8] },
  { name: "realesrgan-x4plus-anime", description: "", scales: [4] },
];
const worker = {
  id: 7, name: "GPU box", prefix: "mgw_abcdef12", roles: ["upscale"], enabled: true, priority: 100, concurrent: 0, upscaleModel: "",
  version: "1.0", platform: "linux/amd64", info: { models, devices: ["RTX"] }, lastIp: "10.0.0.2", createdAt: now, lastSeenAt: now,
  tasksDone: 3, tasksFailed: 0, pagesDone: 24, bytesIn: 1000, bytesOut: 2000, busySeconds: 10, online: true,
  recent: { tasks: 1, failed: 0, pages: 8, bytesIn: 100, bytesOut: 200, seconds: 5 },
};
const local = {
  id: 3, kind: "upscale", implementation: "local", name: "Built-in (this server)", enabled: true, priority: 110, tags: [], events: [],
  settings: { toolsDir: "/opt/upscalers", gpu: "auto" }, capabilities: [],
};
const pool = { ...local, id: 4, implementation: "workers", name: "Workers", priority: 20, settings: {} };

test("this server and the remote workers share one list, each with its own upscale model", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
  const puts: { path: string; body: Record<string, unknown> }[] = [];
  await page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (req.method() === "PUT") {
      puts.push({ path, body: req.postDataJSON() });
      return route.fulfill({ json: {} });
    }
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin"] } } });
    if (path.endsWith("/workers")) return route.fulfill({ json: [worker] });
    if (path.endsWith("/modules/3/upscaler-info")) return route.fulfill({ json: { version: "1", devices: ["Intel iGPU"], models } });
    if (path.endsWith("/modules")) return route.fulfill({ json: [local, pool] });
    if (path.endsWith("/settings/downloads")) return route.fulfill({ json: { maxConcurrentProcessing: 4, maxWorkerTasks: 8, maxConcurrentPerWorker: 2 } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });

  await page.goto("/system/workers");
  const rows = page.getByRole("table").locator("tbody > tr");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0)).toContainText("This server");
  await expect(rows.nth(0)).toContainText("Intel iGPU");
  await expect(rows.nth(1)).toContainText("GPU box");
  await expect(page.getByText("Processing engines")).toHaveCount(0);

  await rows.nth(1).getByRole("combobox", { name: "Upscale model" }).selectOption("realesrgan-x4plus-anime");
  await expect.poll(() => puts.find((p) => p.path.endsWith("/workers/7"))?.body).toEqual({ upscaleModel: "realesrgan-x4plus-anime" });

  await rows.nth(0).getByRole("combobox", { name: "Upscale model" }).selectOption("waifu2x-cunet");
  await expect.poll(() => (puts.find((p) => p.path.endsWith("/modules/3"))?.body.settings as Record<string, unknown> | undefined)?.model).toBe("waifu2x-cunet");
  await page.screenshot({ path: process.env.WORKERS_SHOT ?? "test-results/workers.png", fullPage: true });
});

test("a worker doing many tasks shows a short summary that unfolds", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
  const busy = Array.from({ length: 6 }, (_, i) => ({
    taskId: i + 1, kind: i === 0 ? "download" : "encode", series: "Long Title", chapter: String(i + 1),
    pagesDone: i === 0 ? 3 : 0, pagesTotal: 12, bytesIn: 1000, started: now,
  }));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin"] } } });
    if (path.endsWith("/workers")) return route.fulfill({ json: [{ ...worker, busy }] });
    if (path.endsWith("/modules")) return route.fulfill({ json: [pool] });
    if (path.endsWith("/settings/downloads")) return route.fulfill({ json: { maxConcurrentProcessing: 4, maxWorkerTasks: 8, maxConcurrentPerWorker: 2 } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });

  await page.goto("/system/workers");
  const row = page.getByRole("table").locator("tbody > tr", { hasText: "GPU box" });
  await expect(row).toContainText("6 tasks: 1 download, 5 encode");
  await expect(row.getByText("Long Title", { exact: false })).toHaveCount(2);
  await expect(row).toContainText("3/12");
  await row.getByRole("button", { name: "Show all 6" }).click();
  await expect(row.getByText("Long Title", { exact: false })).toHaveCount(6);
  await row.getByRole("button", { name: "Show less" }).click();
  await expect(row.getByText("Long Title", { exact: false })).toHaveCount(2);
});

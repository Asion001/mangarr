import { expect, test, type Page } from "@playwright/test";

type Move = { action: string; ids?: number[]; anchorId?: number; filter?: unknown };
const job = (id: number, status = "queued", seriesId = 1) => ({
  id, kind: "download", seriesId, seriesTitle: seriesId === 1 ? "Demo Series A" : "Demo Series B", chapter: `Chapter ${id}`,
  numberSort: id, sourceName: "Demo source", scanlator: "", status, rank: id * 1024, priority: 99,
  progress: 35, pagesDone: 7, pagesTotal: 20, attempt: 0, isUpgrade: false, error: "",
  createdAt: "2026-09-20T00:00:00Z", updatedAt: "2026-09-20T00:00:00Z",
});

async function fixture(page: Page, options: { large?: boolean; conflict?: boolean; fail?: boolean } = {}) {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
  let jobs = options.large ? Array.from({ length: 102 }, (_, i) => job(i + 1)) : [job(9, "downloading"), job(1), job(2, "paused", 2), job(3), job(10, "failed")];
  let revision = 7;
  const moves: Move[] = [];
  const requests: URL[] = [];
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin"] } } });
    if (path.endsWith("/queue/bulk")) {
      const move = route.request().postDataJSON() as Move;
      moves.push(move);
      if (options.fail) return route.fulfill({ status: 400, json: { detail: "Demo move rejected" } });
      const selected = jobs.filter((j) => move.ids?.includes(j.id) && ["queued", "paused"].includes(j.status));
      if (move.action === "sort") {
        const slots = jobs.flatMap((j, i) => selected.includes(j) ? [i] : []);
        const ordered = [...selected].sort((a, b) => a.numberSort - b.numberSort);
        slots.forEach((slot, i) => (jobs[slot] = ordered[i]));
        jobs = jobs.map((j, i) => ({ ...j, rank: (i + 1) * 1024 }));
        revision++;
        return route.fulfill({ json: { affected: selected.length } });
      }
      const rest = jobs.filter((j) => !selected.includes(j));
      let index = move.action === "top" ? rest.findIndex((j) => ["queued", "paused"].includes(j.status)) : rest.findIndex((j) => j.status === "failed");
      if (move.anchorId) index = rest.findIndex((j) => j.id === move.anchorId) + (move.action === "after" ? 1 : 0);
      rest.splice(index < 0 ? rest.length : index, 0, ...selected);
      jobs = rest.map((j, i) => ({ ...j, rank: (i + 1) * 1024 }));
      revision++;
      return route.fulfill({ json: { affected: selected.length } });
    }
    if (path.endsWith("/queue")) {
      requests.push(url);
      if (options.conflict && url.searchParams.has("revision")) return route.fulfill({ status: 409, json: { detail: "Queue order changed" } });
      const pageNumber = Number(url.searchParams.get("page") || 1);
      const size = Number(url.searchParams.get("pageSize") || 100);
      const kind = url.searchParams.get("kind") || "download";
      return route.fulfill({ json: { revision, page: pageNumber, pageSize: size, total: jobs.length, counts: Object.fromEntries(["queued", "paused", "downloading", "failed"].map((status) => [status, jobs.filter((j) => j.status === status).length])), state: { paused: false, quiet: {} }, items: jobs.slice((pageNumber - 1) * size, pageNumber * size).map((j) => ({ ...j, kind })) } });
    }
    if (path.endsWith("/reading/shelf")) return route.fulfill({ json: { items: [] } });
    if (path.endsWith("/health")) return route.fulfill({ json: { checks: [] } });
    return route.fulfill({ json: [] });
  });
  return { moves, requests };
}
const row = (page: Page, id: number) => page.getByRole("row").filter({ has: page.getByRole("cell", { name: new RegExp(`^Chapter ${id}( process)?$`) }) });
const toolbar = (page: Page, name: string) => page.getByRole("button", { name, exact: true });

for (const mode of ["downloads", "processing"]) {
  test(`${mode}: positions, no row buttons, toolbar moves the selection`, async ({ page }, testInfo) => {
    const { moves } = await fixture(page);
    await page.goto(`/activity/${mode}?group=flat`);
    await expect(row(page, 9)).toContainText("now");
    await expect(row(page, 1).getByRole("cell").nth(1)).toHaveText("1");
    await expect(row(page, 3).getByRole("cell").nth(1)).toHaveText("3");
    await expect(row(page, 1).getByRole("button")).toHaveCount(0);
    await expect(toolbar(page, "Up")).toBeDisabled();
    await expect(page.getByText("Nothing selected")).toBeVisible();
    await row(page, 2).getByRole("checkbox").check();
    await page.screenshot({ path: testInfo.outputPath("queue-desktop.png"), fullPage: true });
    await toolbar(page, "Up").click();
    await expect.poll(() => moves[0]).toEqual({ action: "before", ids: [2], anchorId: 1 });
    await expect(page.locator("tbody > tr").nth(1)).toContainText("Chapter 2");
    await row(page, 2).getByRole("checkbox").check();
    await row(page, 3).getByRole("checkbox").check();
    await toolbar(page, "Bottom").click();
    await expect.poll(() => moves[1]).toMatchObject({ action: "bottom", ids: [2, 3] });
    await expect(page.locator("tbody > tr").nth(1)).toContainText("Chapter 1");
    // Now 1 2 3: move 3 up to scramble, then sort the selection back.
    await row(page, 3).getByRole("checkbox").check();
    await toolbar(page, "Up").click();
    await expect(page.locator("tbody > tr").nth(2)).toContainText("Chapter 3");
    for (const id of [1, 2, 3]) await row(page, id).getByRole("checkbox").check();
    await page.screenshot({ path: testInfo.outputPath("queue-sort.png"), fullPage: true });
    await toolbar(page, "Sort by chapter").click();
    await expect.poll(() => moves.at(-1)).toMatchObject({ action: "sort" });
    await expect(page.locator("tbody > tr").nth(1)).toContainText("Chapter 1");
    await expect(page.locator("tbody > tr").nth(2)).toContainText("Chapter 2");
    await expect(page.locator("tbody > tr").nth(3)).toContainText("Chapter 3");
  });
}

test("phone: queue rows are cards and the page doesn't scroll sideways", async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await fixture(page);
  await page.goto("/activity/downloads?group=flat");
  await expect(page.getByRole("listitem").filter({ hasText: "Chapter 1 ·" })).toBeVisible();
  await page.getByRole("listitem").filter({ hasText: "Chapter 1 ·" }).getByRole("checkbox").check();
  await page.screenshot({ path: testInfo.outputPath("queue-mobile.png"), fullPage: true });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
});

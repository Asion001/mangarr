import { expect, test, type Page } from "@playwright/test";

const profile = {
  id: 1,
  name: "Default",
  isDefault: true,
  createdAt: "2026-09-24T00:00:00Z",
  updatedAt: "2026-09-24T00:00:00Z",
  config: {
    preferredScanlators: [], blockedScanlators: [], allowUpgrades: false, minPages: 0,
    upscale: { enabled: true, upscalerId: 0, minWidth: 1400, maxWidth: 2048, model: "legacy-model", noise: 1, format: "source", quality: 90 },
    encode: { format: "keep", preset: "balanced", quality: 0, speed: 0, grayscale: true, progressive: false, minSavingsPct: 10, recycleOriginals: true },
    processTiming: "background", processExisting: false, cleanup: {},
  },
};

async function mock(page: Page, catalog: unknown) {
  await page.addInitScript(() => localStorage.setItem("mangarr:ui:anonymous:0", JSON.stringify({ locale: "en", mode: "editing" })));
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["admin"] } } });
    if (path.endsWith("/profiles")) return route.fulfill({ json: [profile] });
    if (path.endsWith("/upscalers/models")) return route.fulfill({ json: catalog });
    if (path.endsWith("/settings/cleanup")) return route.fulfill({ json: {} });
    return route.fulfill({ json: {} });
  });
}

async function openModel(page: Page) {
  await page.goto("/settings/profiles");
  await page.getByRole("button", { name: "Edit" }).click();
  await page.getByRole("button", { name: "Page processing" }).click();
  // the black & white model comes first; the color model select follows it
  return page.getByRole("combobox").first();
}

test("profile model list combines engines and preserves an unavailable saved model", async ({ page }) => {
  await mock(page, {
    models: [
      { name: "realesrgan", description: "Remote", scales: [4], sources: [{ name: "GPU box", available: false }] },
      { name: "waifu2x", description: "Local", scales: [2, 4], sources: [{ name: "Built-in", available: true }] },
    ],
    sources: [
      { name: "Built-in", available: true, devices: ["Intel"] },
      { name: "GPU box", available: false, devices: ["RTX"] },
    ],
  });
  const select = await openModel(page);
  await expect(select).toHaveValue("legacy-model");
  await expect(select.locator("option")).toHaveText([
    "realesrgan — GPU box (offline)",
    "waifu2x — Built-in",
    "legacy-model — unavailable",
  ]);
  await expect(page.getByText("This saved model is unavailable.")).toBeVisible();
});

test("profile explains when only remembered offline models exist", async ({ page }) => {
  await mock(page, {
    models: [{ name: "legacy-model", description: "Remembered", scales: [2], sources: [{ name: "GPU box", available: false }] }],
    sources: [{ name: "GPU box", available: false, devices: ["RTX"] }],
  });
  await openModel(page);
  await expect(page.getByText("No upscaler is available right now. Remembered worker models are still listed.")).toBeVisible();
});

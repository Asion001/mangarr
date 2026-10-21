import { expect, test } from "@playwright/test";

test("downloads a caller-scoped Mihon setup backup and refreshes devices", async ({ page }) => {
  let exportAddress = "";
  let keyLoads = 0;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) {
      await route.fulfill({ json: { authenticated: true, authDisabled: true, account: { kind: "anonymous", id: 0, permissions: ["apps"] } } });
      return;
    }
    if (path.endsWith("/reading/status")) {
      await route.fulfill({ json: { enabled: true, listening: true, address: ":25600", publicUrl: "https://manga.example.test" } });
      return;
    }
    if (path.endsWith("/reading/keys")) {
      keyLoads += 1;
      await route.fulfill({ json: [] });
      return;
    }
    if (path.endsWith("/reading/mihon-backup")) {
      exportAddress = route.request().postDataJSON().address;
      await route.fulfill({
        body: "backup-bytes",
        contentType: "application/octet-stream",
        headers: { "Content-Disposition": 'attachment; filename="mangarr-mihon-test.tachibk"' },
      });
      return;
    }
    if (path.endsWith("/me/ui-preferences")) {
      await route.fulfill({ json: { locale: "en", mode: "reading" } });
      return;
    }
    await route.fulfill({ json: {} });
  });

  await page.goto("/account/apps");
  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download Mihon setup backup" }).click();
  const download = await downloadPromise;

  expect(download.suggestedFilename()).toBe("mangarr-mihon-test.tachibk");
  expect(exportAddress).toBe("https://manga.example.test");
  await expect.poll(() => keyLoads).toBeGreaterThan(1);
  await expect(page.getByText("Mihon setup backup downloaded")).toBeVisible();
});

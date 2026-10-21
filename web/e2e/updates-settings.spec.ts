import { expect, test, type Page } from "@playwright/test";

async function mockApi(page: Page, account: Record<string, unknown>) {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/status")) return route.fulfill({ json: { authenticated: true, account } });
    if (path.endsWith("/updates")) return route.fulfill({ json: { items: [], total: 0, page: 1, pageSize: 50 } });
    if (path.endsWith("/me/messenger")) {
      return route.fulfill({
        json: { links: [], telegram: { available: false }, discord: { available: false }, mode: "off", events: [], allowInstant: true, digestHour: 9 },
      });
    }
    return route.fulfill({ json: [] });
  });
}

test("the Updates settings button opens your own notification settings", async ({ page }) => {
  await mockApi(page, { kind: "user", id: 2, username: "reader", permissions: [] });
  await page.goto("/updates");

  await page.getByRole("link", { name: "Notification settings" }).click();

  await expect(page).toHaveURL(/\/account\/notifications$/);
  const card = page.locator("#notifications");
  await expect(card).toBeVisible();
  await expect(card).toContainText("The admin hasn't set up a Telegram or Discord bot");
  await expect(card.getByRole("link", { name: "Set up" })).toHaveCount(0);
});

test("an admin without a bot gets a link to set one up", async ({ page }) => {
  await mockApi(page, { kind: "user", id: 1, username: "admin", permissions: ["admin"] });
  // an old link to the notifications card still lands on them
  await page.goto("/account#notifications");
  await expect(page).toHaveURL(/\/account\/notifications$/);

  await page.locator("#notifications").getByRole("link", { name: "Set up" }).click();
  await expect(page).toHaveURL(/\/settings\/notifications$/);
});

test("with logins off the button goes to the server's notification settings", async ({ page }) => {
  await mockApi(page, { kind: "anonymous", id: 0, permissions: ["admin"] });
  await page.goto("/updates");

  await expect(page.getByRole("link", { name: "Notification settings" })).toHaveAttribute("href", /\/settings\/notifications$/);
});

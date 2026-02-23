import { expect, type Page } from "@playwright/test";

export async function loginFromStaffPage(
  page: Page,
  email: string,
  password: string,
  nextPath = "",
): Promise<void> {
  const target = nextPath ? `/app/staff-login/?next=${encodeURIComponent(nextPath)}` : "/app/staff-login/";

  await page.goto(target, { waitUntil: "domcontentloaded" });
  await expect(page.locator("#loginForm")).toBeVisible();

  await page.fill("#emailInput", email);
  await page.fill("#passwordInput", password);
  await page.click("#loginBtn");
}

export async function safeAcceptDialog(page: Page): Promise<void> {
  page.once("dialog", async (dialog) => {
    await dialog.accept();
  });
}

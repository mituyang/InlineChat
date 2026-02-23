import { expect, test } from "@playwright/test";

import { createAgentWithRetry, loginAsSuperAdmin } from "../helpers/api";
import { buildScenarioSeed } from "../helpers/data";
import { e2eEnv, requireSuperAdminCredentials } from "../helpers/env";
import { loginFromStaffPage } from "../helpers/ui";

test("staff-login：按角色跳转到对应控制台", async ({ page, request }) => {
  requireSuperAdminCredentials();

  const seed = buildScenarioSeed("staff-login");
  const superToken = await loginAsSuperAdmin(request);
  const agent = await createAgentWithRetry(request, superToken, seed.agent);

  await loginFromStaffPage(page, e2eEnv.superAdminEmail, e2eEnv.superAdminPassword);
  await expect(page).toHaveURL(/\/app\/admin\/?$/);
  await expect(page.locator("#userBox")).toContainText(e2eEnv.superAdminEmail);

  await page.click("#logoutBtn");
  await expect(page).toHaveURL(/\/app\/staff-login\//);

  await loginFromStaffPage(page, agent.email, agent.password, "/app/agent/");
  await expect(page).toHaveURL(/\/app\/agent\/?$/);
  await expect(page.locator("#userBox")).toContainText(agent.email);
});

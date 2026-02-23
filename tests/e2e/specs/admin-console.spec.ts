import { expect, test } from "@playwright/test";

import { loginAsSuperAdmin, pickAvailableAgentID } from "../helpers/api";
import { buildScenarioSeed } from "../helpers/data";
import { e2eEnv, requireSuperAdminCredentials } from "../helpers/env";
import { loginFromStaffPage } from "../helpers/ui";

test("admin-console：创建站点与坐席后列表可见", async ({ page, request }) => {
  requireSuperAdminCredentials();

  const seed = buildScenarioSeed("admin-console");
  const superToken = await loginAsSuperAdmin(request);
  const availableAgentID = await pickAvailableAgentID(request, superToken, seed.agent.agentID);
  const agent = {
    ...seed.agent,
    agentID: availableAgentID,
  };

  await loginFromStaffPage(page, e2eEnv.superAdminEmail, e2eEnv.superAdminPassword, "/app/admin/");
  await expect(page).toHaveURL(/\/app\/admin\/?$/);

  await page.fill("#siteIdInput", seed.site.siteID);
  await page.fill("#siteNameInput", seed.site.siteName);
  await page.fill("#siteDomainInput", seed.site.siteDomain);
  await page.locator("#createSiteForm button[type='submit']").click();

  await expect(page.locator("#statusLine")).toContainText("站点创建成功");
  await expect(page.locator("#siteList")).toContainText(seed.site.siteID);

  await page.fill("#agentIdInput", agent.agentID);
  await page.fill("#agentEmailInput", agent.email);
  await page.fill("#agentPasswordInput", agent.password);
  await page.fill("#agentDisplayNameInput", agent.displayName);
  await page.locator("#createAgentForm button[type='submit']").click();

  await expect(page.locator("#statusLine")).toContainText("坐席创建成功");
  await expect(page.locator("#agentList")).toContainText(agent.email);
});

import { expect, test } from "@playwright/test";

import { createSite, loginAsSuperAdmin } from "../helpers/api";
import { buildScenarioSeed } from "../helpers/data";
import { requireSuperAdminCredentials } from "../helpers/env";

test("widget-chat：打开会话、发送消息、历史可见", async ({ page, request }) => {
  requireSuperAdminCredentials();

  const seed = buildScenarioSeed("widget-chat");
  const superToken = await loginAsSuperAdmin(request);
  await createSite(request, superToken, seed.site);

  await page.addInitScript(
    ({ siteID, visitorToken }) => {
      const key = `inlinechat.widget.visitor_token.${siteID}`;
      localStorage.setItem(key, visitorToken);
    },
    {
      siteID: seed.site.siteID,
      visitorToken: seed.visitorToken,
    },
  );

  await page.goto(`/app/widget/?site_id=${encodeURIComponent(seed.site.siteID)}&title=${encodeURIComponent("E2E Widget")}`);

  await page.click("#startChatBtn");
  await expect(page.locator("#chatView")).toBeVisible();

  const visitorText = `widget-visitor-${seed.suffix}`;
  await page.fill("#contentInput", visitorText);
  await page.click("#sendBtn");

  await expect(page.locator("#messages")).toContainText(visitorText);

  await page.click("#backBtn");

  const historyItem = page.locator(".history-item").first();
  await expect(historyItem).toBeVisible();
  await expect(historyItem).toContainText(visitorText);

  await historyItem.click();
  await expect(page.locator("#chatView")).toBeVisible();
  await expect(page.locator("#messages")).toContainText(visitorText);
});

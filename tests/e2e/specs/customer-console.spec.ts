import { expect, test } from "@playwright/test";

import { createSite, loginAsSuperAdmin } from "../helpers/api";
import { buildScenarioSeed } from "../helpers/data";
import { requireSuperAdminCredentials } from "../helpers/env";

test("customer-console：建会话并发送消息可回显", async ({ page, request }) => {
  requireSuperAdminCredentials();

  const seed = buildScenarioSeed("customer-console");
  const superToken = await loginAsSuperAdmin(request);
  await createSite(request, superToken, seed.site);

  await page.addInitScript(
    ({ siteID, visitorToken }) => {
      localStorage.setItem("inlinechat.customer.site_id", siteID);
      localStorage.setItem("inlinechat.customer.visitor_token", visitorToken);
    },
    {
      siteID: seed.site.siteID,
      visitorToken: seed.visitorToken,
    },
  );

  await page.goto("/app/customer/", { waitUntil: "domcontentloaded" });

  await page.fill("#siteIdInput", seed.site.siteID);
  await page.click("#newBtn");

  await expect(page.locator("#conversationIdInput")).toHaveValue(/\d+/);
  await expect(page.locator("#statusLine")).toContainText(/会话 #\d+/);

  const visitorText = `visitor-${seed.suffix}`;
  await page.fill("#contentInput", visitorText);
  await page.click("#sendBtn");

  await expect(page.locator("#messages")).toContainText(visitorText);
});

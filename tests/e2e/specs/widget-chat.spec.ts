import fs from "node:fs";
import path from "node:path";

import { expect, test } from "@playwright/test";

import { createAgentWithRetry, createSite, loginAsSuperAdmin, loginByPassword } from "../helpers/api";
import { buildScenarioSeed } from "../helpers/data";
import { e2eEnv, requireSuperAdminCredentials } from "../helpers/env";

test("widget-chat：打开会话、发送消息、历史可见", async ({ page, request }) => {
  requireSuperAdminCredentials();

  const seed = buildScenarioSeed("widget-chat");
  const superToken = await loginAsSuperAdmin(request);
  await createSite(request, superToken, seed.site);

  await page.addInitScript(
    ({ siteID, siteDomain, visitorToken }) => {
      const key = `inlinechat.widget.visitor_token.${siteID}@${siteDomain}`;
      localStorage.setItem(key, visitorToken);
    },
    {
      siteID: seed.site.siteID,
      siteDomain: seed.site.siteDomain,
      visitorToken: seed.visitorToken,
    },
  );

  await page.goto(
    `/app/widget/?site_id=${encodeURIComponent(seed.site.siteID)}&title=${encodeURIComponent("E2E Widget")}&parent_origin=${encodeURIComponent(e2eEnv.baseOrigin)}`,
    {
      waitUntil: "domcontentloaded",
      referer: `${e2eEnv.baseOrigin}/tests/e2e/widget-host`,
    },
  );

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

test("widget-sdk：客服发消息后悬浮图标显示未读数", async ({ page, request }) => {
  requireSuperAdminCredentials();

  const seed = buildScenarioSeed("widget-badge");
  const superToken = await loginAsSuperAdmin(request);
  await createSite(request, superToken, seed.site);
  const agent = await createAgentWithRetry(request, superToken, seed.agent);
  const agentToken = await loginByPassword(request, agent.email, agent.password);
  const sdkSource = fs.readFileSync(path.join(e2eEnv.rootDir, "apps/widget-sdk/inlinechat-widget.js"), "utf8");
  const widgetAppSource = fs.readFileSync(path.join(e2eEnv.rootDir, "apps/widget-chat/app.js"), "utf8");

  await page.route("**/sdk/inlinechat-widget.js", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/javascript; charset=utf-8",
      body: sdkSource,
    });
  });
  await page.route("**/app/widget/app.js", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/javascript; charset=utf-8",
      body: widgetAppSource,
    });
  });

  await page.goto("/app/staff-login/", { waitUntil: "domcontentloaded" });
  await page.evaluate(
    ({ gatewayOrigin, siteID, title }) => {
      document.head.innerHTML = `
        <meta charset="UTF-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1.0" />
        <title>Widget Badge E2E</title>
      `;
      document.body.innerHTML = "";

      const script = document.createElement("script");
      script.src = `${gatewayOrigin}/sdk/inlinechat-widget.js`;
      script.dataset.siteId = siteID;
      script.dataset.title = title;
      document.body.appendChild(script);
    },
    {
      gatewayOrigin: e2eEnv.baseOrigin,
      siteID: seed.site.siteID,
      title: "E2E 在线客服",
    },
  );

  const launcher = page.locator('[data-inlinechat-host="true"] button');
  await expect(launcher).toBeVisible();
  await launcher.click();

  const frame = page.frameLocator('iframe[title="E2E 在线客服"]');
  await frame.locator("#startChatBtn").click();

  const visitorText = `widget-badge-visitor-${seed.suffix}`;
  await frame.locator("#contentInput").fill(visitorText);
  const createConversationPromise = page.waitForResponse((response) => {
    if (response.request().method() !== "POST") {
      return false;
    }
    return new URL(response.url()).pathname === "/api/chat/v1/conversations";
  });
  await frame.locator("#sendBtn").click();

  const createConversationResp = await createConversationPromise;
  expect(createConversationResp.ok()).toBeTruthy();
  const createdConversation = await createConversationResp.json();
  const conversationID = String(createdConversation?.id || "").trim();
  expect(conversationID).toMatch(/^\d+$/);

  await expect(frame.locator("#messages")).toContainText(visitorText);
  await frame.locator("#closeBtn").click();
  await expect(page.locator('iframe[title="E2E 在线客服"]')).toBeHidden();

  const claimResp = await request.post(`/api/chat/v1/conversations/${conversationID}/claim`, {
    headers: {
      Authorization: `Bearer ${agentToken}`,
    },
  });
  expect(claimResp.ok()).toBeTruthy();

  const agentText = `widget-badge-agent-${seed.suffix}`;
  const sendAgentResp = await request.post(`/api/chat/v1/conversations/${conversationID}/messages`, {
    headers: {
      Authorization: `Bearer ${agentToken}`,
      "Content-Type": "application/json",
    },
    data: {
      sender_type: "agent",
      content: agentText,
      client_msg_id: `agent_${seed.suffix}`,
    },
  });
  expect(sendAgentResp.ok()).toBeTruthy();

  const badge = page.locator('[data-inlinechat-unread-badge="true"]');
  await expect(badge).toBeVisible();
  await expect(badge).toHaveText("1");
});

import { expect, test } from "@playwright/test";

import {
  createAgentWithRetry,
  createConversation,
  createSite,
  loginAsSuperAdmin,
  sendVisitorMessage,
} from "../helpers/api";
import { buildScenarioSeed } from "../helpers/data";
import { requireSuperAdminCredentials } from "../helpers/env";
import { loginFromStaffPage, safeAcceptDialog } from "../helpers/ui";

test("agent-console：认领、回复并关闭会话", async ({ page, request }) => {
  requireSuperAdminCredentials();

  const seed = buildScenarioSeed("agent-console");
  const superToken = await loginAsSuperAdmin(request);

  await createSite(request, superToken, seed.site);
  const agent = await createAgentWithRetry(request, superToken, seed.agent);

  const conversation = await createConversation(request, seed.site, seed.visitorToken);
  const conversationID = String(conversation.id);
  await sendVisitorMessage(
    request,
    conversationID,
    seed.visitorToken,
    `visitor-first-${seed.suffix}`,
    `visitor-msg-${seed.suffix}`,
  );

  await loginFromStaffPage(page, agent.email, agent.password, "/app/agent/");
  await expect(page).toHaveURL(/\/app\/agent\/?$/);

  await page.fill("#conversationSearchInput", conversationID);
  await page.click("#refreshConversationsBtn");

  const conversationItem = page.locator(".conversation-item", {
    hasText: `#${conversationID}`,
  });
  await expect(conversationItem).toBeVisible();
  await conversationItem.click();

  await expect(page.locator("#activeConversationTitle")).toContainText(`#${conversationID}`);

  await expect(page.locator("#claimBtn")).toBeEnabled();
  await page.click("#claimBtn");
  await expect(page.locator("#statusLine")).toContainText("认领成功");

  const agentReply = `agent-reply-${seed.suffix}`;
  await page.fill("#agentContentInput", agentReply);
  await expect(page.locator("#agentSendBtn")).toBeEnabled();
  await page.click("#agentSendBtn");
  await expect(page.locator("#agentMessages")).toContainText(agentReply);

  await safeAcceptDialog(page);
  await expect(page.locator("#closeBtn")).toBeEnabled();
  await page.click("#closeBtn");

  await expect(page.locator("#statusLine")).not.toContainText("[object Object]");
  await expect(page.locator("#detailStatus")).toContainText("closed");
});

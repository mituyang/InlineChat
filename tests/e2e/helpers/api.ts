import type { APIRequestContext } from "@playwright/test";

import { e2eEnv } from "./env";

type RequestOptions = {
  token?: string;
  data?: unknown;
  headers?: Record<string, string>;
};

export type SitePayload = {
  siteID: string;
  siteName: string;
  siteDomain: string;
};

export type AgentPayload = {
  agentID: string;
  email: string;
  password: string;
  displayName: string;
  siteID: string;
};

function isConflictError(error: unknown): boolean {
  return String(error).includes("] 409 ");
}

function nextAgentPayload(base: AgentPayload, attempt: number): AgentPayload {
  const num = Number.parseInt(base.agentID, 10);
  const safeNum = Number.isInteger(num) && num >= 1000 && num <= 9999 ? num : 1000;
  const nextNum = ((safeNum - 1000 + attempt * 17) % 9000) + 1000;
  const nextAgentID = String(nextNum).padStart(4, "0");

  const [localPart, domainPartRaw] = base.email.split("@");
  const domainPart = domainPartRaw || "example.com";
  const nextLocal = `${localPart || "agent"}r${attempt}`.slice(0, 54);
  const nextEmail = `${nextLocal}@${domainPart}`;

  return {
    ...base,
    agentID: nextAgentID,
    email: nextEmail,
    displayName: `${base.displayName} R${attempt}`,
  };
}

function extractErrorMessage(body: any, status: number): string {
  if (typeof body?.error === "string" && body.error.trim()) {
    return body.error.trim();
  }
  if (typeof body?.error?.message === "string" && body.error.message.trim()) {
    return body.error.message.trim();
  }
  if (typeof body?.message === "string" && body.message.trim()) {
    return body.message.trim();
  }
  return `请求失败 (${status})`;
}

async function requestJSON(
  request: APIRequestContext,
  method: "GET" | "POST" | "PATCH",
  path: string,
  options: RequestOptions = {},
): Promise<any> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };

  if (options.token) {
    headers.Authorization = `Bearer ${options.token}`;
  }
  if (options.headers) {
    Object.assign(headers, options.headers);
  }

  const response = await request.fetch(path, {
    method,
    headers,
    data: options.data,
  });

  const text = await response.text();
  let body: any = {};
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      body = {};
    }
  }

  if (!response.ok()) {
    const message = extractErrorMessage(body, response.status());
    throw new Error(`[${method} ${path}] ${response.status()} ${message}`);
  }

  return body;
}

export async function loginByPassword(request: APIRequestContext, email: string, password: string): Promise<string> {
  const data = await requestJSON(request, "POST", "/api/auth/v1/auth/login", {
    data: { email, password },
  });

  const token = String(data?.token || "").trim();
  if (!token) {
    throw new Error("登录成功但未返回 token");
  }
  return token;
}

export async function loginAsSuperAdmin(request: APIRequestContext): Promise<string> {
  return loginByPassword(request, e2eEnv.superAdminEmail, e2eEnv.superAdminPassword);
}

export async function createSite(request: APIRequestContext, token: string, payload: SitePayload): Promise<any> {
  return requestJSON(request, "POST", "/api/admin/v1/admin/sites", {
    token,
    data: {
      site_id: payload.siteID,
      name: payload.siteName,
      domain: payload.siteDomain,
    },
  });
}

export async function createAgent(request: APIRequestContext, token: string, payload: AgentPayload): Promise<any> {
  return requestJSON(request, "POST", "/api/admin/v1/admin/agents", {
    token,
    data: {
      agent_id: payload.agentID,
      email: payload.email,
      password: payload.password,
      display_name: payload.displayName,
      role: "agent",
      site_id: payload.siteID,
    },
  });
}

export async function createAgentWithRetry(
  request: APIRequestContext,
  token: string,
  payload: AgentPayload,
  maxAttempts = 12,
): Promise<AgentPayload> {
  let candidate = { ...payload };

  for (let i = 0; i < maxAttempts; i += 1) {
    try {
      await createAgent(request, token, candidate);
      return candidate;
    } catch (error) {
      if (!isConflictError(error)) {
        throw error;
      }
      candidate = nextAgentPayload(candidate, i + 1);
    }
  }

  throw new Error(`创建坐席多次冲突，已重试 ${maxAttempts} 次仍失败`);
}

export async function pickAvailableAgentID(
  request: APIRequestContext,
  token: string,
  preferredID: string,
): Promise<string> {
  const data = await requestJSON(request, "GET", "/api/admin/v1/admin/agents?limit=200", { token });
  const used = new Set<number>();
  for (const item of data?.items || []) {
    const id = Number(item?.id || 0);
    if (Number.isInteger(id) && id >= 1000 && id <= 9999) {
      used.add(id);
    }
  }

  const preferredNum = Number.parseInt(preferredID, 10);
  const seed = Number.isInteger(preferredNum) && preferredNum >= 1000 && preferredNum <= 9999 ? preferredNum : 1000;
  for (let i = 0; i < 9000; i += 1) {
    const candidate = ((seed - 1000 + i) % 9000) + 1000;
    if (!used.has(candidate)) {
      return String(candidate).padStart(4, "0");
    }
  }

  throw new Error("没有可用的 4 位坐席 ID（1000-9999 已被占满）");
}

function extractWidgetSession(html: string): string {
  const match = String(html || "").match(/window\.__INLINECHAT_WIDGET_SESSION__=("([^"\\]|\\.)*")/);
  if (!match?.[1]) {
    return "";
  }
  try {
    return String(JSON.parse(match[1]) || "").trim();
  } catch {
    return "";
  }
}

export async function fetchWidgetSession(request: APIRequestContext, site: SitePayload): Promise<string> {
  const path = `/app/widget/?site_id=${encodeURIComponent(site.siteID)}&parent_origin=${encodeURIComponent(e2eEnv.baseOrigin)}`;
  const response = await request.fetch(path, {
    method: "GET",
    headers: {
      Referer: `${e2eEnv.baseOrigin}/tests/e2e`,
      Origin: e2eEnv.baseOrigin,
    },
  });

  const html = await response.text();
  if (!response.ok()) {
    throw new Error(`[GET ${path}] ${response.status()} ${html.trim() || "获取 widget session 失败"}`);
  }

  const session = extractWidgetSession(html);
  if (!session) {
    throw new Error(`[GET ${path}] 200 未找到 widget session`);
  }
  return session;
}

export async function createConversation(request: APIRequestContext, site: SitePayload, visitorToken: string): Promise<any> {
  const widgetSession = await fetchWidgetSession(request, site);
  return requestJSON(request, "POST", "/api/chat/v1/conversations", {
    headers: {
      "X-InlineChat-Widget-Session": widgetSession,
    },
    data: {
      site_id: site.siteID,
      visitor_token: visitorToken,
    },
  });
}

export async function sendVisitorMessage(
  request: APIRequestContext,
  conversationID: string | number,
  visitorToken: string,
  content: string,
  clientMsgID: string,
): Promise<any> {
  return requestJSON(request, "POST", `/api/chat/v1/conversations/${conversationID}/messages`, {
    data: {
      sender_type: "visitor",
      content,
      client_msg_id: clientMsgID,
      visitor_token: visitorToken,
    },
  });
}
